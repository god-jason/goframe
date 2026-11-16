// Package pdf 提供轻量的 PDF 生成能力，面向业务报表、导出单据和简单文档。
//
// 能力：
//   - 标题、正文段落（自动换行）、分页；
//   - 表格（表头、斑马纹、列宽权重、自动分页与表头重复）；
//   - 图片插入；
//   - 页眉、页脚、页码；
//   - 内置 Go Regular 字体，开箱即用；支持注入 TTF 字体（包括中文字体）。
//
// 使用自定义中文字体：
//
//	doc, _ := pdf.New(
//	    pdf.WithFontFile("cjk", "/path/NotoSansSC-Regular.ttf"),
//	    pdf.WithDefaultFont("cjk"),
//	)
//
// 说明：PDF 生成库不会自动做字体回退，中文必须使用包含中文字形的 TTF 字体。
package pdf

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/signintech/gopdf"
	"golang.org/x/image/font/gofont/goregular"
)

var (
	// ErrNoFont 表示默认字体未注册。
	ErrNoFont = errors.New("pdf: default font is not registered")
	// ErrClosed 表示文档已经输出，不能再追加内容。
	ErrClosed = errors.New("pdf: document already finalized")
)

const (
	defaultFont     = "gofont"
	defaultFontSize = 11.0
	defaultTitle    = 22.0
	defaultHeading  = 15.0
	defaultLineRate = 1.5
	cellPadding     = 4.0
)

// Margins 是页面边距（单位：point）。
type Margins struct {
	Left   float64
	Top    float64
	Right  float64
	Bottom float64
}

// FontSource 描述一个可注入的字体。
// Data 与 Path 至少提供一个；两者同时存在时优先使用 Data。
type FontSource struct {
	Name string
	Data []byte
	Path string
}

// Options 是 PDF 文档配置。
type Options struct {
	PageSize    gopdf.Rect
	Landscape   bool
	Margins     Margins
	FontFamily  string
	FontSize    float64
	TitleSize   float64
	HeadingSize float64
	LineHeight  float64
	HeaderText  string
	FooterText  string
	PageNumbers bool
	Title       string
	Author      string
	Subject     string
	Fonts       []FontSource
}

// Option 是配置函数。
type Option func(*Options)

// WithPageSize 设置页面尺寸，通常使用 PageSizeA4 / PageSizeA3 / PageSizeLetter。
func WithPageSize(size gopdf.Rect) Option {
	return func(o *Options) { o.PageSize = size }
}

// WithLandscape 设置横向。
func WithLandscape(enabled bool) Option {
	return func(o *Options) { o.Landscape = enabled }
}

// WithMargins 设置页边距。
func WithMargins(margins Margins) Option {
	return func(o *Options) { o.Margins = margins }
}

// WithFontData 注入 TTF 字体数据。
func WithFontData(name string, data []byte) Option {
	return func(o *Options) {
		o.Fonts = append(o.Fonts, FontSource{Name: name, Data: data})
	}
}

// WithFontFile 注入 TTF 字体文件。
func WithFontFile(name, path string) Option {
	return func(o *Options) {
		o.Fonts = append(o.Fonts, FontSource{Name: name, Path: path})
	}
}

// WithDefaultFont 设置默认字体名称。
func WithDefaultFont(name string) Option {
	return func(o *Options) { o.FontFamily = name }
}

// WithFontSize 设置正文字号。
func WithFontSize(size float64) Option {
	return func(o *Options) { o.FontSize = size }
}

// WithTitleSize 设置标题字号。
func WithTitleSize(size float64) Option {
	return func(o *Options) { o.TitleSize = size }
}

// WithHeadingSize 设置小节标题字号。
func WithHeadingSize(size float64) Option {
	return func(o *Options) { o.HeadingSize = size }
}

// WithLineHeight 设置正文行高倍数。
func WithLineHeight(rate float64) Option {
	return func(o *Options) { o.LineHeight = rate }
}

