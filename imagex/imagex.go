// Package imagex 提供轻量的图片处理能力：解码、缩放、等比适配、居中裁剪、
// 缩略图以及 JPEG/PNG 重新编码。
//
// 设计目标：
//   - 只依赖标准库与 golang.org/x/image，不引入重量级图像库；
//   - 解码前先读取图片尺寸，支持最大像素限制，避免解压炸弹；
//   - 重新编码会丢弃 EXIF 等元数据，降低隐私与安全风险；
//   - 处理过程不修改原图，所有函数返回新的 image.Image。
//
// 支持解码：JPEG、PNG、GIF（首帧）、WebP。
// 支持编码：JPEG、PNG；GIF/WebP 输出会回退为 PNG。
package imagex

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	stddraw "image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"strings"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	defaultMaxBytes  = 32 << 20 // 32MB
	defaultMaxPixels = 32 << 20 // 3200 万像素
	defaultQuality   = 85
)

var (
	// ErrTooLarge 表示图片字节数超过限制。
	ErrTooLarge = errors.New("imagex: image exceeds size limit")
	// ErrTooManyPixels 表示图片像素数超过限制，可能是解压炸弹。
	ErrTooManyPixels = errors.New("imagex: image exceeds pixel limit")
	// ErrUnsupportedFormat 表示不支持的编码格式。
	ErrUnsupportedFormat = errors.New("imagex: unsupported format")
)

// Info 描述图片尺寸与格式。
type Info struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Format string `json:"format"`
}

// Options 是图片处理参数。
type Options struct {
	// MaxWidth / MaxHeight 是等比缩放后的最大宽高；<=0 表示不限制。
	MaxWidth  int
	MaxHeight int
	// MaxPixels 是解码前的最大像素数，默认 3200 万，防止解压炸弹。
	MaxPixels int64
	// MaxBytes 是输入字节上限，默认 32MB。
	MaxBytes int64
	// Format 是输出格式（jpeg/png）；为空时沿用源格式。
	Format string
	// Quality 是 JPEG 质量，1-100，默认 85。
	Quality int
	// AllowUpscale 为 true 时允许把小图放大到 MaxWidth/MaxHeight。
	AllowUpscale bool
}

// Decode 解码图片并返回原始格式。
func Decode(r io.Reader) (image.Image, string, error) {
	return image.Decode(r)
}

// DecodeConfig 只解析图片头，返回尺寸与格式，不分配完整像素内存。
func DecodeConfig(r io.Reader) (Info, error) {
	cfg, format, err := image.DecodeConfig(r)
	if err != nil {
		return Info{}, fmt.Errorf("imagex: decode config: %w", err)
	}
	return Info{Width: cfg.Width, Height: cfg.Height, Format: format}, nil
}

// Resize 将图片缩放到指定宽高（不保持比例）。
func Resize(img image.Image, width, height int) image.Image {
	if img == nil || width <= 0 || height <= 0 {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), stddraw.Src, nil)
	return dst
}

// Fit 等比缩放到不超过 maxWidth/maxHeight 的尺寸。
// 默认不放大；allowUpscale 为 true 时可以放大。
func Fit(img image.Image, maxWidth, maxHeight int, allowUpscale bool) image.Image {
	if img == nil || (maxWidth <= 0 && maxHeight <= 0) {
		return img
	}
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if srcW <= 0 || srcH <= 0 {
		return img
	}

	ratio := 1.0
	if allowUpscale {
		// 允许放大时从“无限大”开始取目标比例，否则初始 1.0 会让 Min 永远无法放大。
		ratio = math.Inf(1)
	}
	if maxWidth > 0 {
		r := float64(maxWidth) / float64(srcW)
		// 不允许放大时只在超限时缩小；允许放大时按目标尺寸计算。
		if allowUpscale || r < 1 {
			ratio = math.Min(ratio, r)
		}
	}
	if maxHeight > 0 {
		r := float64(maxHeight) / float64(srcH)
		if allowUpscale || r < 1 {
			ratio = math.Min(ratio, r)
		}
	}
	if math.IsInf(ratio, 1) {
		ratio = 1
	}
	if ratio >= 1 && !allowUpscale {
		return img
	}

	width := int(math.Round(float64(srcW) * ratio))
	height := int(math.Round(float64(srcH) * ratio))
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return Resize(img, width, height)
}

