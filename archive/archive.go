// Package archive 提供压缩包的创建、解压与列表能力。
//
// 支持格式：
//   - zip
//   - tar
//   - tar.gz / tgz
//   - gz（单文件压缩）
//
// 解压默认按“安全优先”处理：
//   - 拒绝绝对路径、盘符路径与 ../ 路径穿越（Zip Slip）；
//   - 拒绝符号链接、硬链接、设备文件与 FIFO；
//   - 限制单文件大小、总大小与文件数量；
//   - 默认不覆盖已存在的文件。
//
// 典型用法：
//
//	// 解压
//	err := archive.Extract("backup.zip", "data/backup")
//
//	// 带选项解压
//	err := archive.Extract("backup.tar.gz", "data/backup",
//	    archive.WithOverwrite(),
//	    archive.WithMaxFileSize(10<<20),
//	)
//
//	// 创建压缩包，支持文件与目录，目录会递归打包
//	err := archive.Compress("backup.zip", "data", "configs/config.yaml")
//
//	// 列出条目
//	entries, err := archive.List("backup.zip")
package archive

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Format 是压缩包格式。
type Format string

const (
	// Zip 是 zip 格式。
	Zip Format = "zip"
	// Tar 是未压缩的 tar 格式。
	Tar Format = "tar"
	// TarGz 是 gzip 压缩的 tar 格式。
	TarGz Format = "tar.gz"
	// Gz 是单文件 gzip 格式。
	Gz Format = "gz"
)

var (
	// ErrUnsupportedFormat 表示无法识别压缩包格式。
	ErrUnsupportedFormat = errors.New("archive: unsupported format")
	// ErrUnsafeEntry 表示压缩包条目存在安全风险（路径穿越、链接、设备文件等）。
	ErrUnsafeEntry = errors.New("archive: unsafe entry")
	// ErrLimitExceeded 表示超过解压限制。
	ErrLimitExceeded = errors.New("archive: limit exceeded")
	// ErrAlreadyExists 表示目标文件已存在且未开启覆盖。
	ErrAlreadyExists = errors.New("archive: target already exists")
)

// 默认解压限制，避免压缩炸弹耗尽磁盘。
const (
	defaultMaxFileSize  = 100 << 20 // 单文件 100MB
	defaultMaxTotalSize = 1 << 30   // 总大小 1GB
	defaultMaxFiles     = 10000
	// defaultMaxZipInput 是流式读取 zip 时的默认内存上限。
	defaultMaxZipInput = 512 << 20 // 512MB
)

// Entry 是压缩包中的一个条目。
type Entry struct {
	// Name 是条目在压缩包内的路径，统一使用 / 分隔。
	Name string
	// Size 是解压后的大小；未知时为 0。
	Size int64
	// Mode 是文件权限与类型。
	Mode fs.FileMode
	// ModTime 是修改时间。
	ModTime time.Time
	// IsDir 表示目录条目。
	IsDir bool
}

// IsSymlink 判断条目是否为符号链接。
func (e Entry) IsSymlink() bool { return e.Mode&os.ModeSymlink != 0 }

// extractOptions 是解压选项。
type extractOptions struct {
	maxFileSize  int64
	maxTotalSize int64
	maxFiles     int
	maxZipInput  int64
	overwrite    bool
	preserveMode bool
	preserveTime bool
	allowSymlink bool
	onEntry      func(Entry) error
}

// Option 是解压选项函数。
type Option func(*extractOptions)

// WithMaxFileSize 设置单个文件大小上限，n <= 0 表示不限制。
func WithMaxFileSize(n int64) Option {
	return func(o *extractOptions) { o.maxFileSize = n }
}

// WithMaxTotalSize 设置解压总大小上限，n <= 0 表示不限制。
func WithMaxTotalSize(n int64) Option {
	return func(o *extractOptions) { o.maxTotalSize = n }
}

// WithMaxFiles 设置最大文件数量，n <= 0 表示不限制。
func WithMaxFiles(n int) Option {
	return func(o *extractOptions) { o.maxFiles = n }
}

// WithMaxArchiveSize 设置从 reader 读取 zip 时的最大输入字节数，<=0 表示不限制。
// zip 需要随机访问，无法像 tar 一样流式解压，因此必须限制内存占用。
func WithMaxArchiveSize(n int64) Option {
	return func(o *extractOptions) { o.maxZipInput = n }
}

