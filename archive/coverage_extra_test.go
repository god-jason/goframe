package archive

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompressDuplicateWalkEntry 覆盖 collectSources 中目录内条目名重复导致 WalkDir 回调返回错误的分支。
// 同一目录作为两个源传入，第二次遍历产生同名条目，add 报重复并经 WalkDir 上抛。
func TestCompressDuplicateWalkEntry(t *testing.T) {
	root, src := prepareSource(t)
	err := Compress(filepath.Join(root, "dup.zip"), src, src)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("Compress error = %v, want duplicate entry error", err)
	}
}

// TestGzSingleFileMaxSizeLimit 覆盖 copyLimited 中实际写出字节超过单文件上限的分支。
// 单文件 gzip 的条目大小未知（Size=0），checkEntry 不拦截，拷贝阶段按真实长度判定。
func TestGzSingleFileMaxSizeLimit(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "big.txt")
	if err := os.WriteFile(src, bytes.Repeat([]byte("y"), 4096), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gzPath := filepath.Join(root, "big.txt.gz")
	if err := CompressAs(gzPath, Gz, src); err != nil {
		t.Fatalf("CompressAs: %v", err)
	}
	err := Extract(gzPath, filepath.Join(root, "out"), WithMaxFileSize(100))
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Extract error = %v, want ErrLimitExceeded", err)
	}
}

// TestTarGlobalHeaderEntries 覆盖 listTar 与 extractor.tar 跳过全局 PAX 头的分支。
// 需以 PAX 格式并携带记录写入全局头，标准 reader 才会把它作为条目返回。
func TestTarGlobalHeaderEntries(t *testing.T) {
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	if err := writer.WriteHeader(&tar.Header{
		Typeflag:   tar.TypeXGlobalHeader,
		Name:       "GlobalHead",
		Format:     tar.FormatPAX,
		PAXRecords: map[string]string{"comment": "global"},
	}); err != nil {
		t.Fatalf("write global header: %v", err)
	}
	body := []byte("hello")
	if err := writer.WriteHeader(&tar.Header{Name: "f.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatalf("write file header: %v", err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}

	entries, err := listTar(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("listTar: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "f.txt" {
		t.Fatalf("entries = %+v", entries)
	}

	dest := t.TempDir()
	if err := extractStream(bytes.NewReader(buf.Bytes()), Tar, "", dest, newExtractOptions(nil)); err != nil {
		t.Fatalf("extractStream: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "f.txt"), "hello")
}

// TestZipCreateHeaderWriteError 覆盖 writeZip 中 writer.CreateHeader 失败的分支。
// 超长条目名使本地头写入撑满 zip 内部 bufio 缓冲并触发底层 writer 错误。
func TestZipCreateHeaderWriteError(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "a.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(src)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	long := strings.Repeat("n", 5000)
	err = writeZip(failingWriter{}, []sourceEntry{{name: long, path: src, info: info}})
	if err == nil || !strings.Contains(err.Error(), "create zip entry") {
		t.Fatalf("writeZip error = %v, want create zip entry error", err)
	}
}

// 说明：以下分支在当前 Windows 环境或实现下逻辑不可达，保留作为防御性代码：
//   - archive.go 527/546/724：依赖 os.Symlink，本机缺少符号链接特权（测试环境会 Skip）。
//   - archive.go 195/235：os.Open 成功后 file.Stat() 失败，常规文件无法稳定触发。
//   - archive.go 444/454：os.CreateTemp / temp.Close 失败，需只读目录或磁盘错误，无法确定构造。
//   - archive.go 543/550/558：WalkDir 回调中 walkErr、filepath.Rel、entry.Info 失败，
//     在正常目录遍历下不会发生。
//   - archive.go 668：本地文件 file.Close() 失败，常规写入后不会发生。
//   - archive.go 765/768：safePath 中 filepath.Rel 失败或二次逃逸判定，Join 已保证不逃逸。
//   - archive.go 108(zip.go)/zip.go 108：zip.FileInfoHeader 对 nil FileInfo 直接 panic 而非返回错误。
//   - zip.go 62：zip 条目 content.Close() 失败，常规读取后不会发生。
