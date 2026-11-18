package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func tarBytes(t *testing.T, fill func(*tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	fill(writer)
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buf.Bytes()
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

type zipSpec struct {
	name    string
	body    string
	mode    fs.FileMode
	modTime time.Time
	method  uint16
}

func writeZipSpec(t *testing.T, path string, specs []zipSpec) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	writer := zip.NewWriter(file)
	for _, spec := range specs {
		header := &zip.FileHeader{Name: spec.name, Method: spec.method}
		if spec.method == 0 {
			header.Method = zip.Deflate
		}
		if spec.mode != 0 {
			header.SetMode(spec.mode)
		}
		if !spec.modTime.IsZero() {
			header.Modified = spec.modTime
		}
		entry, err := writer.CreateHeader(header)
		if err != nil {
			_ = file.Close()
			t.Fatalf("create header %s: %v", spec.name, err)
		}
		if _, err := entry.Write([]byte(spec.body)); err != nil {
			_ = file.Close()
			t.Fatalf("write %s: %v", spec.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
}

func TestTarAndTarGzRoundTrip(t *testing.T) {
	root, src := prepareSource(t)
	for _, tc := range []struct {
		name   string
		format Format
	}{
		{"data.tar", Tar},
		{"data.tar.gz", TarGz},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archivePath := filepath.Join(root, tc.name)
			if err := Compress(archivePath, src); err != nil {
				t.Fatalf("Compress: %v", err)
			}
			entries, err := List(archivePath)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if !hasEntry(entries, "src/a.txt") {
				t.Fatalf("entries = %v", entryNames(entries))
			}
			data, err := os.ReadFile(archivePath)
			if err != nil {
				t.Fatalf("read archive: %v", err)
			}
			listTo, err := ListTo(bytes.NewReader(data), tc.format)
			if err != nil || len(listTo) == 0 {
				t.Fatalf("ListTo: %v %v", err, listTo)
			}

			dest := filepath.Join(t.TempDir(), "out")
			if err := Extract(archivePath, dest); err != nil {
				t.Fatalf("Extract: %v", err)
			}
			assertFileContent(t, filepath.Join(dest, "src", "a.txt"), "alpha")

			dest2 := filepath.Join(t.TempDir(), "out2")
			if err := ExtractTo(bytes.NewReader(data), tc.format, dest2); err != nil {
				t.Fatalf("ExtractTo: %v", err)
			}
			assertFileContent(t, filepath.Join(dest2, "src", "nested", "b.txt"), "beta")
		})
	}
}

func TestGzipVariants(t *testing.T) {
	root := t.TempDir()

	// 普通单文件 gzip。
	plain := gzipBytes(t, []byte("hello gzip"))
	plainPath := filepath.Join(root, "hello.txt.gz")
	writeFile(t, plainPath, plain)

	entries, err := List(plainPath)
	if err != nil {
		t.Fatalf("List gz: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "hello.txt" {
		t.Fatalf("entries = %+v", entries)
	}
	dest := filepath.Join(root, "out-plain")
	if err := Extract(plainPath, dest); err != nil {
		t.Fatalf("Extract gz: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "hello.txt"), "hello gzip")

	// gzip 内部是 tar，但扩展名仅为 .gz。
	tarData := tarBytes(t, func(writer *tar.Writer) {
		body := []byte("inside")
		if err := writer.WriteHeader(&tar.Header{Name: "inner.txt", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	})
	innerPath := filepath.Join(root, "inner.gz")
	writeFile(t, innerPath, gzipBytes(t, tarData))

	entries, err = List(innerPath)
	if err != nil {
		t.Fatalf("List inner gz: %v", err)
	}
	if !hasEntry(entries, "inner.txt") {
		t.Fatalf("entries = %v", entryNames(entries))
	}
	dest = filepath.Join(root, "out-inner")
	if err := Extract(innerPath, dest); err != nil {
		t.Fatalf("Extract inner gz: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "inner.txt"), "inside")

	// ExtractTo / ListTo 无文件名来源。
	dest = filepath.Join(root, "out-to")
	if err := ExtractTo(bytes.NewReader(plain), Gz, dest); err != nil {
		t.Fatalf("ExtractTo gz: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "data"), "hello gzip")
	listTo, err := ListTo(bytes.NewReader(plain), Gz)
	if err != nil || len(listTo) != 1 || listTo[0].Name != "data" {
		t.Fatalf("ListTo gz: %v %+v", err, listTo)
	}

	// ExtractTo gzip 内 tar。
	dest = filepath.Join(root, "out-inner-to")
	if err := ExtractTo(bytes.NewReader(gzipBytes(t, tarData)), Gz, dest); err != nil {
		t.Fatalf("ExtractTo inner gz: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "inner.txt"), "inside")
}

func TestGzipEntryName(t *testing.T) {
	cases := map[string]string{
		"a.txt.gz":   "a.txt",
		"UPPER.GZ":   "UPPER",
		"plain.txt":  "plain.txt",
		".gz":        "data",
		"":           "data",
		"dir/x.gz":   "x",
		"archive.gz": "archive",
	}
	for input, want := range cases {
		if got := gzipEntryName(input); got != want {
			t.Fatalf("gzipEntryName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDetectFormatVariants(t *testing.T) {
	root, src := prepareSource(t)
	file := filepath.Join(src, "a.txt")

	tarPath := filepath.Join(root, "payload.bin")
	if err := CompressAs(tarPath, Tar, file); err != nil {
		t.Fatalf("CompressAs tar: %v", err)
	}
	if format, err := DetectFormat(tarPath); err != nil || format != Tar {
		t.Fatalf("DetectFormat tar = %q, %v", format, err)
	}

	gzPath := filepath.Join(root, "payload2.bin")
	if err := CompressAs(gzPath, Gz, file); err != nil {
		t.Fatalf("CompressAs gz: %v", err)
	}
	if format, err := DetectFormat(gzPath); err != nil || format != Gz {
		t.Fatalf("DetectFormat gz = %q, %v", format, err)
	}

	textPath := filepath.Join(root, "plain.bin")
	writeFile(t, textPath, []byte("hello world"))
	if _, err := DetectFormat(textPath); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("DetectFormat text = %v", err)
	}

	emptyPath := filepath.Join(root, "empty.bin")
	writeFile(t, emptyPath, nil)
	if _, err := DetectFormat(emptyPath); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("DetectFormat empty = %v", err)
	}

	if _, err := DetectFormat(filepath.Join(root, "missing.bin")); err == nil {
		t.Fatal("expected missing file error")
	}

	for ext, want := range map[string]Format{
		"a.tgz":    TarGz,
		"a.TAR.GZ": TarGz,
		"a.zip":    Zip,
		"a.tar":    Tar,
		"a.gz":     Gz,
		"a.rar":    "",
	} {
		if got := formatFromName(ext); got != want {
			t.Fatalf("formatFromName(%q) = %q, want %q", ext, got, want)
		}
	}

	if err := Compress(filepath.Join(root, "out.rar"), file); err == nil {
		t.Fatal("expected unsupported destination error")
	}
}

func TestCorruptArchives(t *testing.T) {
	root := t.TempDir()

	cases := []struct {
		name   string
		body   []byte
		format string
	}{
		{"bad.zip", []byte("not a zip at all"), ".zip"},
		{"bad.tar", []byte("not a tar at all"), ".tar"},
		{"bad.tar.gz", []byte("not gzip"), ".tar.gz"},
		{"bad.gz", []byte("not gzip"), ".gz"},
	}
	for _, tc := range cases {
		path := filepath.Join(root, tc.name)
		writeFile(t, path, tc.body)
		if _, err := List(path); err == nil {
			t.Fatalf("%s: expected List error", tc.name)
		}
		if err := Extract(path, filepath.Join(root, "out-"+strings.TrimPrefix(tc.name, "bad."))); err == nil {
			t.Fatalf("%s: expected Extract error", tc.name)
		}
	}

	// zip 数据被截断。
	zipPath := filepath.Join(root, "truncated.zip")
	writeRawZip(t, zipPath, map[string]string{"a.txt": "alpha"})
	data, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	writeFile(t, zipPath, data[:len(data)-10])
	if _, err := List(zipPath); err == nil {
		t.Fatal("expected truncated zip list error")
	}
	if err := Extract(zipPath, filepath.Join(root, "trunc")); err == nil {
		t.Fatal("expected truncated zip extract error")
	}
}

func TestSafePathCases(t *testing.T) {
	dest := t.TempDir()
	bad := []string{"", "/abs", "C:\\abs", "\\abs", "..", "../a", "a/../../b", "//server/share"}
	for _, name := range bad {
		if _, err := safePath(dest, name); !errors.Is(err, ErrUnsafeEntry) {
			t.Fatalf("safePath(%q) = %v, want ErrUnsafeEntry", name, err)
		}
	}
	if got, err := safePath(dest, "."); err != nil || got != dest {
		t.Fatalf("safePath(.) = %q, %v", got, err)
	}
	if got, err := safePath(dest, "a/../b.txt"); err != nil || got != filepath.Join(dest, "b.txt") {
		t.Fatalf("safePath clean = %q, %v", got, err)
	}
	if got, err := safePath(dest, "a\\b.txt"); err != nil || got != filepath.Join(dest, "a", "b.txt") {
		t.Fatalf("safePath backslash = %q, %v", got, err)
	}
}

func TestValidateLinkTarget(t *testing.T) {
	bad := [][2]string{
		{"link", ""},
		{"link", "/abs"},
		{"link", "C:\\abs"},
		{"link", "\\\\server\\share"},
		{"link", ".."},
		{"link", "../x"},
		{"sub/link", "../../x"},
	}
	for _, tc := range bad {
		if err := validateLinkTarget(tc[0], tc[1]); !errors.Is(err, ErrUnsafeEntry) {
			t.Fatalf("validateLinkTarget(%q, %q) = %v, want ErrUnsafeEntry", tc[0], tc[1], err)
		}
	}
	for _, tc := range [][2]string{{"link", "ok"}, {"sub/link", "../ok"}, {"sub/link", "sub2/ok"}} {
		if err := validateLinkTarget(tc[0], tc[1]); err != nil {
			t.Fatalf("validateLinkTarget(%q, %q) = %v", tc[0], tc[1], err)
		}
	}
}

func TestExtractUnsafeEntries(t *testing.T) {
	root := t.TempDir()
	names := []string{"../evil.txt", "/abs.txt", "C:\\abs.txt", "\\evil.txt", "sub\\..\\..\\evil.txt"}
	for i, name := range names {
		path := filepath.Join(root, "evil", string(rune('a'+i))+".zip")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		writeZipSpec(t, path, []zipSpec{{name: name, body: "boom"}})
		if err := Extract(path, filepath.Join(root, "out", string(rune('a'+i)))); !errors.Is(err, ErrUnsafeEntry) {
			t.Fatalf("name %q: error = %v, want ErrUnsafeEntry", name, err)
		}
	}

	// 合法但需要清理的路径会被归一化。
	cleanZip := filepath.Join(root, "clean.zip")
	writeZipSpec(t, cleanZip, []zipSpec{{name: "a/../b.txt", body: "ok"}})
	dest := filepath.Join(root, "clean-out")
	if err := Extract(cleanZip, dest); err != nil {
		t.Fatalf("Extract clean: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "b.txt"), "ok")

	// tar 路径穿越。
	tarPath := filepath.Join(root, "evil.tar")
	writeFile(t, tarPath, tarBytes(t, func(writer *tar.Writer) {
		body := []byte("boom")
		_ = writer.WriteHeader(&tar.Header{Name: "../evil", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = writer.Write(body)
	}))
	if err := Extract(tarPath, filepath.Join(root, "tar-out")); !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("tar traversal = %v", err)
	}
}

func TestExtractLimitsAndHooks(t *testing.T) {
	root := t.TempDir()
	tarPath := filepath.Join(root, "files.tar")
	writeFile(t, tarPath, tarBytes(t, func(writer *tar.Writer) {
		for _, name := range []string{"a.bin", "b.bin"} {
			body := bytes.Repeat([]byte("x"), 800)
			_ = writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
			_, _ = writer.Write(body)
		}
	}))

	if err := Extract(tarPath, filepath.Join(root, "o1"), WithMaxFileSize(10)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("max file size = %v", err)
	}
	if err := Extract(tarPath, filepath.Join(root, "o2"), WithMaxFiles(1)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("max files = %v", err)
	}
	if err := Extract(tarPath, filepath.Join(root, "o3"), WithMaxTotalSize(1000)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("max total size = %v", err)
	}

	boom := errors.New("hook stop")
	if err := Extract(tarPath, filepath.Join(root, "o4"), WithEntryHook(func(Entry) error { return boom })); !errors.Is(err, boom) {
		t.Fatalf("hook error = %v", err)
	}
	var seen []string
	if err := Extract(tarPath, filepath.Join(root, "o5"), WithEntryHook(func(entry Entry) error {
		seen = append(seen, entry.Name)
		return nil
	})); err != nil {
		t.Fatalf("hook extract: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("hook entries = %v", seen)
	}

	// 覆盖与保留属性。
	modTime := time.Unix(1600000000, 0)
	zipPath := filepath.Join(root, "attr.zip")
	writeZipSpec(t, zipPath, []zipSpec{{name: "attr.txt", body: "v1", mode: 0o750, modTime: modTime}})
	dest := filepath.Join(root, "attr-out")
	if err := Extract(zipPath, dest, WithPreserveMode(), WithPreserveTime()); err != nil {
		t.Fatalf("extract with attrs: %v", err)
	}
	if err := Extract(zipPath, dest); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second extract = %v", err)
	}
	if err := Extract(zipPath, dest, WithOverwrite()); err != nil {
		t.Fatalf("overwrite extract: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "attr.txt"), "v1")
}

func TestExtractDestinationErrors(t *testing.T) {
	root := t.TempDir()
	zipPath := filepath.Join(root, "a.zip")
	writeZipSpec(t, zipPath, []zipSpec{{name: "a.txt", body: "x"}})

	if err := Extract(zipPath, "   "); err == nil {
		t.Fatal("expected empty destination error")
	}
	blocker := filepath.Join(root, "blocker")
	writeFile(t, blocker, []byte("x"))
	if err := Extract(zipPath, blocker); err == nil {
		t.Fatal("expected destination mkdir error")
	}
	if err := Extract(filepath.Join(root, "missing.zip"), filepath.Join(root, "o")); err == nil {
		t.Fatal("expected missing source error")
	}

	// 父路径是文件导致创建父目录失败。
	parentZip := filepath.Join(root, "parent.zip")
	writeZipSpec(t, parentZip, []zipSpec{{name: "parent/child.txt", body: "x"}})
	dest := filepath.Join(root, "parent-out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dest, "parent"), []byte("file"))
	if err := Extract(parentZip, dest); err == nil {
		t.Fatal("expected parent mkdir error")
	}

	// reader 输入校验。
	if err := ExtractTo(nil, Zip, dest); err == nil {
		t.Fatal("expected nil reader error")
	}
	if err := ExtractTo(bytes.NewReader([]byte("x")), Format("rar"), dest); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("unsupported reader format = %v", err)
	}
	if _, err := ListTo(nil, Zip); err == nil {
		t.Fatal("expected ListTo nil reader error")
	}
	if _, err := ListTo(bytes.NewReader([]byte("x")), Format("rar")); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("unsupported ListTo format = %v", err)
	}
	if _, err := List(filepath.Join(root, "missing.zip")); err == nil {
		t.Fatal("expected List missing source error")
	}
}

func TestSymlinkExtraction(t *testing.T) {
	root := t.TempDir()

	validZip := filepath.Join(root, "valid-link.zip")
	writeZipSpec(t, validZip, []zipSpec{{
		name: "link",
		body: "ok",
		mode: fs.ModeSymlink | 0o777,
	}})
	if err := Extract(validZip, filepath.Join(root, "no-allow")); !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("symlink without option = %v", err)
	}

	// 已存在目标且未开启覆盖。
	dest := filepath.Join(root, "existing")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dest, "link"), []byte("old"))
	if err := Extract(validZip, dest, WithAllowSymlinks()); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("existing symlink target = %v", err)
	}

	// 覆盖路径（若系统不允许创建符号链接，仍应返回带 symlink 的错误）。
	err := Extract(validZip, dest, WithAllowSymlinks(), WithOverwrite())
	if err != nil && !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("overwrite symlink error = %v", err)
	}

	// 目标为空 / 绝对路径 / 逃逸。
	for i, spec := range []zipSpec{
		{name: "empty", body: "  ", mode: fs.ModeSymlink | 0o777},
		{name: "abs", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777},
		{name: "escape", body: "../../outside", mode: fs.ModeSymlink | 0o777},
	} {
		path := filepath.Join(root, "link", string(rune('a'+i))+".zip")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		writeZipSpec(t, path, []zipSpec{spec})
		if err := Extract(path, filepath.Join(root, "link-out", string(rune('a'+i))), WithAllowSymlinks()); !errors.Is(err, ErrUnsafeEntry) {
			t.Fatalf("spec %s: error = %v, want ErrUnsafeEntry", spec.name, err)
		}
	}

	// 符号链接条目名本身逃逸。
	badName := filepath.Join(root, "bad-name.zip")
	writeZipSpec(t, badName, []zipSpec{{name: "../link", body: "ok", mode: fs.ModeSymlink | 0o777}})
	if err := Extract(badName, filepath.Join(root, "bad-name-out"), WithAllowSymlinks()); !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("symlink name escape = %v", err)
	}

	// 父路径是文件。
	parentZip := filepath.Join(root, "link-parent.zip")
	writeZipSpec(t, parentZip, []zipSpec{{name: "parent/link", body: "ok", mode: fs.ModeSymlink | 0o777}})
	parentDest := filepath.Join(root, "link-parent-out")
	if err := os.MkdirAll(parentDest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(parentDest, "parent"), []byte("file"))
	if err := Extract(parentZip, parentDest, WithAllowSymlinks()); err == nil {
		t.Fatal("expected symlink parent mkdir error")
	}
}

func TestTarEntryTypes(t *testing.T) {
	root := t.TempDir()
	modTime := time.Unix(1600000000, 0)

	path := filepath.Join(root, "types.tar")
	writeFile(t, path, tarBytes(t, func(writer *tar.Writer) {
		_ = writer.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "global", ModTime: modTime})
		_ = writer.WriteHeader(&tar.Header{Name: "dir/", Typeflag: tar.TypeDir, Mode: 0o750, ModTime: modTime})
		body := []byte("regular")
		_ = writer.WriteHeader(&tar.Header{Name: "dir/file.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body)), ModTime: modTime})
		_, _ = writer.Write(body)
	}))
	dest := filepath.Join(root, "types-out")
	if err := Extract(path, dest, WithPreserveMode(), WithPreserveTime()); err != nil {
		t.Fatalf("Extract types: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "dir", "file.txt"), "regular")
	if entries, err := List(path); err != nil || !hasEntry(entries, "dir/file.txt") {
		t.Fatalf("List types: %v %v", err, entries)
	}

	hardLink := filepath.Join(root, "hard.tar")
	writeFile(t, hardLink, tarBytes(t, func(writer *tar.Writer) {
		_ = writer.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeLink, Linkname: "target"})
	}))
	if err := Extract(hardLink, filepath.Join(root, "hard-out")); !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("hard link = %v", err)
	}

	fifo := filepath.Join(root, "fifo.tar")
	writeFile(t, fifo, tarBytes(t, func(writer *tar.Writer) {
		_ = writer.WriteHeader(&tar.Header{Name: "pipe", Typeflag: tar.TypeFifo})
	}))
	if err := Extract(fifo, filepath.Join(root, "fifo-out")); !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("fifo = %v", err)
	}

	symlink := filepath.Join(root, "symlink.tar")
	writeFile(t, symlink, tarBytes(t, func(writer *tar.Writer) {
		_ = writer.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "ok"})
	}))
	if err := Extract(symlink, filepath.Join(root, "symlink-out")); !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("tar symlink without option = %v", err)
	}
	if err := Extract(symlink, filepath.Join(root, "symlink-out2"), WithAllowSymlinks()); err != nil {
		// 没有符号链接权限时也应只返回 symlink 相关错误。
		if !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("tar symlink allow error = %v", err)
		}
	}
}

func TestCompressVariants(t *testing.T) {
	root, src := prepareSource(t)
	file := filepath.Join(src, "a.txt")

	nested := filepath.Join(root, "deep", "dir", "out.zip")
	if err := Compress(nested, file); err != nil {
		t.Fatalf("Compress nested: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("nested archive: %v", err)
	}

	for _, format := range []Format{Zip, Tar, TarGz, Gz} {
		var buf bytes.Buffer
		if err := CompressTo(&buf, format, file); err != nil {
			t.Fatalf("CompressTo %s: %v", format, err)
		}
		if buf.Len() == 0 {
			t.Fatalf("empty %s output", format)
		}
		if _, err := ListTo(bytes.NewReader(buf.Bytes()), format); err != nil {
			t.Fatalf("ListTo %s: %v", format, err)
		}
	}

	if err := CompressTo(nil, Zip, file); err == nil {
		t.Fatal("expected nil writer error")
	}
	if err := Compress(filepath.Join(root, "none.zip")); err == nil {
		t.Fatal("expected no sources error")
	}
	if err := Compress(filepath.Join(root, "missing.zip"), filepath.Join(root, "missing.txt")); err == nil {
		t.Fatal("expected missing source error")
	}
	if err := Compress(filepath.Join(root, "dot.zip"), "."); err == nil {
		t.Fatal("expected invalid source directory error")
	}
	if err := CompressAs(filepath.Join(root, "bad-format.bin"), Format("rar"), file); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("unsupported compress format = %v", err)
	}

	// gz 只接受单个普通文件。
	if err := CompressAs(filepath.Join(root, "two.gz"), Gz, file, filepath.Join(src, "nested", "b.txt")); err == nil {
		t.Fatal("expected gz multiple files error")
	}
	if err := CompressAs(filepath.Join(root, "dir.gz"), Gz, src); err == nil {
		t.Fatal("expected gz directory error")
	}

	// 目标目录已存在为文件。
	blocker := filepath.Join(root, "blocker")
	writeFile(t, blocker, []byte("x"))
	if err := Compress(filepath.Join(blocker, "out.zip"), file); err == nil {
		t.Fatal("expected destination mkdir error")
	}

	// 目标路径是已存在的目录，提交重命名失败。
	dirDest := filepath.Join(root, "as-dir")
	if err := os.MkdirAll(dirDest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := CompressAs(dirDest, Zip, file); err == nil {
		t.Fatal("expected rename to directory error")
	}
}

func TestWriteHelpers(t *testing.T) {
	root, src := prepareSource(t)
	existing := filepath.Join(src, "a.txt")
	info, err := os.Stat(existing)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	missing := filepath.Join(root, "missing.txt")

	var buf bytes.Buffer
	if err := writeZip(&buf, []sourceEntry{{name: "missing.txt", path: missing, info: info}}); err == nil {
		t.Fatal("expected zip copy error")
	}
	if err := writeTar(&buf, []sourceEntry{{name: "missing.txt", path: missing, info: info}}, false); err == nil {
		t.Fatal("expected tar copy error")
	}
	if err := writeTar(&buf, []sourceEntry{{name: "missing.txt", path: missing, info: info}}, true); err == nil {
		t.Fatal("expected tar.gz copy error")
	}
	if err := writeGzipFile(&buf, missing); err == nil {
		t.Fatal("expected gzip open error")
	}

	// 写入器失败路径。
	if err := writeZip(failingWriter{}, []sourceEntry{{name: "a.txt", path: existing, info: info}}); err == nil {
		t.Fatal("expected zip writer error")
	}
	if err := writeTar(failingWriter{}, []sourceEntry{{name: "a.txt", path: existing, info: info}}, false); err == nil {
		t.Fatal("expected tar writer error")
	}
	if err := writeGzipFile(failingWriter{}, existing); err == nil {
		t.Fatal("expected gzip writer error")
	}
	if err := writeArchive(&buf, Format("rar"), nil); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("writeArchive unsupported = %v", err)
	}

	dirInfo, err := os.Stat(src)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if err := writeArchive(&buf, Gz, []sourceEntry{{name: "src", path: src, info: dirInfo}}); err == nil {
		t.Fatal("expected gz directory error")
	}
}

func TestCorruptZipEntryData(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "crc.zip")
	writeZipSpec(t, path, []zipSpec{{name: "a.txt", body: "alpha", method: zip.Store}})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	idx := bytes.Index(data, []byte("alpha"))
	if idx < 0 {
		t.Fatal("payload not found")
	}
	data[idx] = 'X'
	writeFile(t, path, data)

	dest := filepath.Join(root, "out")
	if err := Extract(path, dest); err == nil {
		t.Fatal("expected checksum error")
	}
	if _, statErr := os.Stat(filepath.Join(dest, "a.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("partial file should be removed, stat err = %v", statErr)
	}
}

func TestExtractStreamUnsupported(t *testing.T) {
	if err := extractStream(bytes.NewReader(nil), Format("rar"), "", t.TempDir(), newExtractOptions(nil)); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("extractStream = %v", err)
	}
	if _, err := listStream(bytes.NewReader(nil), Format("rar"), ""); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("listStream = %v", err)
	}
}

func TestListZipInvalidAndExtractZipSymlinkOpenError(t *testing.T) {
	root := t.TempDir()
	if _, err := listZip(bytes.NewReader([]byte("garbage")), 7); err == nil {
		t.Fatal("expected listZip error")
	}
	if err := extractZip(bytes.NewReader([]byte("garbage")), 7, t.TempDir(), newExtractOptions(nil)); err == nil {
		t.Fatal("expected extractZip error")
	}

	// 不支持的压缩算法：zip.NewReader 成功，file.Open 失败。
	path := filepath.Join(root, "method.zip")
	writeZipSpec(t, path, []zipSpec{{name: "a.txt", body: "data", method: zip.Store}})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	patchZipMethod(t, data, 99)
	writeFile(t, path, data)
	if err := Extract(path, filepath.Join(root, "method-out")); err == nil {
		t.Fatal("expected unsupported compression error")
	}
}

// patchZipMethod 修改本地文件头与中央目录中的压缩方法。
func patchZipMethod(t *testing.T, data []byte, method uint16) {
	t.Helper()
	local := []byte{'P', 'K', 3, 4}
	central := []byte{'P', 'K', 1, 2}
	patched := false
	for _, sig := range [][]byte{local, central} {
		for offset := 0; ; {
			idx := bytes.Index(data[offset:], sig)
			if idx < 0 {
				break
			}
			pos := offset + idx
			methodPos := pos + 8
			if bytes.Equal(sig, central) {
				methodPos = pos + 10
			}
			if methodPos+2 <= len(data) {
				data[methodPos] = byte(method)
				data[methodPos+1] = 0
				patched = true
			}
			offset = pos + 4
		}
	}
	if !patched {
		t.Fatal("no zip headers found")
	}
}

func TestReadAllLimitedBranches(t *testing.T) {
	data, err := readAllLimited(bytes.NewReader([]byte("abc")), 0)
	if err != nil || string(data) != "abc" {
		t.Fatalf("readAllLimited unlimited = %q, %v", data, err)
	}
	boom := errors.New("read failed")
	if _, err := readAllLimited(io.MultiReader(bytes.NewReader([]byte("abc")), errorReader{boom}), 0); !errors.Is(err, boom) {
		t.Fatalf("readAllLimited error = %v", err)
	}
	if _, err := readAllLimited(bytes.NewReader([]byte("abcdef")), 3); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("readAllLimited limit = %v", err)
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

type limitedFailWriter struct{ remaining int }

func (w *limitedFailWriter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, errors.New("write failed")
	}
	n := len(p)
	if n > w.remaining {
		n = w.remaining
	}
	w.remaining -= n
	if n < len(p) {
		return n, errors.New("write failed")
	}
	return n, nil
}

func pseudoRandomBytes(n int) []byte {
	data := make([]byte, n)
	state := uint32(1)
	for i := range data {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		data[i] = byte(state)
	}
	return data
}

func TestDetectErrorsOnExtractAndList(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, "plain.bin")
	writeFile(t, plain, []byte("hello"))

	if err := Extract(plain, filepath.Join(root, "out")); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("Extract unsupported = %v", err)
	}
	if _, err := List(plain); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("List unsupported = %v", err)
	}
	if _, err := DetectFormat(root); err == nil {
		t.Fatal("expected directory read error")
	}

	boom := errors.New("read failed")
	if _, err := ListTo(errorReader{boom}, Zip); !errors.Is(err, boom) {
		t.Fatalf("ListTo error = %v", err)
	}
	if _, err := readAllLimited(io.MultiReader(bytes.NewReader([]byte("abc")), errorReader{boom}), 3); !errors.Is(err, boom) {
		t.Fatalf("readAllLimited limited error = %v", err)
	}
	if err := ExtractTo(bytes.NewReader(gzipBytes(t, []byte("x"))), Gz, "   "); err == nil {
		t.Fatal("expected empty destination error")
	}
	if err := CompressTo(&bytes.Buffer{}, Zip); err == nil {
		t.Fatal("expected no sources error")
	}
}

func TestCollectSourcesSymlink(t *testing.T) {
	root, src := prepareSource(t)
	target := filepath.Join(src, "a.txt")

	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if err := Compress(filepath.Join(root, "link.zip"), link); !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("symlink source = %v", err)
	}

	dirWithLink := filepath.Join(root, "withlink")
	if err := os.MkdirAll(dirWithLink, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(dirWithLink, "inner.txt")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if err := Compress(filepath.Join(root, "withlink.zip"), dirWithLink); !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("symlink inside directory = %v", err)
	}
}