// WithOverwrite 允许覆盖已存在的文件。
func WithOverwrite() Option {
	return func(o *extractOptions) { o.overwrite = true }
}

// WithPreserveMode 保留压缩包内的文件权限。
func WithPreserveMode() Option {
	return func(o *extractOptions) { o.preserveMode = true }
}

// WithPreserveTime 保留压缩包内的修改时间。
func WithPreserveTime() Option {
	return func(o *extractOptions) { o.preserveTime = true }
}

// WithAllowSymlinks 允许解压符号链接。
// 符号链接目标仍必须位于解压目录内，否则返回 ErrUnsafeEntry。
func WithAllowSymlinks() Option {
	return func(o *extractOptions) { o.allowSymlink = true }
}

// WithEntryHook 在写入每个条目之前回调，返回错误可中止解压。
func WithEntryHook(fn func(Entry) error) Option {
	return func(o *extractOptions) { o.onEntry = fn }
}

func newExtractOptions(opts []Option) extractOptions {
	o := extractOptions{
		maxFileSize:  defaultMaxFileSize,
		maxTotalSize: defaultMaxTotalSize,
		maxFiles:     defaultMaxFiles,
		maxZipInput:  defaultMaxZipInput,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// Extract 解压压缩包到 dest 目录，支持 zip / tar / tar.gz / gz。
// 文件格式优先按扩展名判断，无法判断时读取文件头识别。
func Extract(src, dest string, opts ...Option) error {
	format, err := DetectFormat(src)
	if err != nil {
		return err
	}

	file, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("archive: open %s: %w", src, err)
	}
	defer file.Close()

	if format == Zip {
		info, err := file.Stat()
		if err != nil {
			return fmt.Errorf("archive: stat %s: %w", src, err)
		}
		return extractZip(file, info.Size(), dest, newExtractOptions(opts))
	}
	return extractStream(file, format, filepath.Base(src), dest, newExtractOptions(opts))
}

// ExtractTo 从 reader 解压到 dest 目录。
// format 必填；zip 需要完整读取到内存，适合已限制大小的上传文件。
func ExtractTo(r io.Reader, format Format, dest string, opts ...Option) error {
	if r == nil {
		return errors.New("archive: reader is nil")
	}
	options := newExtractOptions(opts)
	if format == Zip {
		data, err := readAllLimited(r, options.maxZipInput)
		if err != nil {
			return err
		}
		return extractZip(bytes.NewReader(data), int64(len(data)), dest, options)
	}
	return extractStream(r, format, "", dest, options)
}

// List 列出压缩包内的条目，不落盘。
func List(src string) ([]Entry, error) {
	format, err := DetectFormat(src)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(src)
	if err != nil {
		return nil, fmt.Errorf("archive: open %s: %w", src, err)
	}
	defer file.Close()

	if format == Zip {
		info, err := file.Stat()
		if err != nil {
			return nil, fmt.Errorf("archive: stat %s: %w", src, err)
		}
		return listZip(file, info.Size())
	}
	return listStream(file, format, filepath.Base(src))
}

// ListTo 从 reader 列出压缩包条目。format 必填。
func ListTo(r io.Reader, format Format) ([]Entry, error) {
	if r == nil {
		return nil, errors.New("archive: reader is nil")
	}
	if format == Zip {
		data, err := readAllLimited(r, defaultMaxZipInput)
		if err != nil {
			return nil, err
		}
		return listZip(bytes.NewReader(data), int64(len(data)))
	}
	return listStream(r, format, "")
}

// readAllLimited 读取全部内容并限制最大字节数，limit<=0 表示不限制。
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("archive: read input: %w", err)
		}
		return data, nil
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("archive: read input: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: input exceeds %d bytes", ErrLimitExceeded, limit)
	}
	return data, nil
}

// DetectFormat 识别压缩包格式。
// 先按扩展名判断；扩展名缺失或未知时读取文件头，支持不带后缀的压缩包。
func DetectFormat(src string) (Format, error) {
	if format := formatFromName(src); format != "" {
		return format, nil
	}

	file, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("archive: open %s: %w", src, err)
	}
	defer file.Close()

	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("archive: read %s: %w", src, err)
	}
	return detectMagic(head[:n])
}

