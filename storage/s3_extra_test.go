package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// errTransport 让 minio 客户端的所有请求立即失败，避免任何真实网络访问。
type errTransport struct{ err error }

func (t errTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, t.err }

func TestMinioAPIAdapter(t *testing.T) {
	client, err := minio.New("127.0.0.1:1", &minio.Options{
		Creds:     credentials.NewStaticV4("key", "secret", ""),
		Transport: errTransport{err: errors.New("no network")},
	})
	if err != nil {
		t.Fatalf("minio.New: %v", err)
	}
	api := minioAPI{client: client}
	ctx := context.Background()

	if _, err := api.PutObject(ctx, "bucket", "a.txt", strings.NewReader("x"), -1, minio.PutObjectOptions{}); err == nil {
		t.Fatal("expected put error")
	}
	if _, err := api.StatObject(ctx, "bucket", "a.txt", minio.StatObjectOptions{}); err == nil {
		t.Fatal("expected stat error")
	}
	if err := api.RemoveObject(ctx, "bucket", "a.txt", minio.RemoveObjectOptions{}); err == nil {
		t.Fatal("expected remove error")
	}
	object, err := api.GetObject(ctx, "bucket", "a.txt", minio.GetObjectOptions{})
	if err == nil {
		_ = object.Close()
	}
	// PresignedGetObject 需要先解析 bucket region，替身传输会立即失败；
	// 这里只要求走通适配方法，不关心最终 URL。
	value, err := api.PresignedGetObject(ctx, "bucket", "a.txt", time.Minute, nil)
	if err == nil && !strings.Contains(value.String(), "a.txt") {
		t.Fatalf("url = %q", value.String())
	}
}

type fakeS3 struct {
	putErr     error
	getErr     error
	removeErr  error
	statErr    error
	presignErr error

	statInfo   minio.ObjectInfo
	presignURL *url.URL
	expires    time.Duration

	lastBucket string
	lastObject string
	putBody    string
	getBody    io.ReadCloser
}

func (f *fakeS3) PutObject(_ context.Context, bucket, object string, reader io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
	f.lastBucket = bucket
	f.lastObject = object
	if reader != nil {
		data, _ := io.ReadAll(reader)
		f.putBody = string(data)
	}
	return minio.UploadInfo{}, f.putErr
}

func (f *fakeS3) GetObject(_ context.Context, bucket, object string, _ minio.GetObjectOptions) (io.ReadCloser, error) {
	f.lastBucket = bucket
	f.lastObject = object
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getBody != nil {
		return f.getBody, nil
	}
	return io.NopCloser(strings.NewReader("data")), nil
}

func (f *fakeS3) RemoveObject(_ context.Context, bucket, object string, _ minio.RemoveObjectOptions) error {
	f.lastBucket = bucket
	f.lastObject = object
	return f.removeErr
}

func (f *fakeS3) StatObject(_ context.Context, bucket, object string, _ minio.StatObjectOptions) (minio.ObjectInfo, error) {
	f.lastBucket = bucket
	f.lastObject = object
	return f.statInfo, f.statErr
}

func (f *fakeS3) PresignedGetObject(_ context.Context, bucket, object string, expires time.Duration, _ url.Values) (*url.URL, error) {
	f.lastBucket = bucket
	f.lastObject = object
	f.expires = expires
	if f.presignErr != nil {
		return nil, f.presignErr
	}
	if f.presignURL != nil {
		return f.presignURL, nil
	}
	return url.Parse("https://s3.example.com/" + bucket + "/" + object)
}

func newFakeS3() (*S3Storage, *fakeS3) {
	fake := &fakeS3{}
	return &S3Storage{client: fake, bucket: "bucket"}, fake
}

