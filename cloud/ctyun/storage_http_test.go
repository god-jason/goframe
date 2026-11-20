package ctyun

// 本文件通过 httptest 模拟 S3 兼容端点，覆盖 ZOS 存储客户端的构造与增删查签路径。
// minio-go 对 IP 端点自动使用 path-style，模拟服务按 multipart 协议应答。

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// s3Handler 应答 minio-go 的 multipart 上传、下载、删除与分片协议。
func s3Handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.Method {
		case http.MethodPost:
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/xml")
			if _, ok := q["uploads"]; ok {
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
					`<InitiateMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
					`<Bucket>bkt</Bucket><Key>obj</Key><UploadId>up-1</UploadId></InitiateMultipartUploadResult>`))
				return
			}
			if q.Get("uploadId") != "up-1" {
				t.Errorf("complete uploadId = %q", q.Get("uploadId"))
			}
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
				`<CompleteMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
				`<Location>http://127.0.0.1/bkt/obj</Location><Bucket>bkt</Bucket><Key>obj</Key>` +
				`<ETag>"cmp-etag"</ETag></CompleteMultipartUploadResult>`))
		case http.MethodPut:
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", `"part-1"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			if _, ok := q["location"]; ok {
				// minio-go 首次调用会探测桶区域。
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
					`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`))
				return
			}
			w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
			w.Header().Set("Content-Length", "11")
			_, _ = w.Write([]byte("hello-ctyun"))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

// newStorageAt 返回端点指向本地模拟服务的存储客户端（显式关闭 TLS）。
func newStorageAt(t *testing.T, handler http.HandlerFunc) *Storage {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := baseConfig()
	useSSL := false
	c.Storage = StorageConfig{
		Enabled:  true,
		Endpoint: strings.TrimPrefix(srv.URL, "http://"),
		Bucket:   "bkt",
		UseSSL:   &useSSL,
	}
	st, err := NewStorage(c)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	return st
}

func TestNewStorageValidateError(t *testing.T) {
	c := baseConfig()
	c.Storage = StorageConfig{Enabled: true, Endpoint: "eos.example.cn"}
	if _, err := NewStorage(c); err == nil || !strings.Contains(err.Error(), "storage") {
		t.Fatalf("expected storage validation error: %v", err)
	}
}

func TestNewStorageEmptyEndpoint(t *testing.T) {
	c := baseConfig()
	c.Enabled = false // 绕过启用态校验，仅测试端点拆分后的空值分支。
	c.Storage = StorageConfig{Endpoint: "http://", Bucket: "bkt"}
	if _, err := NewStorage(c); err == nil || !strings.Contains(err.Error(), "storage.endpoint is required") {
		t.Fatalf("expected empty endpoint error: %v", err)
	}
}

func TestNewStorageMinioError(t *testing.T) {
	c := baseConfig()
	c.Enabled = false
	// 主机名含空格，minio.New 构造客户端失败。
	c.Storage = StorageConfig{Endpoint: "exa mple.com", Bucket: "bkt"}
	if _, err := NewStorage(c); err == nil || !strings.Contains(err.Error(), "create zos client") {
		t.Fatalf("expected minio error: %v", err)
	}
}

func TestNewStorageDefaultSSL(t *testing.T) {
	c := baseConfig()
	// UseSSL 未设置时默认走 HTTPS。
	c.Storage = StorageConfig{Enabled: true, Endpoint: "eos.example.cn", Bucket: "bkt"}
	st, err := NewStorage(c)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	if st.bucket != "bkt" {
		t.Fatalf("bucket = %s", st.bucket)
	}
}

func TestStorageOperationsSuccess(t *testing.T) {
	st := newStorageAt(t, s3Handler(t))
	ctx := context.Background()

	if err := st.Put(ctx, "obj", strings.NewReader("hello-ctyun")); err != nil {
		t.Fatalf("put: %v", err)
	}
	obj, err := st.Get(ctx, "obj")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	raw, err := io.ReadAll(obj)
	_ = obj.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(raw) != "hello-ctyun" {
		t.Fatalf("content = %q", raw)
	}
	if err := st.Delete(ctx, "obj"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	u, err := st.SignURL(ctx, "obj", time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.Contains(u, "X-Amz-Signature") || !strings.Contains(u, "/bkt/obj") {
		t.Fatalf("signed url = %s", u)
	}
}

func TestStorageOperationsError(t *testing.T) {
	c := baseConfig()
	c.Enabled = false // 绕过启用态校验，构造非法桶名以触发各方法错误分支。
	c.Storage = StorageConfig{Endpoint: "eos.example.cn", Bucket: ""}
	st, err := NewStorage(c)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	ctx := context.Background()
	if err := st.Put(ctx, "obj", strings.NewReader("x")); err == nil {
		t.Fatal("expected put error")
	}
	if _, err := st.Get(ctx, "obj"); err == nil {
		t.Fatal("expected get error")
	}
	if err := st.Delete(ctx, "obj"); err == nil {
		t.Fatal("expected delete error")
	}
	if _, err := st.SignURL(ctx, "obj", time.Minute); err == nil {
		t.Fatal("expected sign error")
	}
}