// formatFromName 根据文件名后缀推断格式。
func formatFromName(name string) Format {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return TarGz
	case strings.HasSuffix(lower, ".zip"):
		return Zip
	case strings.HasSuffix(lower, ".tar"):
		return Tar
	case strings.HasSuffix(lower, ".gz"):
		return Gz
	default:
		return ""
	}
}

// detectMagic 根据文件头识别格式。
func detectMagic(head []byte) (Format, error) {
	if len(head) >= 4 && head[0] == 'P' && head[1] == 'K' {
		return Zip, nil
	}
	if len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b {
		// gzip 内部可能是 tar，解压时会再次嗅探。
		return Gz, nil
	}
	if isTarHeader(head) {
		return Tar, nil
	}
	return "", ErrUnsupportedFormat
}

// isTarHeader 判断是否为 tar 头（ustar 魔数位于偏移 257）。
func isTarHeader(head []byte) bool {
	return len(head) >= 262 && string(head[257:262]) == "ustar"
}

// extractStream 处理流式格式：tar / tar.gz / gz。
func extractStream(r io.Reader, format Format, sourceName, dest string, opts extractOptions) error {
	ex, err := newExtractor(dest, opts)
	if err != nil {
		return err
	}

	switch format {
	case Tar:
		return ex.tar(r)
	case TarGz:
		gz, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("archive: open gzip: %w", err)
		}
		defer gz.Close()
		return ex.tar(gz)
	case Gz:
		gz, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("archive: open gzip: %w", err)
		}
		defer gz.Close()

		// .gz 可能只是 tar.gz 改了后缀，先嗅探内部是否为 tar。
		buffered := bufio.NewReader(gz)
		head, _ := buffered.Peek(512)
		if isTarHeader(head) {
			return ex.tar(buffered)
		}
		return ex.file(Entry{
			Name: gzipEntryName(sourceName),
			Mode: 0o644,
		}, buffered)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFormat, format)
	}
}

// listStream 列出流式格式条目。
func listStream(r io.Reader, format Format, sourceName string) ([]Entry, error) {
	switch format {
	case Tar:
		return listTar(r)
	case TarGz:
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("archive: open gzip: %w", err)
		}
		defer gz.Close()
		return listTar(gz)
	case Gz:
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("archive: open gzip: %w", err)
		}
		defer gz.Close()

		buffered := bufio.NewReader(gz)
		head, _ := buffered.Peek(512)
		if isTarHeader(head) {
			return listTar(buffered)
		}
		return []Entry{{
			Name: gzipEntryName(sourceName),
			Mode: 0o644,
		}}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, format)
	}
}

// gzipEntryName 从 .gz 文件名推导解压后的文件名。
func gzipEntryName(sourceName string) string {
	name := filepath.Base(sourceName)
	if strings.HasSuffix(strings.ToLower(name), ".gz") {
		name = name[:len(name)-3]
	}
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "data"
	}
	return name
}

// Compress 创建压缩包，格式由目标文件扩展名推断。
// 参数可以是文件或目录；目录会递归打包，归档内路径保留目录名前缀。
func Compress(dest string, sources ...string) error {
	format := formatFromName(dest)
	if format == "" {
		return fmt.Errorf("%w: cannot infer format from %q", ErrUnsupportedFormat, dest)
	}
	return CompressAs(dest, format, sources...)
}

// CompressAs 按指定格式创建压缩包。
func CompressAs(dest string, format Format, sources ...string) error {
	entries, err := collectSources(sources)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(dest); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("archive: create destination directory: %w", err)
		}
	}

	// 先写同目录临时文件，成功后再原子替换，避免失败时破坏已有归档。
	dir := filepath.Dir(dest)
	temp, err := os.CreateTemp(dir, ".archive-*")
	if err != nil {
		return fmt.Errorf("archive: create temp file: %w", err)
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()

	if err := writeArchive(temp, format, entries); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("archive: close temp file: %w", err)
	}
	if err := os.Rename(tempName, dest); err != nil {
		return fmt.Errorf("archive: commit %s: %w", dest, err)
	}
	return nil
}

// CompressTo 将文件或目录写入已经打开的 writer。
func CompressTo(w io.Writer, format Format, sources ...string) error {
	if w == nil {
		return errors.New("archive: writer is nil")
	}
	entries, err := collectSources(sources)
	if err != nil {
		return err
	}
	return writeArchive(w, format, entries)
}

