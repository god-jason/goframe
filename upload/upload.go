// Package upload 提供安全的文件上传服务。
//
// 职责：
//   - 校验文件大小、扩展名与 MIME/Magic Number；
//   - 使用服务端生成的随机 Key 存储文件，不信任客户端文件名；
//   - 对 JPEG/PNG/WebP 图片按配置等比缩放并重新编码，去除 EXIF 等元数据；
//   - 可选接入病毒/内容扫描器；
//   - 通过 Storage 接口落盘，返回访问 URL。
//
// 安全约束：
//   - 默认拒绝 SVG、HTML、JS 等主动内容类型；
//   - 文件名只用于展示，不参与存储 Key；
//   - 租户、分类必须通过安全字符校验，防止路径穿越；
//   - 扫描开启但未提供 Scanner 时，服务拒绝启动。
package upload

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/id"
	"github.com/god-jason/goframe/imagex"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/storage"
)

const (
	defaultMaxSize = 50 << 20
	headerPeekSize = 512
	maxNameLength  = 255
)

// ErrTooLarge 表示上传内容超过大小限制。
var ErrTooLarge = errors.New("upload: file exceeds size limit")

// Scanner 是可选的文件安全扫描接口。
// 上传服务会把文件读入内存后交给扫描器（受 MaxSize 限制）。
type Scanner interface {
	Scan(ctx context.Context, data []byte, mimeType string) error
}

