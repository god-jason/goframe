//go:build windows

package upload

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/gin-gonic/gin"
	"golang.org/x/sys/windows"
)

// TestHandlerOpenUploadedFileError 覆盖 fileHeader.Open 失败分支（http.go:50）。
//
// 技巧：创建一个单实例命名管道并把 FileHeader 的内部 tmpfile 指向它。
// gin 的 c.FormFile 内部会预打开一次（客户端 CreateFile 连上管道成功），
// 随后服务端 ConnectNamedPipe 返回并 CloseHandle 销毁实例；
// handler 再次 Open 时管道实例已耗尽，os.Open 返回 ERROR_FILE_NOT_FOUND，
// 从而命中 "open uploaded file failed" 分支。
func TestHandlerOpenUploadedFileError(t *testing.T) {
	typ := reflect.TypeOf(multipart.FileHeader{})
	tmpField, ok := typ.FieldByName("tmpfile")
	if !ok {
		t.Fatal("multipart.FileHeader 缺少 tmpfile 字段")
	}
	contentField, ok := typ.FieldByName("content")
	if !ok {
		t.Fatal("multipart.FileHeader 缺少 content 字段")
	}
	sharedField, ok := typ.FieldByName("tmpshared")
	if !ok {
		t.Fatal("multipart.FileHeader 缺少 tmpshared 字段")
	}
	tmpOff, contentOff, sharedOff := tmpField.Offset, contentField.Offset, sharedField.Offset

	service, _ := newTestService(t, testUploadConfig())
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/upload", service.Handler(HTTPOptions{}))

	var lastBody string
	for attempt := 0; attempt < 5; attempt++ {
		pipePath := fmt.Sprintf(`\\.\pipe\goframe-upload-open-%d-%d`, os.Getpid(), attempt+time.Now().Nanosecond())
		name, err := windows.UTF16PtrFromString(pipePath)
		if err != nil {
			t.Fatalf("pipe name: %v", err)
		}
		h, err := windows.CreateNamedPipe(name,
			windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
			1, 64, 64, 0, nil)
		if err != nil {
			t.Fatalf("CreateNamedPipe: %v", err)
		}
		go func() {
			_ = windows.ConnectNamedPipe(h, nil)
			_ = windows.CloseHandle(h)
		}()

		// 构造真实 FileHeader 后用 unsafe 改写非导出字段，使其指向管道。
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, err := writer.CreateFormFile("file", "x.txt")
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write([]byte("hello")); err != nil {
			t.Fatalf("write part: %v", err)
		}
		_ = writer.Close()
		reader := multipart.NewReader(body, writer.Boundary())
		form, err := reader.ReadForm(1 << 20)
		if err != nil {
			t.Fatalf("read form: %v", err)
		}
		fh := form.File["file"][0]
		base := unsafe.Pointer(fh)
		*(*string)(unsafe.Pointer(uintptr(base) + tmpOff)) = pipePath
		*(*bool)(unsafe.Pointer(uintptr(base) + sharedOff)) = false
		*(*[]byte)(unsafe.Pointer(uintptr(base) + contentOff)) = nil

		req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(""))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=xx")
		req.MultipartForm = &multipart.Form{
			Value: map[string][]string{},
			File:  map[string][]*multipart.FileHeader{"file": {fh}},
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)

		lastBody = recorder.Body.String()
		if strings.Contains(lastBody, "open uploaded file failed") {
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", recorder.Code, lastBody)
			}
			return
		}
	}
	t.Fatalf("重试后仍未命中 open uploaded file failed 分支, last body = %s", lastBody)
}