// writeArchive 按格式分发写入逻辑。
func writeArchive(w io.Writer, format Format, entries []sourceEntry) error {
	switch format {
	case Zip:
		return writeZip(w, entries)
	case Tar:
		return writeTar(w, entries, false)
	case TarGz:
		return writeTar(w, entries, true)
	case Gz:
		if len(entries) != 1 || entries[0].info.IsDir() {
			return errors.New("archive: gz format supports exactly one regular file")
		}
		return writeGzipFile(w, entries[0].path)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFormat, format)
	}
}

// sourceEntry 是待打包的源文件。
type sourceEntry struct {
	// name 是归档内路径，使用 / 分隔。
	name string
	// path 是本地文件路径。
	path string
	// info 是文件信息。
	info fs.FileInfo
}

// collectSources 收集文件与目录，生成确定性的归档条目列表。
func collectSources(sources []string) ([]sourceEntry, error) {
	if len(sources) == 0 {
		return nil, errors.New("archive: no source files")
	}

	var entries []sourceEntry
	names := make(map[string]struct{})
	add := func(item sourceEntry) error {
		if _, exists := names[item.name]; exists {
			return fmt.Errorf("archive: duplicate entry name %q", item.name)
		}
		names[item.name] = struct{}{}
		entries = append(entries, item)
		return nil
	}

	for _, src := range sources {
		src = filepath.Clean(src)
		info, err := os.Lstat(src)
		if err != nil {
			return nil, fmt.Errorf("archive: stat %s: %w", src, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: source is a symlink: %s", ErrUnsafeEntry, src)
		}

		if !info.IsDir() {
			if err := add(sourceEntry{name: filepath.Base(src), path: src, info: info}); err != nil {
				return nil, err
			}
			continue
		}

		base := filepath.Base(src)
		if base == "." || base == string(filepath.Separator) {
			return nil, fmt.Errorf("archive: invalid source directory %q", src)
		}
		err = filepath.WalkDir(src, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: source contains a symlink: %s", ErrUnsafeEntry, current)
			}
			rel, err := filepath.Rel(src, current)
			if err != nil {
				return err
			}
			name := base
			if rel != "." {
				name = path.Join(base, filepath.ToSlash(rel))
			}
			fileInfo, err := entry.Info()
			if err != nil {
				return err
			}
			return add(sourceEntry{name: name, path: current, info: fileInfo})
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	return entries, nil
}

// extractor 负责安全落盘与限制检查。
type extractor struct {
	dest  string
	opts  extractOptions
	files int
	total int64
}

// newExtractor 创建解压器并确保目标目录存在。
func newExtractor(dest string, opts extractOptions) (*extractor, error) {
	if strings.TrimSpace(dest) == "" {
		return nil, errors.New("archive: destination must not be empty")
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, fmt.Errorf("archive: create destination %s: %w", dest, err)
	}
	return &extractor{dest: dest, opts: opts}, nil
}

// checkEntry 校验条目数量与单文件大小，并触发回调。
func (e *extractor) checkEntry(entry Entry) error {
	if !entry.IsDir {
		e.files++
		if e.opts.maxFiles > 0 && e.files > e.opts.maxFiles {
			return fmt.Errorf("%w: too many files (max %d)", ErrLimitExceeded, e.opts.maxFiles)
		}
		if e.opts.maxFileSize > 0 && entry.Size > e.opts.maxFileSize {
			return fmt.Errorf("%w: %s exceeds max file size %d", ErrLimitExceeded, entry.Name, e.opts.maxFileSize)
		}
	}
	if e.opts.onEntry != nil {
		if err := e.opts.onEntry(entry); err != nil {
			return fmt.Errorf("archive: entry hook: %w", err)
		}
	}
	return nil
}

// dir 创建目录。
func (e *extractor) dir(entry Entry) error {
	if err := e.checkEntry(entry); err != nil {
		return err
	}
	target, err := safePath(e.dest, entry.Name)
	if err != nil {
		return err
	}

	mode := fs.FileMode(0o755)
	if e.opts.preserveMode && entry.Mode.Perm() != 0 {
		mode = entry.Mode.Perm()
	}
	if err := os.MkdirAll(target, mode|0o700); err != nil {
		return fmt.Errorf("archive: create directory %s: %w", target, err)
	}
	if e.opts.preserveTime && !entry.ModTime.IsZero() {
		_ = os.Chtimes(target, entry.ModTime, entry.ModTime)
	}
	return nil
}

// file 写入普通文件，并在拷贝过程中再次校验大小，防止头部大小伪造。
func (e *extractor) file(entry Entry, r io.Reader) error {
	if err := e.checkEntry(entry); err != nil {
		return err
	}
	target, err := safePath(e.dest, entry.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("archive: create parent directory: %w", err)
	}

	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if !e.opts.overwrite {
		flags |= os.O_EXCL
	}
	mode := fs.FileMode(0o644)
	if e.opts.preserveMode && entry.Mode.Perm() != 0 {
		mode = entry.Mode.Perm()
	}
	file, err := os.OpenFile(target, flags, mode)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, target)
		}
		return fmt.Errorf("archive: create file %s: %w", target, err)
	}

	written, copyErr := e.copyLimited(file, r)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(target)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(target)
		return fmt.Errorf("archive: close %s: %w", target, closeErr)
	}
	e.total += written

	if e.opts.preserveTime && !entry.ModTime.IsZero() {
		_ = os.Chtimes(target, entry.ModTime, entry.ModTime)
	}
	return nil
}

