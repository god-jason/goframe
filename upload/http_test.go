package upload

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
	"github.com/god-jason/goframe/response"
)

func TestHandlerUploadsFile(t *testing.T) {
	service, _ := newTestService(t, testUploadConfig())

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(auth.WithIdentity(c.Request.Context(), &auth.Identity{
			UserID:   "user-1",
			TenantID: "tenant-1",
		}))
		c.Next()
	})
	engine.POST("/upload", service.Handler(HTTPOptions{}))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("category", "docs")
	part, err := writer.CreateFormFile("file", "readme.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = part.Write([]byte("hello"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var result response.Response[Result]
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Data.Key == "" || !strings.HasPrefix(result.Data.Key, "tenant-1/docs/") {
		t.Fatalf("unexpected key: %+v", result.Data)
	}
	if result.Data.Name != "readme.txt" || result.Data.Size != 5 {
		t.Fatalf("unexpected result: %+v", result.Data)
	}
}

func TestHandlerRejectsInvalidUpload(t *testing.T) {
	service, _ := newTestService(t, testUploadConfig())

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/upload", service.Handler(HTTPOptions{Field: "file"}))

	// 缺少文件
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/upload", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing file status = %d", recorder.Code)
	}

	// 非法扩展名
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "virus.exe")
	_, _ = part.Write([]byte("x"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid extension status = %d", recorder.Code)
	}
}