// WithHeader 设置页眉文本。
func WithHeader(text string) Option {
	return func(o *Options) { o.HeaderText = text }
}

// WithFooter 设置页脚文本。
func WithFooter(text string) Option {
	return func(o *Options) { o.FooterText = text }
}

// WithPageNumbers 设置是否显示页码。
func WithPageNumbers(enabled bool) Option {
	return func(o *Options) { o.PageNumbers = enabled }
}

// WithMetadata 设置 PDF 元数据。
func WithMetadata(title, author, subject string) Option {
	return func(o *Options) {
		o.Title = title
		o.Author = author
		o.Subject = subject
	}
}

func defaultOptions() Options {
	return Options{
		PageSize:    *gopdf.PageSizeA4,
		Margins:     Margins{Left: 50, Top: 60, Right: 50, Bottom: 60},
		FontFamily:  defaultFont,
		FontSize:    defaultFontSize,
		TitleSize:   defaultTitle,
		HeadingSize: defaultHeading,
		LineHeight:  defaultLineRate,
		PageNumbers: true,
	}
}

// Document 是 PDF 文档构建器。
// 所有绘制方法在文档输出前调用；Bytes/Save 之后文档不可再追加内容。
type Document struct {
	opts      Options
	pdf       *gopdf.GoPdf
	pageSize  gopdf.Rect
	page      int
	y         float64
	finalized bool
}

// New 创建 PDF 文档并注册字体。
func New(opts ...Option) (*Document, error) {
	options := defaultOptions()
	for _, opt := range opts {
		opt(&options)
	}
	if options.FontSize <= 0 || options.TitleSize <= 0 || options.HeadingSize <= 0 {
		return nil, fmt.Errorf("pdf: font sizes must be greater than 0")
	}
	if options.LineHeight <= 0 {
		options.LineHeight = defaultLineRate
	}
	if options.Margins.Left < 0 || options.Margins.Right < 0 || options.Margins.Top < 0 || options.Margins.Bottom < 0 {
		return nil, fmt.Errorf("pdf: margins must not be negative")
	}

	size := options.PageSize
	if size.W <= 0 || size.H <= 0 {
		size = *gopdf.PageSizeA4
	}
	if options.Landscape {
		size = gopdf.Rect{W: size.H, H: size.W}
	}

	doc := &Document{opts: options, pageSize: size}
	doc.pdf = &gopdf.GoPdf{}
	doc.pdf.Start(gopdf.Config{PageSize: size})
	doc.pdf.SetMargins(options.Margins.Left, options.Margins.Top, options.Margins.Right, options.Margins.Bottom)

	// 内置 Go 字体仅包含拉丁字形；中文需要 WithFontFile 注入。
	if err := doc.pdf.AddTTFFontData(defaultFont, goregular.TTF); err != nil {
		return nil, fmt.Errorf("pdf: register default font: %w", err)
	}
	for _, font := range options.Fonts {
		if font.Name == "" {
			return nil, fmt.Errorf("pdf: font name must not be empty")
		}
		switch {
		case len(font.Data) > 0:
			if err := doc.pdf.AddTTFFontData(font.Name, font.Data); err != nil {
				return nil, fmt.Errorf("pdf: register font %q: %w", font.Name, err)
			}
		case font.Path != "":
			data, err := os.ReadFile(font.Path)
			if err != nil {
				return nil, fmt.Errorf("pdf: read font %q: %w", font.Name, err)
			}
			if err := doc.pdf.AddTTFFontData(font.Name, data); err != nil {
				return nil, fmt.Errorf("pdf: register font %q: %w", font.Name, err)
			}
		default:
			return nil, fmt.Errorf("pdf: font %q has no data or path", font.Name)
		}
	}

	if err := doc.pdf.SetFont(options.FontFamily, "", options.FontSize); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNoFont, options.FontFamily)
	}

	doc.pdf.SetInfo(gopdf.PdfInfo{
		Title:    options.Title,
		Author:   options.Author,
		Subject:  options.Subject,
		Creator:  "goframe",
		Producer: "goframe pdf",
	})
	doc.pdf.AddPage()
	// 写入一个不可见的白色像素，确保空白文档也拥有有效页面对象。
	doc.pdf.SetFillColor(255, 255, 255)
	doc.pdf.RectFromUpperLeftWithStyle(0, 0, 1, 1, "F")
	doc.page = 1
	doc.y = options.Margins.Top
	return doc, nil
}

