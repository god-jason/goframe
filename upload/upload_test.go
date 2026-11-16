package upload

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"

	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/storage"
)

func newTestService(t *testing.T, cfg Config, opts ...Option) (*Service, storage.Storage) {
	t.Helper()
	store, err := storage.NewLocal(storage.Config{Driver: "local", Path: t.TempDir()})
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	service, err := New(store, cfg, logger.NewNop(), opts...)
	if err != nil {
		t.Fatalf("new upload service: %v", err)
	}
	return service, store
}

func testUploadConfig() Config {
	return Config{
		MaxSize:           1024,
		AllowedExtensions: []string{".jpg", ".jpeg", ".png", ".txt", ".csv", ".pdf", ".zip"},
		ImageMaxWidth:     50,
		ImageMaxHeight:    50,
		ImageMaxPixels:    1 << 20,
		ImageQuality:      80,
	}
}

func makePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestSaveTextFile(t *testing.T) {
	service, store := newTestService(t, testUploadConfig())
	ctx := context.Background()

	result, err := service.Save(ctx, "tenant-a", "docs", "readme.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if result.Size != 5 || result.MIME != "text/plain" || result.Name != "readme.txt" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !strings.HasPrefix(result.Key, "tenant-a/docs/") || !strings.HasSuffix(result.Key, ".txt") {
		t.Fatalf("unexpected key: %s", result.Key)
	}
	if result.URL == "" {
		t.Fatal("expected url")
	}

	reader, err := store.Get(ctx, result.Key)
	if err != nil {
		t.Fatalf("get stored file: %v", err)
	}
	defer reader.Close()
	data, _ := io.ReadAll(reader)
	if string(data) != "hello" {
		t.Fatalf("stored content = %q", data)
	}
}

func TestSaveRejectsInvalidTypeAndSize(t *testing.T) {
	service, _ := newTestService(t, testUploadConfig())
	ctx := context.Background()

	if _, err := service.Save(ctx, "t", "docs", "virus.exe", strings.NewReader("x")); err == nil {
		t.Fatal("expected extension rejection")
	}
	if _, err := service.Save(ctx, "t", "docs", "image.txt", bytes.NewReader(makePNG(t, 5, 5))); err == nil {
		t.Fatal("expected extension/mime mismatch")
	}

	_, err := service.Save(ctx, "t", "docs", "big.txt", strings.NewReader(strings.Repeat("a", 2048)))
	if err == nil {
		t.Fatal("expected size limit error")
	}
	if got := apperrors.From(err); got.Status != 413 {
		t.Fatalf("status = %d, want 413", got.Status)
	}
}

func TestSaveSanitizesNamesAndSegments(t *testing.T) {
	service, _ := newTestService(t, testUploadConfig())
	ctx := context.Background()

	result, err := service.Save(ctx, "tenant-a", "docs", "../../etc/passwd.txt", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if result.Name != "passwd.txt" {
		t.Fatalf("name = %q", result.Name)
	}
	if strings.Contains(result.Key, "..") {
		t.Fatalf("key contains traversal: %s", result.Key)
	}

	if _, err := service.Save(ctx, "../evil", "docs", "a.txt", strings.NewReader("x")); err == nil {
		t.Fatal("expected invalid tenant rejection")
	}
	if _, err := service.Save(ctx, "t", "../evil", "a.txt", strings.NewReader("x")); err == nil {
		t.Fatal("expected invalid category rejection")
	}
	if _, err := service.Save(ctx, "t", "docs", "empty.txt", strings.NewReader("")); err == nil {
		t.Fatal("expected empty file rejection")
	}
}

func TestSaveImageProcessesAndResizes(t *testing.T) {
	cfg := testUploadConfig()
	cfg.MaxSize = 1 << 20
	service, store := newTestService(t, cfg)
	ctx := context.Background()

	data := makePNG(t, 200, 100)
	result, err := service.Save(ctx, "t", "images", "photo.png", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if result.Width != 50 || result.Height != 25 {
		t.Fatalf("unexpected dimensions: %+v", result)
	}
	if result.MIME != "image/png" || !strings.HasSuffix(result.Key, ".png") {
		t.Fatalf("unexpected result: %+v", result)
	}

	reader, err := store.Get(ctx, result.Key)
	if err != nil {
		t.Fatalf("get stored image: %v", err)
	}
	defer reader.Close()
	info, err := decodeInfo(reader)
	if err != nil {
		t.Fatalf("decode stored image: %v", err)
	}
	if info.Width != 50 || info.Height != 25 {
		t.Fatalf("stored image size = %dx%d", info.Width, info.Height)
	}
}

func TestScannerRequirementAndRejection(t *testing.T) {
	cfg := testUploadConfig()
	cfg.ScanEnabled = true

	if _, err := New(newMemoryStorage(t), cfg, logger.NewNop()); err == nil {
		t.Fatal("expected scan scanner requirement")
	}

	service, _ := newTestService(t, cfg, WithScanner(rejectScanner{}))
	if _, err := service.Save(context.Background(), "t", "docs", "a.txt", strings.NewReader("x")); err == nil {
		t.Fatal("expected scanner rejection")
	}
}

func TestScannerAccepts(t *testing.T) {
	cfg := testUploadConfig()
	cfg.ScanEnabled = true
	service, _ := newTestService(t, cfg, WithScanner(acceptScanner{}))

	if _, err := service.Save(context.Background(), "t", "docs", "a.txt", strings.NewReader("x")); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestScannerRejectsProcessedImage(t *testing.T) {
	cfg := testUploadConfig()
	cfg.MaxSize = 1 << 20
	cfg.ScanEnabled = true
	service, _ := newTestService(t, cfg, WithScanner(rejectScanner{}))

	if _, err := service.Save(context.Background(), "t", "images", "a.png", bytes.NewReader(makePNG(t, 20, 10))); err == nil {
		t.Fatal("expected image scanner rejection")
	}
}

type acceptScanner struct{}

func (acceptScanner) Scan(context.Context, []byte, string) error { return nil }

type rejectScanner struct{}

func (rejectScanner) Scan(context.Context, []byte, string) error {
	return io.ErrUnexpectedEOF
}

// newMemoryStorage 使用临时目录创建本地存储，供“扫描器必填”测试使用。
func newMemoryStorage(t *testing.T) storage.Storage {
	t.Helper()
	store, err := storage.NewLocal(storage.Config{Driver: "local", Path: t.TempDir()})
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	return store
}

func decodeInfo(r io.Reader) (struct{ Width, Height int }, error) {
	var info struct{ Width, Height int }
	data, err := io.ReadAll(r)
	if err != nil {
		return info, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return info, err
	}
	info.Width = cfg.Width
	info.Height = cfg.Height
	return info, nil
}