func TestDirectoryEntryErrors(t *testing.T) {
	root := t.TempDir()

	hookTar := filepath.Join(root, "hook-dir.tar")
	writeFile(t, hookTar, tarBytes(t, func(writer *tar.Writer) {
		_ = writer.WriteHeader(&tar.Header{Name: "dir/", Typeflag: tar.TypeDir, Mode: 0o755})
	}))
	boom := errors.New("hook stop")
	if err := Extract(hookTar, filepath.Join(root, "hook-out"), WithEntryHook(func(Entry) error { return boom })); !errors.Is(err, boom) {
		t.Fatalf("dir hook error = %v", err)
	}

	evilTar := filepath.Join(root, "evil-dir.tar")
	writeFile(t, evilTar, tarBytes(t, func(writer *tar.Writer) {
		_ = writer.WriteHeader(&tar.Header{Name: "../evil/", Typeflag: tar.TypeDir, Mode: 0o755})
	}))
	if err := Extract(evilTar, filepath.Join(root, "evil-out")); !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("dir traversal = %v", err)
	}

	parentTar := filepath.Join(root, "parent-dir.tar")
	writeFile(t, parentTar, tarBytes(t, func(writer *tar.Writer) {
		_ = writer.WriteHeader(&tar.Header{Name: "parent/sub/", Typeflag: tar.TypeDir, Mode: 0o755})
	}))
	dest := filepath.Join(root, "parent-out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dest, "parent"), []byte("file"))
	if err := Extract(parentTar, dest); err == nil {
		t.Fatal("expected directory mkdir error")
	}

	// zip 目录条目逃逸。
	zipPath := filepath.Join(root, "evil-dir.zip")
	writeZipSpec(t, zipPath, []zipSpec{{name: "../evil/", body: ""}})
	if err := Extract(zipPath, filepath.Join(root, "evil-zip-out")); !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("zip dir traversal = %v", err)
	}
}