// Result 是上传结果。
type Result struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	MIME   string `json:"mime"`
	URL    string `json:"url"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// Option 是上传服务选项。
type Option func(*Service)

// WithScanner 注入文件安全扫描器。
func WithScanner(scanner Scanner) Option {
	return func(s *Service) { s.scanner = scanner }
}

// Service 是文件上传服务。
type Service struct {
	store      storage.Storage
	log        logger.Logger
	maxSize    int64
	scan       bool
	scanner    Scanner
	imageMaxW  int
	imageMaxH  int
	maxPixels  int64
	imageQ     int
	allowedExt map[string]struct{}
	allowedMIM map[string]struct{}
}

var segmentPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// New 创建上传服务。
func New(store storage.Storage, cfg Config, log logger.Logger, opts ...Option) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("upload: storage is nil")
	}
	if log == nil {
		log = logger.NewNop()
	}
	if cfg.MaxSize <= 0 {
		return nil, fmt.Errorf("upload: max size must be greater than 0")
	}

	s := &Service{
		store:      store,
		log:        log,
		maxSize:    cfg.MaxSize.Bytes(),
		scan:       cfg.ScanEnabled,
		imageMaxW:  cfg.ImageMaxWidth,
		imageMaxH:  cfg.ImageMaxHeight,
		maxPixels:  cfg.ImageMaxPixels,
		imageQ:     cfg.ImageQuality,
		allowedExt: make(map[string]struct{}),
		allowedMIM: make(map[string]struct{}),
	}
	if s.maxSize <= 0 {
		s.maxSize = defaultMaxSize
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.scan && s.scanner == nil {
		return nil, fmt.Errorf("upload: scan_enabled requires a scanner")
	}

	extensions := cfg.AllowedExtensions
	if len(extensions) == 0 {
		extensions = []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".pdf", ".txt", ".csv", ".zip"}
	}
	for _, ext := range extensions {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		s.allowedExt[ext] = struct{}{}
	}
	for _, mimeType := range cfg.AllowedMIMETypes {
		mimeType = strings.ToLower(strings.TrimSpace(mimeType))
		if mimeType != "" {
			s.allowedMIM[mimeType] = struct{}{}
		}
	}
	return s, nil
}

// Save 保存文件。
// tenantID/category 用于隔离存储目录；filename 只用于展示，不参与存储 Key。
func (s *Service) Save(ctx context.Context, tenantID, category, filename string, r io.Reader) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, apperrors.BadRequest("file reader is nil")
	}

	tenant, err := safeSegment(tenantID, "default")
	if err != nil {
		return nil, apperrors.BadRequest("invalid tenant id")
	}
	category, err = safeSegment(category, "default")
	if err != nil {
		return nil, apperrors.BadRequest("invalid category")
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if _, ok := s.allowedExt[ext]; !ok {
		return nil, apperrors.Validation("file extension is not allowed")
	}

	buffered := bufio.NewReader(r)
	header, err := buffered.Peek(headerPeekSize)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, apperrors.BadRequest("read file header failed").WithCause(err)
	}
	if len(header) == 0 {
		return nil, apperrors.Validation("file is empty")
	}

	detected := detectMIME(ext, header)
	if err := s.validateType(ext, detected); err != nil {
		return nil, err
	}

	size := int64(0)
	width, height := 0, 0
	var reader io.Reader = buffered

	// 图片：按配置等比缩放并重新编码，同时丢弃 EXIF 等元数据。
	if s.isProcessableImage(ext) {
		data, readErr := readLimited(buffered, s.maxSize)
		if readErr != nil {
			return nil, readErr
		}
		processed, info, processErr := imagex.Process(bytes.NewReader(data), imagex.Options{
			MaxWidth:  s.imageMaxW,
			MaxHeight: s.imageMaxH,
			MaxPixels: s.maxPixels,
			MaxBytes:  s.maxSize,
			Quality:   s.imageQ,
		})
		if processErr != nil {
			return nil, apperrors.Validation("invalid or unsupported image").WithCause(processErr)
		}
		ext = "." + info.Format
		detected = mimeForFormat(info.Format)
		width, height = info.Width, info.Height
		reader = bytes.NewReader(processed)
		size = int64(len(processed))

		// 图片重编码后仍需经过安全扫描（扫描器看到的是最终落盘内容）。
		if s.scan && s.scanner != nil {
			if scanErr := s.scanner.Scan(ctx, processed, detected); scanErr != nil {
				s.log.Warn("image upload rejected by scanner",
					logger.String("name", displayName(filename)),
					logger.Error(scanErr),
				)
				return nil, apperrors.Validation("file failed security scan").WithCause(scanErr)
			}
		}
	} else if s.scan && s.scanner != nil {
		// 扫描器需要完整内容，读取后重新构造 Reader。
		data, readErr := readLimited(buffered, s.maxSize)
		if readErr != nil {
			return nil, readErr
		}
		if scanErr := s.scanner.Scan(ctx, data, detected); scanErr != nil {
			s.log.Warn("file upload rejected by scanner",
				logger.String("name", displayName(filename)),
				logger.Error(scanErr),
			)
			return nil, apperrors.Validation("file failed security scan").WithCause(scanErr)
		}
		reader = bytes.NewReader(data)
		size = int64(len(data))
	}

	key := fmt.Sprintf("%s/%s/%s/%s%s", tenant, category, time.Now().UTC().Format("2006/01"), id.New(), ext)
	stream := &limitedReader{reader: reader, remaining: s.maxSize}
	if err := s.store.Put(ctx, key, stream); err != nil {
		s.log.Error("file upload failed",
			logger.String("key", key),
			logger.Int64("size", size),
			logger.Error(err),
		)
		if errors.Is(err, ErrTooLarge) {
			return nil, apperrors.PayloadTooLarge()
		}
		return nil, apperrors.Internal("save file failed").WithCause(err)
	}
	if size <= 0 {
		size = stream.read
	}
	if size <= 0 {
		_ = s.store.Delete(ctx, key)
		return nil, apperrors.Validation("file is empty")
	}

	url, err := s.store.GetURL(ctx, key, 0)
	if err != nil {
		s.log.Warn("file uploaded but get url failed",
			logger.String("key", key),
			logger.Error(err),
		)
		url = ""
	}

	result := &Result{
		Key:    key,
		Name:   displayName(filename),
		Size:   size,
		MIME:   detected,
		URL:    url,
		Width:  width,
		Height: height,
	}
	s.log.Info("file uploaded",
		logger.String("key", key),
		logger.Int64("size", size),
		logger.String("mime", detected),
	)
	return result, nil
}

// validateType 校验扩展名与探测到的 MIME 是否匹配，并应用 MIME 白名单。
func (s *Service) validateType(ext, detected string) error {
	if len(s.allowedMIM) > 0 {
		if _, ok := s.allowedMIM[strings.ToLower(detected)]; !ok {
			return apperrors.Validation("file mime type is not allowed")
		}
	}

	switch ext {
	case ".jpg", ".jpeg":
		if detected != "image/jpeg" {
			return apperrors.Validation("file content does not match extension")
		}
	case ".png":
		if detected != "image/png" {
			return apperrors.Validation("file content does not match extension")
		}
	case ".gif":
		if detected != "image/gif" {
			return apperrors.Validation("file content does not match extension")
		}
	case ".webp":
		if detected != "image/webp" {
			return apperrors.Validation("file content does not match extension")
		}
	case ".pdf":
		if detected != "application/pdf" {
			return apperrors.Validation("file content does not match extension")
		}
	case ".zip":
		if detected != "application/zip" && detected != "application/x-zip-compressed" {
			return apperrors.Validation("file content does not match extension")
		}
	case ".txt", ".csv":
		if !strings.HasPrefix(detected, "text/") {
			return apperrors.Validation("file content does not match extension")
		}
	}
	return nil
}

func (s *Service) isProcessableImage(ext string) bool {
	if s.imageMaxW <= 0 && s.imageMaxH <= 0 {
		return false
	}
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	default:
		return false
	}
}

// limitedReader 在读取超过上限时返回 ErrTooLarge，让存储层中止写入。
type limitedReader struct {
	reader    io.Reader
	remaining int64
	read      int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.remaining <= 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > l.remaining+1 {
		p = p[:l.remaining+1]
	}
	n, err := l.reader.Read(p)
	l.read += int64(n)
	l.remaining -= int64(n)
	if l.remaining < 0 {
		return n, ErrTooLarge
	}
	return n, err
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, apperrors.BadRequest("read file failed").WithCause(err)
	}
	if int64(len(data)) > max {
		return nil, apperrors.PayloadTooLarge()
	}
	return data, nil
}

func safeSegment(value, fallback string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	if !segmentPattern.MatchString(value) {
		return "", fmt.Errorf("invalid segment %q", value)
	}
	return value, nil
}

func displayName(filename string) string {
	name := filepath.Base(strings.ReplaceAll(filename, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		name = "file"
	}
	if len(name) > maxNameLength {
		name = name[:maxNameLength]
	}
	return name
}

func mimeForFormat(format string) string {
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// normalizeMIME 去掉 "text/plain; charset=utf-8" 中的参数，便于白名单比较。
func normalizeMIME(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	}
	return strings.ToLower(strings.TrimSpace(mediaType))
}

// detectMIME 在标准库嗅探基础上补充 WebP 识别。
// 部分 Go 版本对 RIFF....WEBP 的嗅探结果依赖文件长度，这里显式兜底。
func detectMIME(ext string, header []byte) string {
	detected := normalizeMIME(http.DetectContentType(header))
	if ext == ".webp" && len(header) >= 12 &&
		bytes.Equal(header[0:4], []byte("RIFF")) &&
		bytes.Equal(header[8:12], []byte("WEBP")) {
		return "image/webp"
	}
	return detected
}
