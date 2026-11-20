package storage

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewLocalAbsPathError 覆盖 filepath.Abs 失败的分支。
func TestNewLocalAbsPathError(t *testing.T) {
	if _, err := NewLocal(Config{Path: "\x00invalid"}); err == nil {
		t.Fatal("expected error for invalid path")
	}
}

// TestLocalStoragePutCreateTempError 覆盖 os.CreateTemp 失败的分支。
// 通过 icacls 拒绝当前用户对子目录的写权限，使 MkdirAll 成功但 CreateTemp 失败。
func TestLocalStoragePutCreateTempError(t *testing.T) {
	store := newTestStorage(t)
	sub := filepath.Join(store.root, "ro")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	user := os.Getenv("USERNAME")
	if user == "" {
		t.Skip("无法获取当前用户名")
	}
	out, err := exec.Command("icacls", sub, "/deny", user+":W").CombinedOutput()
	if err != nil {
		t.Skipf("icacls 拒绝写入失败: %s", out)
	}
	t.Cleanup(func() { _ = exec.Command("icacls", sub, "/remove:d", user).Run() })

	if err := store.Put(context.Background(), "ro/file.txt", strings.NewReader("x")); err == nil ||
		!strings.Contains(err.Error(), "create temp file") {
		t.Fatalf("err = %v", err)
	}
}

// TestLocalStoragePutCloseTempError 覆盖 tmp.Close 失败的分支。
// 传入实现 io.WriterTo 的读取器，在写入完成后提前关闭临时文件，
// 使 io.Copy 成功返回后 Put 再次 Close 时得到错误。
func TestLocalStoragePutCloseTempError(t *testing.T) {
	store := newTestStorage(t)
	err := store.Put(context.Background(), "a.txt", &closingWriterTo{data: []byte("hello")})
	if err == nil || !strings.Contains(err.Error(), "close temp file") {
		t.Fatalf("err = %v", err)
	}
}

// closingWriterTo 在 WriteTo 中写完即关闭目标，触发后续重复关闭错误。
type closingWriterTo struct{ data []byte }

func (c *closingWriterTo) Read([]byte) (int, error) { return 0, io.EOF }

func (c *closingWriterTo) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(c.data)
	if err != nil {
		return int64(n), err
	}
	if cl, ok := w.(io.Closer); ok {
		_ = cl.Close()
	}
	return int64(n), nil
}

// TestLocalStoragePutRenameError 覆盖 os.Rename 失败的分支。
func TestLocalStoragePutRenameError(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()
	// 目标路径预先创建为目录，Rename 文件到目录会失败。
	if err := os.MkdirAll(filepath.Join(store.root, "blocked.txt"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	err := store.Put(ctx, "blocked.txt", strings.NewReader("x"))
	if err == nil || !strings.Contains(err.Error(), "commit file") {
		t.Fatalf("err = %v", err)
	}
}

// TestLocalStorageExistsStatError 覆盖 Exists 中 os.Stat 返回非 NotExist 错误的分支。
func TestLocalStorageExistsStatError(t *testing.T) {
	store := newTestStorage(t)
	// 单段名称超过 255 字符，os.Stat 返回语法错误而非 NotExist。
	long := strings.Repeat("x", 300)
	ok, err := store.Exists(context.Background(), long)
	if err == nil || os.IsNotExist(err) || ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
}
