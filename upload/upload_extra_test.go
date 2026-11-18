package upload

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/logger"
)

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// stubStore 是 storage.Storage 的替身，用于覆盖 Save 的错误分支。
type stubStore struct {
	putErr      error
	urlErr      error
	deleteErr   error
	url         string
	consume     bool
	deleteCalls int
}

func (s *stubStore) Put(_ context.Context, _ string, r io.Reader) error {
	if s.putErr != nil {
		return s.putErr
	}
	if s.consume {
		_, _ = io.Copy(io.Discard, r)
	}
	return nil
}

func (s *stubStore) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not found")
}

func (s *stubStore) Delete(context.Context, string) error {
	s.deleteCalls++
	return s.deleteErr
}

func (s *stubStore) Exists(context.Context, string) (bool, error) { return false, nil }

func (s *stubStore) GetURL(context.Context, string, time.Duration) (string, error) {
	return s.url, s.urlErr
}

func baseConfig() Config {
	return Config{
		MaxSize: 1 << 20,
	}
}

func TestNewConfigEdges(t *testing.T) {
	if _, err := New(nil, baseConfig(), logger.NewNop()); err == nil {
		t.Fatal("expected nil storage error")
	}
	if _, err := New(&stubStore{}, Config{}, logger.NewNop()); err == nil {
		t.Fatal("expected max size error")
	}

	cfg := baseConfig()
	cfg.ScanEnabled = true
	if _, err := New(&stubStore{}, cfg, logger.NewNop()); err == nil {
		t.Fatal("expected scanner requirement")
	}

	cfg = baseConfig()
	cfg.AllowedExtensions = []string{"TXT", "", " .csv "}
	cfg.AllowedMIMETypes = []string{" Text/Plain ", ""}
	service, err := New(&stubStore{consume: true, url: "http://cdn/a"}, cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := service.allowedExt[".txt"]; !ok {
		t.Fatalf("allowedExt = %+v", service.allowedExt)
	}
	if _, ok := service.allowedMIM["text/plain"]; !ok {
		t.Fatalf("allowedMIM = %+v", service.allowedMIM)
	}
	if _, err := service.Save(context.Background(), "t", "d", "a.txt", strings.NewReader("hello")); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestSaveContextAndNilReader(t *testing.T) {
	service, _ := newTestService(t, testUploadConfig())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Save(ctx, "t", "d", "a.txt", strings.NewReader("x")); err == nil {
		t.Fatal("expected canceled context error")
	}
	if _, err := service.Save(context.Background(), "t", "d", "a.txt", nil); err == nil {
		t.Fatal("expected nil reader error")
	}
}

func TestSaveHeaderErrors(t *testing.T) {
	service, _ := newTestService(t, testUploadConfig())
	if _, err := service.Save(context.Background(), "t", "d", "a.txt", failingReader{err: errors.New("boom")}); err == nil {
		t.Fatal("expected header read error")
	}
	if _, err := service.Save(context.Background(), "t", "d", "a.txt", strings.NewReader("")); err == nil {
		t.Fatal("expected empty file error")
	}
}

func TestSaveRejectsEmptyFilenameExtension(t *testing.T) {
	service, _ := newTestService(t, testUploadConfig())
	if _, err := service.Save(context.Background(), "t", "d", "noext", strings.NewReader("x")); err == nil {
		t.Fatal("expected extension rejection")
	}
}

func TestSaveAcceptsSupportedTypes(t *testing.T) {
	cfg := baseConfig()
	cfg.AllowedExtensions = []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".pdf", ".txt", ".csv", ".zip"}
	service, _ := newTestService(t, cfg)

	gifData := append([]byte("GIF89a"), bytes.Repeat([]byte{0}, 32)...)
	webpData := append([]byte("RIFF\x00\x00\x00\x00WEBP"), bytes.Repeat([]byte{0}, 32)...)
	zipData := append([]byte("PK\x03\x04"), bytes.Repeat([]byte{0}, 32)...)

	cases := []struct {
		name string
		data []byte
	}{
		{"a.jpg", append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0}, 32)...)},
		{"a.jpeg", append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0}, 32)...)},
		{"a.png", makePNG(t, 2, 2)},
		{"a.gif", gifData},
		{"a.webp", webpData},
		{"a.pdf", []byte("%PDF-1.4\nhello")},
		{"a.zip", zipData},
		{"a.txt", []byte("hello world")},
		{"a.csv", []byte("a,b\n1,2\n")},
	}
	for _, tc := range cases {
		if _, err := service.Save(context.Background(), "t", "d", tc.name, bytes.NewReader(tc.data)); err != nil {
			t.Fatalf("Save(%s): %v", tc.name, err)
		}
	}
}

