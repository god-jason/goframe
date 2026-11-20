package aliyun

// 本文件通过 httptest 模拟 OSS 服务端，覆盖对象存储读写与签名链接路径。

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// storageAt 启动模拟 OSS 的 httptest 服务，返回绑定其端点的存储客户端与服务地址。
func storageAt(t *testing.T, handler http.HandlerFunc) (*Storage, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := baseConfig()
	c.Storage.Enabled = true
	c.Storage.Endpoint = srv.URL
	c.Storage.Bucket = "demo"
	st, err := NewStorage(c)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	return st, srv.URL
}

// ossMux 按方法模拟 OSS 对象操作的成功响应。
func ossMux(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		_, _ = w.Write([]byte("hello"))
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// ossDenied 对所有请求返回 403 服务错误。
func ossDenied(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>denied</Message><RequestId>req-x</RequestId></Error>`))
}

func TestStoragePutGetDeleteOverHTTP(t *testing.T) {
	st, _ := storageAt(t, http.HandlerFunc(ossMux))
	ctx := context.Background()
	if err := st.Put(ctx, "a/b.txt", strings.NewReader("data")); err != nil {
		t.Fatalf("put: %v", err)
	}
	body, err := st.Get(ctx, "a/b.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	raw, _ := io.ReadAll(body)
	_ = body.Close()
	if string(raw) != "hello" {
		t.Fatalf("get body = %q", raw)
	}
	if err := st.Delete(ctx, "a/b.txt"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if st.Client() == nil || st.Bucket() == nil {
		t.Fatal("accessors must not be nil")
	}
}

func TestStorageErrorsOverHTTP(t *testing.T) {
	st, _ := storageAt(t, http.HandlerFunc(ossDenied))
	ctx := context.Background()
	if err := st.Put(ctx, "k", strings.NewReader("d")); err == nil || !strings.Contains(err.Error(), "oss put") {
		t.Fatalf("put error: %v", err)
	}
	if _, err := st.Get(ctx, "k"); err == nil || !strings.Contains(err.Error(), "oss get") {
		t.Fatalf("get error: %v", err)
	}
	if err := st.Delete(ctx, "k"); err == nil || !strings.Contains(err.Error(), "oss delete") {
		t.Fatalf("delete error: %v", err)
	}
}

func TestStorageContextErrors(t *testing.T) {
	st, _ := storageAt(t, http.HandlerFunc(ossMux))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := st.Put(cancelled, "k", strings.NewReader("d")); err == nil {
		t.Fatal("put: expected ctx error")
	}
	if _, err := st.Get(cancelled, "k"); err == nil {
		t.Fatal("get: expected ctx error")
	}
	if err := st.Delete(cancelled, "k"); err == nil {
		t.Fatal("delete: expected ctx error")
	}
	if _, err := st.SignURL(cancelled, "k", time.Minute); err == nil {
		t.Fatal("sign: expected ctx error")
	}
}

func TestStorageSignURL(t *testing.T) {
	st, _ := storageAt(t, http.HandlerFunc(ossMux))
	url, err := st.SignURL(context.Background(), "a/b.txt", 0)
	if err != nil {
		t.Fatalf("sign url: %v", err)
	}
	if !strings.Contains(url, "Signature=") || !strings.Contains(url, "Expires=") {
		t.Fatalf("signed url = %s", url)
	}
	if _, err := st.SignURL(context.Background(), "k", time.Hour); err != nil {
		t.Fatalf("sign url with expiry: %v", err)
	}
	// 空对象名触发 SignURL 错误分支。
	if _, err := st.SignURL(context.Background(), "", time.Minute); err == nil {
		t.Fatal("expected sign url error")
	}
}

func TestNewStorageErrorBranches(t *testing.T) {
	// 校验失败：启用存储但缺少桶名。
	c := baseConfig()
	c.Storage.Enabled = true
	c.Storage.Endpoint = "https://oss-cn-hangzhou.aliyuncs.com"
	if _, err := NewStorage(c); err == nil {
		t.Fatal("expected validate error")
	}

	// 未启用顶层时跳过校验，非法 endpoint 触发 oss.New 错误。
	c = baseConfig()
	c.Enabled = false
	c.Storage.Endpoint = "http://exa mple.com"
	if _, err := NewStorage(c); err == nil || !strings.Contains(err.Error(), "create oss client") {
		t.Fatalf("oss new error: %v", err)
	}

	// endpoint 合法但桶名为空，触发 client.Bucket 错误。
	c = baseConfig()
	c.Enabled = false
	c.Storage.Endpoint = "http://127.0.0.1:1"
	if _, err := NewStorage(c); err == nil || !strings.Contains(err.Error(), "open oss bucket") {
		t.Fatalf("bucket error: %v", err)
	}
}