// Fill 将图片等比缩放到覆盖目标尺寸，并居中裁剪。
func Fill(img image.Image, width, height int) image.Image {
	if img == nil || width <= 0 || height <= 0 {
		return img
	}
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if srcW <= 0 || srcH <= 0 {
		return img
	}

	ratio := math.Max(float64(width)/float64(srcW), float64(height)/float64(srcH))
	scaledW := int(math.Ceil(float64(srcW) * ratio))
	scaledH := int(math.Ceil(float64(srcH) * ratio))

	scaled := Resize(img, scaledW, scaledH)
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	offsetX := (scaledW - width) / 2
	offsetY := (scaledH - height) / 2
	stddraw.Draw(dst, dst.Bounds(), scaled, image.Pt(offsetX, offsetY), stddraw.Src)
	return dst
}

// Thumbnail 生成最大边为 size 的等比缩略图。
func Thumbnail(img image.Image, size int) image.Image {
	return Fit(img, size, size, false)
}

// Encode 按 format 编码图片。
func Encode(w io.Writer, img image.Image, format string, quality int) error {
	if img == nil {
		return errors.New("imagex: image is nil")
	}
	switch normalizeFormat(format) {
	case "jpeg":
		if quality <= 0 || quality > 100 {
			quality = defaultQuality
		}
		return jpeg.Encode(w, flatten(img), &jpeg.Options{Quality: quality})
	case "png":
		return png.Encode(w, img)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFormat, format)
	}
}

// Process 完成一次完整的图片处理：限流读取 -> 尺寸检查 -> 解码 -> 等比缩放 -> 重新编码。
// 返回编码后的字节、输出尺寸与格式。
func Process(r io.Reader, opts Options) ([]byte, Info, error) {
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	data, err := readAllLimited(r, maxBytes)
	if err != nil {
		return nil, Info{}, err
	}

	cfg, err := DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, Info{}, err
	}
	maxPixels := opts.MaxPixels
	if maxPixels <= 0 {
		maxPixels = defaultMaxPixels
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, Info{}, fmt.Errorf("%w: %dx%d", ErrTooManyPixels, cfg.Width, cfg.Height)
	}

	img, sourceFormat, err := Decode(bytes.NewReader(data))
	if err != nil {
		return nil, Info{}, fmt.Errorf("imagex: decode: %w", err)
	}
	if opts.MaxWidth > 0 || opts.MaxHeight > 0 {
		img = Fit(img, opts.MaxWidth, opts.MaxHeight, opts.AllowUpscale)
	}

	format := normalizeFormat(opts.Format)
	if format == "" {
		format = normalizeFormat(sourceFormat)
	}
	// GIF 可能是动图，重新编码为 PNG 可以保留首帧；WebP 编码不在标准库中。
	if format == "gif" || format == "webp" {
		format = "png"
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, format, opts.Quality); err != nil {
		return nil, Info{}, err
	}
	bounds := img.Bounds()
	return buf.Bytes(), Info{Width: bounds.Dx(), Height: bounds.Dy(), Format: format}, nil
}

func normalizeFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpg", "jpeg":
		return "jpeg"
	case "png":
		return "png"
	case "gif":
		return "gif"
	case "webp":
		return "webp"
	default:
		return ""
	}
}

// flatten 将透明背景合成到白色背景上，避免 JPEG 透明区域变黑。
func flatten(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	stddraw.Draw(dst, dst.Bounds(), image.White, image.Point{}, stddraw.Src)
	stddraw.Draw(dst, dst.Bounds(), img, b.Min, stddraw.Over)
	return dst
}

// readAllLimited 读取全部内容并限制最大字节数。
func readAllLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	if r == nil {
		return nil, errors.New("imagex: reader is nil")
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("imagex: read image: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrTooLarge
	}
	return data, nil
}