// copyLimited 拷贝文件内容并检查单文件与总大小限制。
func (e *extractor) copyLimited(w io.Writer, r io.Reader) (int64, error) {
	var source io.Reader = r
	if e.opts.maxFileSize > 0 {
		source = io.LimitReader(r, e.opts.maxFileSize+1)
	}

	written, err := io.Copy(w, source)
	if err != nil {
		return written, fmt.Errorf("archive: write file: %w", err)
	}
	if e.opts.maxFileSize > 0 && written > e.opts.maxFileSize {
		return written, fmt.Errorf("%w: file exceeds max file size %d", ErrLimitExceeded, e.opts.maxFileSize)
	}
	if e.opts.maxTotalSize > 0 && e.total+written > e.opts.maxTotalSize {
		return written, fmt.Errorf("%w: total size exceeds %d", ErrLimitExceeded, e.opts.maxTotalSize)
	}
	return written, nil
}

// symlink 创建符号链接，并确保链接目标不会逃逸解压目录。
func (e *extractor) symlink(entry Entry, linkTarget string) error {
	if !e.opts.allowSymlink {
		return fmt.Errorf("%w: symlink %q", ErrUnsafeEntry, entry.Name)
	}
	if err := validateLinkTarget(entry.Name, linkTarget); err != nil {
		return err
	}
	target, err := safePath(e.dest, entry.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("archive: create parent directory: %w", err)
	}
	if !e.opts.overwrite {
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, target)
		}
	} else {
		_ = os.Remove(target)
	}
	if err := os.Symlink(linkTarget, target); err != nil {
		return fmt.Errorf("archive: create symlink %s: %w", target, err)
	}
	return nil
}

// validateLinkTarget 校验符号链接目标必须位于解压目录内。
func validateLinkTarget(entryName, linkTarget string) error {
	if linkTarget == "" {
		return fmt.Errorf("%w: empty symlink target for %q", ErrUnsafeEntry, entryName)
	}
	normalized := strings.ReplaceAll(linkTarget, "\\", "/")
	if strings.HasPrefix(normalized, "/") || filepath.IsAbs(linkTarget) || filepath.VolumeName(linkTarget) != "" {
		return fmt.Errorf("%w: absolute symlink target %q", ErrUnsafeEntry, linkTarget)
	}
	resolved := path.Clean(path.Join(path.Dir(entryName), normalized))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("%w: symlink target escapes destination: %q", ErrUnsafeEntry, linkTarget)
	}
	return nil
}

// safePath 把归档内路径安全地映射到目标目录。
// 任何绝对路径、盘符路径或 ../ 逃逸都会返回 ErrUnsafeEntry。
func safePath(dest, name string) (string, error) {
	normalized := strings.ReplaceAll(name, "\\", "/")
	if normalized == "" {
		return "", fmt.Errorf("%w: empty entry name", ErrUnsafeEntry)
	}
	if strings.HasPrefix(normalized, "/") || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("%w: absolute path %q", ErrUnsafeEntry, name)
	}

	clean := path.Clean(normalized)
	if clean == "." {
		return dest, nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: path escapes destination: %q", ErrUnsafeEntry, name)
	}

	target := filepath.Join(dest, filepath.FromSlash(clean))
	rel, err := filepath.Rel(dest, target)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrUnsafeEntry, name)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: path escapes destination: %q", ErrUnsafeEntry, name)
	}
	return target, nil
}