// Pages 返回当前页数。
func (d *Document) Pages() int { return d.pdf.GetNumberOfPages() }

// Title 绘制文档标题。
func (d *Document) Title(text string) error {
	return d.drawBlock(text, d.opts.TitleSize, d.opts.TitleSize*1.3, 10)
}

// Heading 绘制小节标题。
func (d *Document) Heading(text string) error {
	return d.drawBlock(text, d.opts.HeadingSize, d.opts.HeadingSize*1.4, 6)
}

// Paragraph 绘制正文段落，自动换行。
func (d *Document) Paragraph(text string) error {
	lineHeight := d.opts.FontSize * d.opts.LineHeight
	return d.drawBlock(text, d.opts.FontSize, lineHeight, 4)
}

// Spacer 增加垂直间距。
func (d *Document) Spacer(height float64) {
	if height > 0 {
		d.y += height
	}
}

// PageBreak 强制分页。
func (d *Document) PageBreak() {
	d.addPage()
}

// Image 插入图片。宽高单位为 point；宽度超过内容区时按比例缩小。
func (d *Document) Image(path string, width, height float64) error {
	if d.finalized {
		return ErrClosed
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("pdf: image path is empty")
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("pdf: image width and height must be greater than 0")
	}
	contentWidth := d.contentWidth()
	if width > contentWidth {
		scale := contentWidth / width
		width = contentWidth
		height *= scale
	}
	d.ensureSpace(height + 6)
	if err := d.pdf.Image(path, d.opts.Margins.Left, d.y, &gopdf.Rect{W: width, H: height}); err != nil {
		return fmt.Errorf("pdf: draw image: %w", err)
	}
	d.y += height + 6
	return nil
}

// Bytes 输出 PDF 字节。
func (d *Document) Bytes() ([]byte, error) {
	if err := d.finalize(); err != nil {
		return nil, err
	}
	data, err := d.pdf.GetBytesPdfReturnErr()
	if err != nil {
		return nil, fmt.Errorf("pdf: get bytes: %w", err)
	}
	return data, nil
}

// Save 写入文件。
func (d *Document) Save(path string) error {
	data, err := d.Bytes()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("pdf: write file: %w", err)
	}
	return nil
}

func (d *Document) contentWidth() float64 {
	return d.pageSize.W - d.opts.Margins.Left - d.opts.Margins.Right
}

func (d *Document) contentBottom() float64 {
	return d.pageSize.H - d.opts.Margins.Bottom
}

func (d *Document) addPage() {
	d.pdf.AddPage()
	d.page++
	d.y = d.opts.Margins.Top
}

func (d *Document) ensureSpace(height float64) {
	if d.y+height > d.contentBottom() {
		d.addPage()
	}
}

