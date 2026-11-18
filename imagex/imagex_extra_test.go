package imagex

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
)

type failReader struct{ err error }

func (r failReader) Read([]byte) (int, error) { return 0, r.err }

func TestResizeInvalid(t *testing.T) {
	if got := Resize(nil, 10, 10); got != nil {
		t.Fatalf("Resize(nil) = %v, want nil", got)
	}
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	if got := Resize(src, 0, 10); got != src {
		t.Fatalf("Resize zero width = %v, want original", got)
	}
	if got := Resize(src, 10, -1); got != src {
		t.Fatalf("Resize negative height = %v, want original", got)
	}
}

func TestFitEdgeCases(t *testing.T) {
	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
	if got := Fit(empty, 10, 10, false); got != empty {
		t.Fatalf("Fit empty = %v, want original", got)
	}
	if got := Fit(nil, 10, 10, false); got != nil {
		t.Fatalf("Fit nil = %v, want nil", got)
	}

	// 高度被压缩到小于 1 像素时会被夹到 1。
	tall := image.NewRGBA(image.Rect(0, 0, 1, 100))
	if out := Fit(tall, 0, 1, false); out.Bounds().Dx() != 1 || out.Bounds().Dy() != 1 {
		t.Fatalf("tall fit = %v", out.Bounds())
	}
	// 宽度被压缩到小于 1 像素时会被夹到 1。
	wide := image.NewRGBA(image.Rect(0, 0, 100, 1))
	if out := Fit(wide, 1, 0, false); out.Bounds().Dx() != 1 || out.Bounds().Dy() != 1 {
		t.Fatalf("wide fit = %v", out.Bounds())
	}
}

func TestFillEdgeCases(t *testing.T) {
	if got := Fill(nil, 10, 10); got != nil {
		t.Fatalf("Fill nil = %v, want nil", got)
	}
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	if got := Fill(src, 0, 10); got != src {
		t.Fatalf("Fill zero width = %v, want original", got)
	}
	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
	if got := Fill(empty, 10, 10); got != empty {
		t.Fatalf("Fill empty = %v, want original", got)
	}
}

func TestEncodeQualityAndNil(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, nil, "png", 0); err == nil {
		t.Fatal("expected nil image error")
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for _, quality := range []int{0, -1, 101} {
		buf.Reset()
		if err := Encode(&buf, img, "jpeg", quality); err != nil {
			t.Fatalf("Encode jpeg quality %d: %v", quality, err)
		}
		if buf.Len() == 0 {
			t.Fatalf("empty jpeg for quality %d", quality)
		}
	}
	if err := Encode(&buf, img, "PNG", 0); err != nil {
		t.Fatalf("Encode uppercase png: %v", err)
	}
}

func TestProcessReaderErrors(t *testing.T) {
	if _, _, err := Process(nil, Options{}); err == nil {
		t.Fatal("expected nil reader error")
	}
	boom := errors.New("boom")
	if _, _, err := Process(failReader{err: boom}, Options{}); !errors.Is(err, boom) {
		t.Fatalf("Process error = %v, want boom", err)
	}
}

func TestProcessDecodeAfterConfig(t *testing.T) {
	data := makePNG(t, 8, 8)
	// 保留 PNG 签名与 IHDR，DecodeConfig 成功但完整解码失败。
	cfg, err := DecodeConfig(bytes.NewReader(data[:33]))
	if err != nil {
		t.Fatalf("DecodeConfig truncated: %v", err)
	}
	if cfg.Width != 8 || cfg.Height != 8 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if _, _, err := Process(bytes.NewReader(data[:33]), Options{}); err == nil {
		t.Fatal("expected decode error for truncated image")
	}
}

func TestProcessGIFAndWebPFormat(t *testing.T) {
	palette := color.Palette{color.Black, color.White}
	src := image.NewPaletted(image.Rect(0, 0, 4, 4), palette)
	src.SetColorIndex(0, 0, 1)
	var buf bytes.Buffer
	if err := gif.Encode(&buf, src, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	out, info, err := Process(bytes.NewReader(buf.Bytes()), Options{})
	if err != nil {
		t.Fatalf("Process gif: %v", err)
	}
	if info.Format != "png" || len(out) == 0 {
		t.Fatalf("unexpected info: %+v", info)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("output not png: %v", err)
	}

	out, info, err = Process(bytes.NewReader(makePNG(t, 4, 4)), Options{Format: " WEBP "})
	if err != nil {
		t.Fatalf("Process webp format: %v", err)
	}
	if info.Format != "png" || len(out) == 0 {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestNormalizeFormatVariants(t *testing.T) {
	cases := map[string]string{
		"jpg":    "jpeg",
		"JPEG":   "jpeg",
		" png ":  "png",
		"gif":    "gif",
		"webp":   "webp",
		"bmp":    "",
		"":       "",
		"image/": "",
	}
	for input, want := range cases {
		if got := normalizeFormat(input); got != want {
			t.Fatalf("normalizeFormat(%q) = %q, want %q", input, got, want)
		}
	}
}