func TestFileOpenGenericError(t *testing.T) {
	root := t.TempDir()
	zipPath := filepath.Join(root, "a.zip")
	writeZipSpec(t, zipPath, []zipSpec{{name: "a.txt", body: "x"}})
	dest := filepath.Join(root, "out")
	if err := os.MkdirAll(filepath.Join(dest, "a.txt"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := Extract(zipPath, dest, WithOverwrite()); err == nil {
		t.Fatal("expected file open error")
	}
}

func TestSymlinkSafePathError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "abs-link.zip")
	writeZipSpec(t, path, []zipSpec{{name: "/abs/link", body: "ok", mode: fs.ModeSymlink | 0o777}})
	if err := Extract(path, filepath.Join(root, "out"), WithAllowSymlinks()); !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("absolute symlink name = %v", err)
	}
}

func TestWriteHelperErrorBranches(t *testing.T) {
	root, src := prepareSource(t)
	existing := filepath.Join(src, "a.txt")
	info, err := os.Stat(existing)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// 文件信息为 nil 时 tar 头生成失败。
	if err := writeTar(&bytes.Buffer{}, []sourceEntry{{name: "x"}}, false); err == nil {
		t.Fatal("expected tar header error")
	}

	// 关闭阶段失败。
	if err := writeTar(&limitedFailWriter{}, nil, false); err == nil {
		t.Fatal("expected tar close error")
	}
	big := filepath.Join(root, "big.bin")
	writeFile(t, big, []byte("payload"))
	if err := writeGzipFile(&limitedFailWriter{remaining: 10}, big); err == nil {
		t.Fatal("expected gzip close error")
	}

	// 复制阶段失败：较大的不可压缩数据会触发 flate 写出。
	if err := writeZip(failingWriter{}, []sourceEntry{{name: "big.bin", path: writeBigFile(t, root), info: bigInfo(t, root)}}); err == nil {
		t.Fatal("expected zip copy error")
	}
	_ = info
}

