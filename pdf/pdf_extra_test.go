package pdf

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signintech/gopdf"
	"golang.org/x/image/font/gofont/goregular"
)

func writePNG(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	path := filepath.Join(t.TempDir(), "img.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create png: %v", err)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		t.Fatalf("encode png: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close png: %v", err)
	}
	return path
}

func TestOptionApplication(t *testing.T) {
	size := gopdf.Rect{W: 300, H: 400}
	doc, err := New(
		WithPageSize(size),
		WithLandscape(true),
		WithMargins(Margins{Left: 10, Top: 20, Right: 30, Bottom: 40}),
		WithTitleSize(18),
		WithHeadingSize(12),
		WithLineHeight(1.8),
		WithPageNumbers(false),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if doc.pageSize.W != 400 || doc.pageSize.H != 300 {
		t.Fatalf("landscape page size = %+v", doc.pageSize)
	}
	if doc.opts.TitleSize != 18 || doc.opts.HeadingSize != 12 || doc.opts.LineHeight != 1.8 {
		t.Fatalf("options not applied: %+v", doc.opts)
	}
	if doc.opts.PageNumbers {
		t.Fatal("page numbers should be disabled")
	}
	doc.Spacer(12)
	doc.Spacer(0)
	if err := doc.Paragraph("hello"); err != nil {
		t.Fatalf("Paragraph: %v", err)
	}
	if _, err := doc.Bytes(); err != nil {
		t.Fatalf("Bytes: %v", err)
	}
}

func TestNewValidation(t *testing.T) {
	cases := []Option{
		WithFontSize(0),
		WithTitleSize(-1),
		WithHeadingSize(0),
		WithMargins(Margins{Left: -1}),
		WithMargins(Margins{Top: -1}),
		WithFontData("", goregular.TTF),
		WithFontData("broken", []byte("not a ttf font")),
		WithFontFile("empty", ""),
	}
	for i, opt := range cases {
		if doc, err := New(opt); err == nil {
			_ = doc
			t.Fatalf("case %d: expected error", i)
		}
	}

	// 无效页面尺寸回退到 A4，line height 回退到默认值。
	doc, err := New(WithPageSize(gopdf.Rect{}), WithLineHeight(0))
	if err != nil {
		t.Fatalf("New fallback: %v", err)
	}
	if doc.pageSize.W != gopdf.PageSizeA4.W || doc.pageSize.H != gopdf.PageSizeA4.H {
		t.Fatalf("fallback page size = %+v", doc.pageSize)
	}
	if doc.opts.LineHeight != defaultLineRate {
		t.Fatalf("line height = %v", doc.opts.LineHeight)
	}

	// 字体文件路径注册成功分支。
	fontPath := filepath.Join(t.TempDir(), "regular.ttf")
	if err := os.WriteFile(fontPath, goregular.TTF, 0o600); err != nil {
		t.Fatalf("write font: %v", err)
	}
	doc, err = New(WithFontFile("custom", fontPath), WithDefaultFont("custom"))
	if err != nil {
		t.Fatalf("New with font file: %v", err)
	}
	if err := doc.Paragraph("custom font from file"); err != nil {
		t.Fatalf("Paragraph: %v", err)
	}
	if _, err := doc.Bytes(); err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	// 字体文件存在但内容非法。
	badFont := filepath.Join(t.TempDir(), "bad.ttf")
	if err := os.WriteFile(badFont, []byte("bad"), 0o600); err != nil {
		t.Fatalf("write bad font: %v", err)
	}
	if _, err := New(WithFontFile("bad", badFont)); err == nil {
		t.Fatal("expected invalid font data error")
	}
}

func TestFinalizeErrorPaths(t *testing.T) {
	// 页眉绘制时字体缺失导致 finalize 失败。
	doc, err := New(WithHeader("header"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	doc.opts.FontFamily = "missing"
	if _, err := doc.Bytes(); err == nil {
		t.Fatal("expected finalize error in Bytes")
	}
	if err := doc.Save(filepath.Join(t.TempDir(), "out.pdf")); err == nil {
		t.Fatal("expected finalize error in Save")
	}

	// 正常文档第二次 Bytes 走已 finalize 分支。
	ok, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := ok.Bytes(); err != nil {
		t.Fatalf("first Bytes: %v", err)
	}
	if _, err := ok.Bytes(); err != nil {
		t.Fatalf("second Bytes: %v", err)
	}

	// 没有页面的文档直接 finalize。
	empty := &Document{opts: defaultOptions(), pdf: &gopdf.GoPdf{}}
	empty.pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := empty.finalize(); err != nil {
		t.Fatalf("finalize empty: %v", err)
	}
	if !empty.finalized {
		t.Fatal("empty document should be finalized")
	}
}

func TestSaveWriteError(t *testing.T) {
	doc, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := doc.Save(filepath.Join(t.TempDir(), "missing", "out.pdf")); err == nil {
		t.Fatal("expected write error")
	}
}

func TestImageValidation(t *testing.T) {
	path := writePNG(t, 4, 2)
	doc, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := doc.Image("", 10, 10); err == nil {
		t.Fatal("expected empty path error")
	}
	if err := doc.Image(path, 0, 10); err == nil {
		t.Fatal("expected invalid size error")
	}
	// 宽度超过内容区时按比例缩小。
	if err := doc.Image(path, doc.contentWidth()*2, 20); err != nil {
		t.Fatalf("Image scaling: %v", err)
	}
	if err := doc.Image(filepath.Join(t.TempDir(), "missing.png"), 10, 10); err == nil {
		t.Fatal("expected missing image error")
	}
	if _, err := doc.Bytes(); err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if err := doc.Image(path, 10, 10); err != ErrClosed {
		t.Fatalf("Image after finalize = %v, want ErrClosed", err)
	}
}

func TestTextEdgeCases(t *testing.T) {
	doc, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := doc.Paragraph("   \n\t "); err != nil {
		t.Fatalf("blank Paragraph: %v", err)
	}
	if err := doc.Paragraph("first\n\nsecond"); err != nil {
		t.Fatalf("Paragraph with blank line: %v", err)
	}
	// 极小页面迫使 ensureSpace 分页。
	small, err := New(
		WithPageSize(gopdf.Rect{W: 200, H: 100}),
		WithMargins(Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}),
	)
	if err != nil {
		t.Fatalf("New small: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := small.Paragraph("content on a very small page"); err != nil {
			t.Fatalf("Paragraph %d: %v", i, err)
		}
	}
	if small.Pages() < 2 {
		t.Fatalf("pages = %d, want >= 2", small.Pages())
	}
	if _, err := small.Bytes(); err != nil {
		t.Fatalf("Bytes: %v", err)
	}
}

func TestTableEdgeCases(t *testing.T) {
	doc, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 空表头单元格、空数据单元格、字号下限。
	if err := doc.Table(Table{
		Headers:  []string{"", "B"},
		Rows:     [][]string{{"", "x"}},
		FontSize: 1,
	}); err != nil {
		t.Fatalf("Table empty cells: %v", err)
	}
	// 列宽数量多于列数、权重全为 0。
	if err := doc.Table(Table{
		Headers: []string{"A"},
		Rows:    [][]string{{"value"}},
		Widths:  []float64{0, 0, 0},
	}); err != nil {
		t.Fatalf("Table widths: %v", err)
	}
	// 单元格自动换行。
	if err := doc.Table(Table{
		Headers: []string{"H"},
		Rows:    [][]string{{strings.Repeat("long content ", 40)}},
		Widths:  []float64{1},
	}); err != nil {
		t.Fatalf("Table wrapping: %v", err)
	}
	if _, err := doc.Bytes(); err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if err := doc.Table(Table{Headers: []string{"A"}}); err != ErrClosed {
		t.Fatalf("Table after finalize = %v, want ErrClosed", err)
	}
}

func TestTableSetFontError(t *testing.T) {
	doc, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	doc.opts.FontFamily = "missing"
	if err := doc.Table(Table{Headers: []string{"A"}, Rows: [][]string{{"1"}}}); err == nil {
		t.Fatal("expected table font error")
	}
	if err := doc.Paragraph("text"); err == nil {
		t.Fatal("expected paragraph font error")
	}
}
