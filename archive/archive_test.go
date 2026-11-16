package archive

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prepareSource 创建测试用的目录树。
func prepareSource(t *testing.T) (root, src string) {
	t.Helper()
	root = t.TempDir()
	src = filepath.Join(root, "src")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "b.txt"), []byte("beta"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	return root, src
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}

func entryNames(entries []Entry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func hasEntry(entries []Entry, name string) bool {
	for _, entry := range entries {
		if entry.Name == name {
			return true
		}
	}
	return false
}

func TestZipRoundTrip(t *testing.T) {
	root, src := prepareSource(t)
	archivePath := filepath.Join(root, "backup.zip")

	if err := Compress(archivePath, src); err != nil {
		t.Fatalf("Compress: %v", err)
	}

	entries, err := List(archivePath)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, name := range []string{"src", "src/a.txt", "src/nested", "src/nested/b.txt"} {
		if !hasEntry(entries, name) {
			t.Fatalf("entries %v missing %q", entryNames(entries), name)
		}
	}

	dest := filepath.Join(root, "out")
	if err := Extract(archivePath, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "src", "a.txt"), "alpha")
	assertFileContent(t, filepath.Join(dest, "src", "nested", "b.txt"), "beta")
}

func TestTarGzRoundTrip(t *testing.T) {
	root, src := prepareSource(t)
	archivePath := filepath.Join(root, "backup.tar.gz")

	if err := Compress(archivePath, src); err != nil {
		t.Fatalf("Compress: %v", err)
	}

	dest := filepath.Join(root, "out")
	if err := Extract(archivePath, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "src", "a.txt"), "alpha")
	assertFileContent(t, filepath.Join(dest, "src", "nested", "b.txt"), "beta")
}

func TestGzipSingleFile(t *testing.T) {
	root, src := prepareSource(t)
	file := filepath.Join(src, "a.txt")
	archivePath := filepath.Join(root, "a.txt.gz")

	if err := Compress(archivePath, file); err != nil {
		t.Fatalf("Compress: %v", err)
	}

	entries, err := List(archivePath)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "a.txt" {
		t.Fatalf("unexpected entries: %+v", entries)
	}

	dest := filepath.Join(root, "out")
	if err := Extract(archivePath, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "a.txt"), "alpha")
}

func TestExtractRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "evil.zip")
	writeRawZip(t, archivePath, map[string]string{
		"../evil.txt": "boom",
	})

	dest := filepath.Join(root, "out")
	err := Extract(archivePath, dest)
	if !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("Extract error = %v, want ErrUnsafeEntry", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "evil.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("traversal file should not exist, stat err = %v", statErr)
	}
}

func TestExtractRejectsZipSymlink(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "link.zip")

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	writer := zip.NewWriter(file)
	header := &zip.FileHeader{Name: "link"}
	header.SetMode(os.ModeSymlink | 0o777)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatalf("create header: %v", err)
	}
	if _, err := entry.Write([]byte("../outside")); err != nil {
		t.Fatalf("write link: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}

	dest := filepath.Join(root, "out")
	if err := Extract(archivePath, dest); !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("default Extract error = %v, want ErrUnsafeEntry", err)
	}
	// 即使显式允许符号链接，逃逸目标也必须被拒绝。
	if err := Extract(archivePath, dest, WithAllowSymlinks()); !errors.Is(err, ErrUnsafeEntry) {
		t.Fatalf("Extract with symlink error = %v, want ErrUnsafeEntry", err)
	}
}

func TestExtractLimits(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "big.zip")
	writeRawZip(t, archivePath, map[string]string{
		"big.bin": strings.Repeat("x", 2048),
	})

	dest := filepath.Join(root, "out-size")
	if err := Extract(archivePath, dest, WithMaxFileSize(1024)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("file size limit error = %v, want ErrLimitExceeded", err)
	}

	multi := filepath.Join(root, "multi.zip")
	writeRawZip(t, multi, map[string]string{
		"a.bin": strings.Repeat("a", 800),
		"b.bin": strings.Repeat("b", 800),
	})
	dest = filepath.Join(root, "out-total")
	if err := Extract(multi, dest, WithMaxTotalSize(1000)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("total size limit error = %v, want ErrLimitExceeded", err)
	}

	dest = filepath.Join(root, "out-count")
	if err := Extract(multi, dest, WithMaxFiles(1)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("file count limit error = %v, want ErrLimitExceeded", err)
	}
}

func TestExtractOverwrite(t *testing.T) {
	root, src := prepareSource(t)
	archivePath := filepath.Join(root, "backup.zip")
	if err := Compress(archivePath, filepath.Join(src, "a.txt")); err != nil {
		t.Fatalf("Compress: %v", err)
	}

	dest := filepath.Join(root, "out")
	if err := Extract(archivePath, dest); err != nil {
		t.Fatalf("first Extract: %v", err)
	}
	if err := Extract(archivePath, dest); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second Extract error = %v, want ErrAlreadyExists", err)
	}
	if err := Extract(archivePath, dest, WithOverwrite()); err != nil {
		t.Fatalf("Extract with overwrite: %v", err)
	}
}

func TestDetectFormatByMagic(t *testing.T) {
	root, src := prepareSource(t)
	archivePath := filepath.Join(root, "data.bin")
	if err := CompressAs(archivePath, Zip, filepath.Join(src, "a.txt")); err != nil {
		t.Fatalf("CompressAs: %v", err)
	}

	format, err := DetectFormat(archivePath)
	if err != nil {
		t.Fatalf("DetectFormat: %v", err)
	}
	if format != Zip {
		t.Fatalf("format = %q, want %q", format, Zip)
	}

	entries, err := List(archivePath)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "a.txt" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestExtractToReader(t *testing.T) {
	root, src := prepareSource(t)
	archivePath := filepath.Join(root, "backup.zip")
	if err := Compress(archivePath, filepath.Join(src, "a.txt")); err != nil {
		t.Fatalf("Compress: %v", err)
	}
	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	dest := filepath.Join(root, "out")
	if err := ExtractTo(bytes.NewReader(data), Zip, dest); err != nil {
		t.Fatalf("ExtractTo: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "a.txt"), "alpha")
}

func TestExtractToZipInputLimit(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "data.zip")
	writeRawZip(t, archivePath, map[string]string{"a.txt": "alpha"})
	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	err = ExtractTo(bytes.NewReader(data), Zip, filepath.Join(root, "out"), WithMaxArchiveSize(16))
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("ExtractTo error = %v, want ErrLimitExceeded", err)
	}
}

func TestCompressFailurePreservesExistingFile(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "backup.zip")
	if err := os.WriteFile(dest, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	err := CompressAs(dest, Zip, filepath.Join(root, "missing.txt"))
	if err == nil {
		t.Fatal("expected compress error")
	}
	assertFileContent(t, dest, "keep")
}

func TestCompressRejectsDuplicateNames(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "one", "same.txt")
	second := filepath.Join(root, "two", "same.txt")
	for _, file := range []string{first, second} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
	}

	err := Compress(filepath.Join(root, "dup.zip"), first, second)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("Compress error = %v, want duplicate entry error", err)
	}
}

// writeRawZip 直接构造 zip，用于测试恶意或异常归档。
func writeRawZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	writer := zip.NewWriter(file)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
}
