package imagex

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func makePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestProcessFitAndReencode(t *testing.T) {
	data := makePNG(t, 200, 100)

	out, info, err := Process(bytes.NewReader(data), Options{MaxWidth: 50, MaxHeight: 50})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if info.Width != 50 || info.Height != 25 || info.Format != "png" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if len(out) == 0 {
		t.Fatal("expected encoded output")
	}
	decoded, format, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if format != "png" {
		t.Fatalf("format = %q", format)
	}
	if decoded.Bounds().Dx() != 50 || decoded.Bounds().Dy() != 25 {
		t.Fatalf("decoded size = %v", decoded.Bounds())
	}
}

func TestFitAllowUpscale(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 5))

	upscaled := Fit(src, 40, 20, true)
	if upscaled.Bounds().Dx() != 40 || upscaled.Bounds().Dy() != 20 {
		t.Fatalf("upscaled size = %v, want 40x20", upscaled.Bounds())
	}

	kept := Fit(src, 40, 20, false)
	if kept.Bounds().Dx() != 10 || kept.Bounds().Dy() != 5 {
		t.Fatalf("size without upscale = %v, want 10x5", kept.Bounds())
	}
}

func TestProcessToJPEG(t *testing.T) {
	data := makePNG(t, 40, 20)
	out, info, err := Process(bytes.NewReader(data), Options{Format: "jpg", Quality: 70})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if info.Format != "jpeg" || info.Width != 40 || info.Height != 20 {
		t.Fatalf("unexpected info: %+v", info)
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("decode jpeg: %v", err)
	}
}

func TestProcessLimits(t *testing.T) {
	data := makePNG(t, 100, 100)

	if _, _, err := Process(bytes.NewReader(data), Options{MaxPixels: 1000}); !errors.Is(err, ErrTooManyPixels) {
		t.Fatalf("expected pixel limit error, got %v", err)
	}
	if _, _, err := Process(bytes.NewReader(data), Options{MaxBytes: 16}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected size limit error, got %v", err)
	}
	if _, _, err := Process(bytes.NewReader([]byte("not an image")), Options{}); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestResizeFillThumbnail(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))

	resized := Resize(img, 50, 80)
	if resized.Bounds().Dx() != 50 || resized.Bounds().Dy() != 80 {
		t.Fatalf("resize = %v", resized.Bounds())
	}

	filled := Fill(img, 60, 60)
	if filled.Bounds().Dx() != 60 || filled.Bounds().Dy() != 60 {
		t.Fatalf("fill = %v", filled.Bounds())
	}

	thumb := Thumbnail(img, 50)
	if thumb.Bounds().Dx() != 50 || thumb.Bounds().Dy() != 25 {
		t.Fatalf("thumbnail = %v", thumb.Bounds())
	}

	if Fit(img, 0, 0, false) != img {
		t.Fatal("expected original image when no limits")
	}
}

func TestEncodeRejectsUnsupportedFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1)), "bmp", 80); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("expected unsupported format, got %v", err)
	}
}

func TestDecodeConfig(t *testing.T) {
	data := makePNG(t, 30, 40)
	info, err := DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if info.Width != 30 || info.Height != 40 || info.Format != "png" {
		t.Fatalf("unexpected info: %+v", info)
	}
}