func TestSaveRejectsTypeMismatch(t *testing.T) {
	cfg := baseConfig()
	cfg.AllowedExtensions = []string{".jpg", ".png", ".gif", ".webp", ".pdf", ".zip", ".txt"}
	service, _ := newTestService(t, cfg)

	cases := []string{"a.jpg", "a.png", "a.gif", "a.webp", "a.pdf", "a.zip"}
	for _, name := range cases {
		if _, err := service.Save(context.Background(), "t", "d", name, strings.NewReader("plain text content")); err == nil {
			t.Fatalf("Save(%s) expected mismatch error", name)
		}
	}
	if _, err := service.Save(context.Background(), "t", "d", "a.txt", bytes.NewReader(makePNG(t, 2, 2))); err == nil {
		t.Fatal("expected txt/png mismatch")
	}
}

func TestSaveMIMEWimelist(t *testing.T) {
	cfg := baseConfig()
	cfg.AllowedMIMETypes = []string{"application/json"}
	service, _ := newTestService(t, cfg)
	if _, err := service.Save(context.Background(), "t", "d", "a.txt", strings.NewReader("hello")); err == nil {
		t.Fatal("expected mime whitelist rejection")
	}
}

func TestSaveInvalidImage(t *testing.T) {
	cfg := testUploadConfig()
	cfg.MaxSize = 1 << 20
	service, _ := newTestService(t, cfg)
	// 具备 PNG 文件头但内容损坏，会通过 MIME 探测而在解码阶段失败。
	data := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0xAA}, 64)...)
	if _, err := service.Save(context.Background(), "t", "d", "a.png", bytes.NewReader(data)); err == nil {
		t.Fatal("expected image processing error")
	}
}

func TestSaveStoreErrors(t *testing.T) {
	cfg := testUploadConfig()
	cfg.MaxSize = 1 << 20

	ctx := context.Background()

	store := &stubStore{putErr: errors.New("disk full")}
	service, err := New(store, cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := service.Save(ctx, "t", "d", "a.txt", strings.NewReader("hello")); err == nil {
		t.Fatal("expected put error")
	}

	store = &stubStore{putErr: ErrTooLarge}
	service, _ = New(store, cfg, logger.NewNop())
	if _, err := service.Save(ctx, "t", "d", "a.txt", strings.NewReader("hello")); err == nil {
		t.Fatal("expected payload too large")
	} else if got := apperrors.From(err); got.Status != 413 {
		t.Fatalf("status = %d", got.Status)
	}

	store = &stubStore{consume: true, urlErr: errors.New("sign failed")}
	service, _ = New(store, cfg, logger.NewNop())
	result, err := service.Save(ctx, "t", "d", "a.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if result.URL != "" {
		t.Fatalf("url = %q", result.URL)
	}

	store = &stubStore{}
	service, _ = New(store, cfg, logger.NewNop())
	if _, err := service.Save(ctx, "t", "d", "a.txt", strings.NewReader("hello")); err == nil {
		t.Fatal("expected empty file error")
	}
	if store.deleteCalls != 1 {
		t.Fatalf("delete calls = %d", store.deleteCalls)
	}
}

func TestReadLimitedAndLimitedReader(t *testing.T) {
	if _, err := readLimited(failingReader{err: errors.New("boom")}, 10); err == nil {
		t.Fatal("expected read error")
	}
	if _, err := readLimited(strings.NewReader(strings.Repeat("a", 11)), 10); err == nil {
		t.Fatal("expected too large error")
	} else if got := apperrors.From(err); got.Status != 413 {
		t.Fatalf("status = %d", got.Status)
	}

	reader := &limitedReader{reader: strings.NewReader("abc"), remaining: 0}
	if _, err := reader.Read(make([]byte, 4)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}

	reader = &limitedReader{reader: strings.NewReader("abc"), remaining: 2}
	if _, err := reader.Read(make([]byte, 10)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}

	reader = &limitedReader{reader: strings.NewReader("abc"), remaining: 5}
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "abc" {
		t.Fatalf("data = %q, err = %v", data, err)
	}
}

func TestDisplayNameAndMIMEHelpers(t *testing.T) {
	cases := map[string]string{
		"":                    "file",
		"   ":                 "file",
		".":                   "file",
		"\x01\x02a.txt":       "a.txt",
		"dir/sub/file.txt":    "file.txt",
		"..\\..\\windows.txt": "windows.txt",
	}
	for input, want := range cases {
		if got := displayName(input); got != want {
			t.Fatalf("displayName(%q) = %q, want %q", input, got, want)
		}
	}
	if got := displayName(strings.Repeat("a", 300)); len(got) != maxNameLength {
		t.Fatalf("long name length = %d", len(got))
	}

	formats := map[string]string{
		"jpeg": "image/jpeg",
		"jpg":  "image/jpeg",
		"png":  "image/png",
		"gif":  "image/gif",
		"webp": "image/webp",
		"bmp":  "application/octet-stream",
	}
	for format, want := range formats {
		if got := mimeForFormat(format); got != want {
			t.Fatalf("mimeForFormat(%q) = %q", format, got)
		}
	}

	if got := normalizeMIME("text/plain; charset=utf-8"); got != "text/plain" {
		t.Fatalf("normalizeMIME = %q", got)
	}
	if got := normalizeMIME("text/plain; charset=\"unterminated"); got != "text/plain" {
		t.Fatalf("normalizeMIME fallback = %q", got)
	}
	if got := detectMIME("a.txt", []byte("hello")); got != "text/plain" {
		t.Fatalf("detectMIME = %q", got)
	}
}

func TestSafeSegmentAndProcessable(t *testing.T) {
	if value, err := safeSegment("   ", "default"); err != nil || value != "default" {
		t.Fatalf("safeSegment = %q, %v", value, err)
	}
	if value, err := safeSegment(" docs ", "default"); err != nil || value != "docs" {
		t.Fatalf("safeSegment = %q, %v", value, err)
	}
	if _, err := safeSegment("a/b", "default"); err == nil {
		t.Fatal("expected invalid segment error")
	}

	service := &Service{}
	if service.isProcessableImage(".png") {
		t.Fatal("no size limits should disable image processing")
	}
	service.imageMaxW = 10
	if service.isProcessableImage(".gif") {
		t.Fatal("gif must not be processable")
	}
	if !service.isProcessableImage(".jpg") {
		t.Fatal("jpg should be processable")
	}
}

func makeJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func makeGIF(t *testing.T) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 4, 4), []color.Color{color.Black, color.White})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