func (d *Document) drawBlock(text string, fontSize, lineHeight, after float64) error {
	if d.finalized {
		return ErrClosed
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if err := d.pdf.SetFont(d.opts.FontFamily, "", fontSize); err != nil {
		return fmt.Errorf("pdf: set font: %w", err)
	}
	d.pdf.SetTextColor(33, 37, 41)
	lines, err := d.wrap(text, d.contentWidth())
	if err != nil {
		return err
	}
	for _, line := range lines {
		d.ensureSpace(lineHeight)
		d.pdf.SetXY(d.opts.Margins.Left, d.y)
		if line != "" {
			if err := d.pdf.Cell(&gopdf.Rect{W: d.contentWidth(), H: lineHeight}, line); err != nil {
				return fmt.Errorf("pdf: draw text: %w", err)
			}
		}
		d.y += lineHeight
	}
	d.y += after
	return nil
}

// wrap 按当前字体逐字符测量并换行，兼容中英文混排。
func (d *Document) wrap(text string, width float64) ([]string, error) {
	var lines []string
	for _, paragraph := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		var current strings.Builder
		measured := 0.0
		for _, r := range paragraph {
			// 逐字符累加宽度，避免每次都测量整行造成 O(n²)。
			widthOfRune, err := d.pdf.MeasureTextWidth(string(r))
			if err != nil {
				return nil, fmt.Errorf("pdf: measure text: %w", err)
			}
			if measured+widthOfRune > width && current.Len() > 0 {
				lines = append(lines, current.String())
				current.Reset()
				measured = 0
			}
			current.WriteRune(r)
			measured += widthOfRune
		}
		if current.Len() > 0 {
			lines = append(lines, current.String())
		}
	}
	return lines, nil
}

func (d *Document) finalize() error {
	if d.finalized {
		return nil
	}
	total := d.pdf.GetNumberOfPages()
	if total <= 0 {
		// 空文档没有内容页，gopdf 不会登记页面；直接输出空 PDF。
		d.finalized = true
		return nil
	}
	for i := 1; i <= total; i++ {
		if err := d.pdf.SetPage(i); err != nil {
			return fmt.Errorf("pdf: set page: %w", err)
		}
		if err := d.drawHeaderFooter(i, total); err != nil {
			return err
		}
	}
	if err := d.pdf.SetPage(total); err != nil {
		return fmt.Errorf("pdf: restore page: %w", err)
	}
	d.finalized = true
	return nil
}

func (d *Document) drawHeaderFooter(page, total int) error {
	if d.opts.HeaderText == "" && d.opts.FooterText == "" && !d.opts.PageNumbers {
		return nil
	}
	if err := d.pdf.SetFont(d.opts.FontFamily, "", 9); err != nil {
		return fmt.Errorf("pdf: set footer font: %w", err)
	}
	d.pdf.SetTextColor(120, 120, 120)

	if d.opts.HeaderText != "" {
		d.pdf.SetXY(d.opts.Margins.Left, 24)
		_ = d.pdf.Cell(&gopdf.Rect{W: d.contentWidth(), H: 14}, d.opts.HeaderText)
		d.pdf.SetStrokeColor(220, 220, 220)
		d.pdf.SetLineWidth(0.4)
		d.pdf.Line(d.opts.Margins.Left, 42, d.pageSize.W-d.opts.Margins.Right, 42)
	}

	footerY := d.pageSize.H - 34
	if d.opts.FooterText != "" {
		d.pdf.SetXY(d.opts.Margins.Left, footerY)
		_ = d.pdf.Cell(&gopdf.Rect{W: d.contentWidth(), H: 14}, d.opts.FooterText)
	}
	if d.opts.PageNumbers {
		label := fmt.Sprintf("Page %d / %d", page, total)
		width, err := d.pdf.MeasureTextWidth(label)
		if err != nil {
			return fmt.Errorf("pdf: measure page number: %w", err)
		}
		d.pdf.SetXY(d.pageSize.W-d.opts.Margins.Right-width, footerY)
		_ = d.pdf.Cell(&gopdf.Rect{W: width + 4, H: 14}, label)
	}
	return nil
}

// PageSizeA4 / A3 / Letter 是对外暴露的常用页面尺寸。
var (
	PageSizeA4     = *gopdf.PageSizeA4
	PageSizeA3     = *gopdf.PageSizeA3
	PageSizeLetter = *gopdf.PageSizeLetter
)