func writeBigFile(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "random.bin")
	writeFile(t, path, pseudoRandomBytes(256<<10))
	return path
}

func bigInfo(t *testing.T, root string) fs.FileInfo {
	t.Helper()
	info, err := os.Stat(filepath.Join(root, "random.bin"))
	if err != nil {
		t.Fatalf("stat big: %v", err)
	}
	return info
}

func TestZipSymlinkReadErrors(t *testing.T) {
	root := t.TempDir()

	// 不支持的压缩方法导致 Open 失败。
	openErrPath := filepath.Join(root, "open-error.zip")
	writeZipSpec(t, openErrPath, []zipSpec{{name: "link", body: "ok", mode: fs.ModeSymlink | 0o777, method: zip.Store}})
	data, err := os.ReadFile(openErrPath)
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	patchZipMethod(t, data, 99)
	writeFile(t, openErrPath, data)
	if err := Extract(openErrPath, filepath.Join(root, "open-error-out"), WithAllowSymlinks()); err == nil {
		t.Fatal("expected symlink open error")
	}

	// 内容 CRC 错误导致读取失败。
	readErrPath := filepath.Join(root, "read-error.zip")
	writeZipSpec(t, readErrPath, []zipSpec{{name: "link", body: "link-target", mode: fs.ModeSymlink | 0o777, method: zip.Store}})
	data, err = os.ReadFile(readErrPath)
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	idx := bytes.Index(data, []byte("link-target"))
	if idx < 0 {
		t.Fatal("symlink body not found")
	}
	data[idx] = 'X'
	writeFile(t, readErrPath, data)
	if err := Extract(readErrPath, filepath.Join(root, "read-error-out"), WithAllowSymlinks()); err == nil {
		t.Fatal("expected symlink read error")
	}
}