func TestSaveJPEGAndGIFVariants(t *testing.T) {
	cfg := baseConfig()
	cfg.ImageMaxWidth = 2
	service, store := newTestService(t, cfg)

	jpegResult, err := service.Save(context.Background(), "t", "d", "photo.jpg", bytes.NewReader(makeJPEG(t)))
	if err != nil {
		t.Fatalf("Save jpeg: %v", err)
	}
	if jpegResult.MIME != "image/jpeg" {
		t.Fatalf("mime = %q", jpegResult.MIME)
	}

	// GIF 不做重编码，走普通存储路径。
	gifResult, err := service.Save(context.Background(), "t", "d", "photo.gif", bytes.NewReader(makeGIF(t)))
	if err != nil {
		t.Fatalf("Save gif: %v", err)
	}
	if gifResult.MIME != "image/gif" {
		t.Fatalf("mime = %q", gifResult.MIME)
	}
	reader, err := store.Get(context.Background(), gifResult.Key)
	if err != nil {
		t.Fatalf("stored gif: %v", err)
	}
	_ = reader.Close()
}

func TestHandlerUploadOptionsAndLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := testUploadConfig()
	service, _ := newTestService(t, cfg)
	engine := gin.New()
	engine.POST("/upload", service.Handler(HTTPOptions{
		Tenant:   func(*gin.Context) string { return "tenant-custom" },
		Category: func(*gin.Context) string { return "cat-custom" },
	}))

	upload := func(filename string, content []byte) *httptest.ResponseRecorder {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, _ := writer.CreateFormFile("file", filename)
		_, _ = part.Write(content)
		_ = writer.Close()
		req := httptest.NewRequest(stdhttp.MethodPost, "/upload", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		return recorder
	}

	recorder := upload("a.txt", []byte("hello"))
	if recorder.Code != stdhttp.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "tenant-custom/cat-custom/") {
		t.Fatalf("body = %s", recorder.Body.String())
	}

	// 超过大小限制时在读取文件前拒绝。
	recorder = upload("big.txt", bytes.Repeat([]byte("a"), 2048))
	if recorder.Code != stdhttp.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestHandlerDefaultTenantAndUserFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _ := newTestService(t, testUploadConfig())

	// 无身份、无分类时使用 default/default。
	engine := gin.New()
	engine.POST("/upload", service.Handler(HTTPOptions{}))
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "a.txt")
	_, _ = part.Write([]byte("hello"))
	_ = writer.Close()
	req := httptest.NewRequest(stdhttp.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	if recorder.Code != stdhttp.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "default/default/") {
		t.Fatalf("body = %s", recorder.Body.String())
	}

	// 只有 UserID 时回退使用 UserID 作为租户。
	engine = gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(auth.WithIdentity(c.Request.Context(), &auth.Identity{UserID: "user-only"}))
		c.Next()
	})
	engine.POST("/upload", service.Handler(HTTPOptions{}))
	body = &bytes.Buffer{}
	writer = multipart.NewWriter(body)
	part, _ = writer.CreateFormFile("file", "b.txt")
	_, _ = part.Write([]byte("hello"))
	_ = writer.Close()
	req = httptest.NewRequest(stdhttp.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	if recorder.Code != stdhttp.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "user-only/default/") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

// 保证 PNG 编码器被引用，便于编译期发现 helper 变化。
var _ = png.Encode
