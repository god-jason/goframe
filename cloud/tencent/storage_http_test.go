package tencent

// 本文件通过 httptest 模拟腾讯云 COS 服务端，覆盖对象存储读写与签名链接路径。

import (
	"context"
	"hash/crc64"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// storageAt 启动模拟 COS 的 httptest 服务，返回绑定其端点的存储客户端。
func storageAt(t *testing.T, handler http.HandlerFunc) *Storage {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := baseConfig()
	c.Storage.Enabled = true
	c.Storage.Bucket = "demo-1250000000"
	c.Storage.Endpoint = srv.URL
	st, err := NewStorage(c)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	return st
}

// cosMux 按方法模拟 COS 对象操作的成功响应。
func cosMux(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		// COS SDK 会校验响应头中的 CRC64（ECMA 多项式）。
		body, _ := io.ReadAll(r.Body)
		sum := crc64.Checksum(body, crc64.MakeTable(crc64.ECMA))
		w.Header().Set("x-cos-hash-crc64ecma", strconv.FormatUint(sum, 10))
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		_, _ = w.Write([]byte("hello"))
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// cosDenied 对所有请求返回 403 服务错误。
func cosDenied(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>denied</Message><RequestId>req-x</RequestId></Error>`))
}

func TestStoragePutGetDeleteOverHTTP(t *testing.T) {
	st := storageAt(t, http.HandlerFunc(cosMux))
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
	if st.Client() == nil {
		t.Fatal("client accessor must not be nil")
	}
}

func TestStorageErrorsOverHTTP(t *testing.T) {
	st := storageAt(t, http.HandlerFunc(cosDenied))
	ctx := context.Background()
	if err := st.Put(ctx, "k", strings.NewReader("d")); err == nil || !strings.Contains(err.Error(), "cos put") {
		t.Fatalf("put error: %v", err)
	}
	if _, err := st.Get(ctx, "k"); err == nil || !strings.Contains(err.Error(), "cos get") {
		t.Fatalf("get error: %v", err)
	}
	if err := st.Delete(ctx, "k"); err == nil || !strings.Contains(err.Error(), "cos delete") {
		t.Fatalf("delete error: %v", err)
	}
}

func TestStorageContextAndSignURL(t *testing.T) {
	st := storageAt(t, http.HandlerFunc(cosMux))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.SignURL(cancelled, "k", time.Minute); err == nil {
		t.Fatal("sign: expected ctx error")
	}

	// 默认有效期分支与显式有效期均应生成含签名参数的链接。
	signed, err := st.SignURL(context.Background(), "a/b.txt", 0)
	if err != nil {
		t.Fatalf("sign url: %v", err)
	}
	if !strings.Contains(signed, "q-sign-algorithm") || !strings.Contains(signed, "q-signature=") {
		t.Fatalf("signed url = %s", signed)
	}
	if _, err := st.SignURL(context.Background(), "k", time.Hour); err != nil {
		t.Fatalf("sign url with expiry: %v", err)
	}
	// 空对象名触发签名错误分支。
	if _, err := st.SignURL(context.Background(), "", time.Minute); err == nil {
		t.Fatal("expected sign url error")
	}
}

func TestNewStorageURLError(t *testing.T) {
	// 未启用顶层校验时，非法 endpoint 触发 url.Parse 错误。
	c := baseConfig()
	c.Enabled = false
	c.Storage.Endpoint = "http://[::1"
	if _, err := NewStorage(c); err == nil || !strings.Contains(err.Error(), "parse cos bucket url") {
		t.Fatalf("url parse error: %v", err)
	}
}