func TestS3Put(t *testing.T) {
	store, fake := newFakeS3()
	if err := store.Put(context.Background(), "a/b.txt", strings.NewReader("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if fake.lastBucket != "bucket" || fake.lastObject != "a/b.txt" || fake.putBody != "hello" {
		t.Fatalf("recorded = %+v", fake)
	}

	if err := store.Put(context.Background(), "a.txt", nil); err == nil {
		t.Fatal("expected nil reader error")
	}
	if err := store.Put(context.Background(), "../evil", strings.NewReader("x")); err == nil {
		t.Fatal("expected invalid key error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Put(ctx, "a.txt", strings.NewReader("x")); err == nil {
		t.Fatal("expected canceled context error")
	}

	fake.putErr = errors.New("put failed")
	if err := store.Put(context.Background(), "a.txt", strings.NewReader("x")); err == nil || !strings.Contains(err.Error(), "put") {
		t.Fatalf("err = %v", err)
	}
}

func TestS3Get(t *testing.T) {
	store, fake := newFakeS3()
	reader, err := store.Get(context.Background(), "a/b.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	data, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(data) != "data" {
		t.Fatalf("data = %q", data)
	}
	if fake.lastObject != "a/b.txt" {
		t.Fatalf("object = %q", fake.lastObject)
	}

	if _, err := store.Get(context.Background(), "../evil"); err == nil {
		t.Fatal("expected invalid key error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Get(ctx, "a.txt"); err == nil {
		t.Fatal("expected canceled context error")
	}

	fake.getErr = errors.New("get failed")
	if _, err := store.Get(context.Background(), "a.txt"); err == nil {
		t.Fatal("expected get error")
	}
}

func TestS3Delete(t *testing.T) {
	store, fake := newFakeS3()
	if err := store.Delete(context.Background(), "a.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if fake.lastObject != "a.txt" {
		t.Fatalf("object = %q", fake.lastObject)
	}
	if err := store.Delete(context.Background(), "../evil"); err == nil {
		t.Fatal("expected invalid key error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Delete(ctx, "a.txt"); err == nil {
		t.Fatal("expected canceled context error")
	}
	fake.removeErr = errors.New("remove failed")
	if err := store.Delete(context.Background(), "a.txt"); err == nil {
		t.Fatal("expected remove error")
	}
}

func TestS3Exists(t *testing.T) {
	store, fake := newFakeS3()
	ok, err := store.Exists(context.Background(), "a.txt")
	if err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	if _, err := store.Exists(context.Background(), "../evil"); err == nil {
		t.Fatal("expected invalid key error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Exists(ctx, "a.txt"); err == nil {
		t.Fatal("expected canceled context error")
	}

	notFound := []error{
		minio.ErrorResponse{StatusCode: 404},
		minio.ErrorResponse{Code: "NoSuchKey"},
		minio.ErrorResponse{Code: "NoSuchObject"},
		minio.ErrorResponse{Code: "NotFound"},
	}
	for _, err := range notFound {
		fake.statErr = err
		ok, gotErr := store.Exists(context.Background(), "a.txt")
		if gotErr != nil || ok {
			t.Fatalf("Exists(%v) = %v, %v", err, ok, gotErr)
		}
	}

	fake.statErr = errors.New("stat failed")
	if _, err := store.Exists(context.Background(), "a.txt"); err == nil {
		t.Fatal("expected stat error")
	}
}

func TestS3GetURL(t *testing.T) {
	store, fake := newFakeS3()
	fake.presignURL = &url.URL{Scheme: "https", Host: "s3.example.com", Path: "/bucket/a.txt"}
	value, err := store.GetURL(context.Background(), "a.txt", time.Minute)
	if err != nil {
		t.Fatalf("GetURL: %v", err)
	}
	if value != "https://s3.example.com/bucket/a.txt" {
		t.Fatalf("url = %q", value)
	}
	if fake.expires != time.Minute {
		t.Fatalf("expires = %v", fake.expires)
	}

	if _, err := store.GetURL(context.Background(), "a.txt", 0); err != nil {
		t.Fatalf("GetURL default ttl: %v", err)
	}
	if fake.expires != defaultPresignTTL {
		t.Fatalf("default expires = %v", fake.expires)
	}

	if _, err := store.GetURL(context.Background(), "../evil", time.Minute); err == nil {
		t.Fatal("expected invalid key error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.GetURL(ctx, "a.txt", time.Minute); err == nil {
		t.Fatal("expected canceled context error")
	}
	fake.presignErr = errors.New("presign failed")
	if _, err := store.GetURL(context.Background(), "a.txt", time.Minute); err == nil {
		t.Fatal("expected presign error")
	}
}

func TestNewS3EndpointHandling(t *testing.T) {
	base := Config{
		Driver:    "s3",
		Bucket:    "files",
		AccessKey: "key",
		SecretKey: "secret",
	}
	base.Endpoint = "https://example.com:9000/"
	if _, err := NewS3(base); err != nil {
		t.Fatalf("https endpoint: %v", err)
	}
	base.Endpoint = "http://example.com:9000"
	if _, err := NewS3(base); err != nil {
		t.Fatalf("http endpoint: %v", err)
	}
	base.Endpoint = "http://"
	if _, err := NewS3(base); err == nil {
		t.Fatal("expected invalid endpoint error")
	}
	base.Endpoint = "bad endpoint"
	if _, err := NewS3(base); err == nil {
		t.Fatal("expected invalid endpoint error")
	}
}
