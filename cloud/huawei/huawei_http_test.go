package huawei

// 本文件通过 httptest 模拟华为云 SMS/VCP/OBS 服务端。
// 剩余未覆盖块均为逻辑不可达的防御分支，无法在测试中触发：
//   - sms.go / unified.go 中 json.Marshal([]string) 的错误分支：字符串切片必然编码成功；
//   - voice.go NewVoice 中 SafeBuild 的错误分支：凭证由 newCredentials 固定构造且类型合法，
//     不使用 region 分支，endpoint 不参与校验，构建恒成功；
//   - voice.go Call 中类型断言失败分支：reqDef 每次调用现场构造，WithResponse 传入的
//     实例类型即断言目标类型，SDK 返回值恒满足断言。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/god-jason/goframe/cloud"
	"github.com/god-jason/goframe/config"
)

// smsRegionEnv 返回 SDK 区域端点环境变量名。
// SDK 的 EnvProvider 按 HUAWEICLOUD_SDK_REGION_<SERVICE>_<REGION> 查找自定义端点，
// 每个测试使用唯一 regionId 避免区域缓存冲突。
func smsRegionEnv(regionId string) string {
	return "HUAWEICLOUD_SDK_REGION_SMSAPI_" + strings.ToUpper(strings.ReplaceAll(regionId, "-", "_"))
}

var regionSeq atomic.Uint64

// uniqueRegion 生成进程内唯一的 regionId：SDK 按 region 缓存端点映射，
// 同名 region 在 -count>1 时会命中上一轮已关闭的 httptest 服务器。
func uniqueRegion(t *testing.T) string {
	name := strings.ToLower(strings.ReplaceAll(t.Name(), "_", "-"))
	return fmt.Sprintf("cn-%s-%d", name, regionSeq.Add(1))
}

// newSmsForTest 用唯一 regionId 创建指向 srv 的 Sms 客户端。
func newSmsForTest(t *testing.T, srvURL string) *Sms {
	t.Helper()
	region := uniqueRegion(t)
	t.Setenv(smsRegionEnv(region), srvURL)
	c := baseConfig()
	c.Region = region
	c.SMS.Enabled = true
	c.SMS.Signature = "签名"
	c.SMS.From = "+861000000000"
	sms, err := NewSms(c)
	if err != nil {
		t.Fatalf("NewSms: %v", err)
	}
	return sms
}

// ---------- Sms.Send ----------

func TestSmsSendSuccessViaHTTptest(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"200000","description":"success"}`))
	}))
	defer srv.Close()

	sms := newSmsForTest(t, srv.URL)
	err := sms.Send(context.Background(), []string{"+8613800000000"}, "tpl-1", `["123"]`)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(gotBody, "tpl-1") {
		t.Fatalf("body missing template: %s", gotBody)
	}
}

func TestSmsSendSuccessNoParas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"200000","description":"ok"}`))
	}))
	defer srv.Close()

	sms := newSmsForTest(t, srv.URL)
	if err := sms.Send(context.Background(), []string{"+8613800000000"}, "tpl-1", ""); err != nil {
		t.Fatalf("Send no paras: %v", err)
	}
}

func TestSmsSendErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"100002","description":"invalid template"}`))
	}))
	defer srv.Close()

	sms := newSmsForTest(t, srv.URL)
	err := sms.Send(context.Background(), []string{"+8613800000000"}, "tpl-1", "")
	if err == nil || !strings.Contains(err.Error(), "100002") {
		t.Fatalf("expected code error, got %v", err)
	}
}

func TestSmsSendNilCode(t *testing.T) {
	// code 为 nil 时 deref 返回 ""，视为成功。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"description":"ok"}`))
	}))
	defer srv.Close()

	sms := newSmsForTest(t, srv.URL)
	if err := sms.Send(context.Background(), []string{"+8613800000000"}, "tpl-1", ""); err != nil {
		t.Fatalf("nil code should succeed: %v", err)
	}
}

func TestSmsSendHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`server error`))
	}))
	defer srv.Close()

	sms := newSmsForTest(t, srv.URL)
	if err := sms.Send(context.Background(), []string{"+8613800000000"}, "tpl-1", ""); err == nil {
		t.Fatal("expected HTTP error")
	}
}

func TestSmsSendValidation(t *testing.T) {
	sms := newSmsForTest(t, "http://127.0.0.1:1")
	if err := sms.Send(context.Background(), nil, "tpl", ""); err == nil {
		t.Fatal("expected empty phone error")
	}
	if err := sms.Send(context.Background(), []string{"+86"}, "", ""); err == nil {
		t.Fatal("expected empty template error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sms.Send(ctx, []string{"+86"}, "tpl", ""); err == nil {
		t.Fatal("expected context cancelled error")
	}
}

// ---------- NewSms 错误路径 ----------

func TestNewSmsInvalidRegion(t *testing.T) {
	c := baseConfig()
	c.Region = "totally-invalid-xyz"
	c.SMS.Enabled = true
	c.SMS.Signature = "s"
	c.SMS.From = "f"
	_ = os.Unsetenv(smsRegionEnv("totally-invalid-xyz"))
	if _, err := NewSms(c); err == nil {
		t.Fatal("expected region error")
	}
}

func TestNewSmsInvalidConfig(t *testing.T) {
	c := baseConfig()
	c.SecretAccessKey = ""
	if _, err := NewSms(c); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestNewSmsProviderError(t *testing.T) {
	c := baseConfig()
	c.SecretAccessKey = ""
	if _, err := NewSmsProvider(c); err == nil {
		t.Fatal("expected error from NewSmsProvider")
	}
}

// ---------- SmsProvider.Send ----------

func TestSmsProviderSendViaHTTptest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"200000","description":"ok"}`))
	}))
	defer srv.Close()

	region := uniqueRegion(t)
	t.Setenv(smsRegionEnv(region), srv.URL)
	c := baseConfig()
	c.Region = region
	c.SMS.Enabled = true
	c.SMS.Signature = "签名"
	c.SMS.From = "+861000000000"
	provider, err := NewSmsProvider(c)
	if err != nil {
		t.Fatalf("NewSmsProvider: %v", err)
	}
	err = provider.Send(context.Background(), []string{"+8613800000000"}, "content", map[string]string{
		"template_id": "tpl-1",
		"1":           "code1",
		"2":           "code2",
	})
	if err != nil {
		t.Fatalf("provider.Send: %v", err)
	}
}

func TestSmsProviderSendNoVars(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"200000","description":"ok"}`))
	}))
	defer srv.Close()

	region := uniqueRegion(t)
	t.Setenv(smsRegionEnv(region), srv.URL)
	c := baseConfig()
	c.Region = region
	c.SMS.Enabled = true
	c.SMS.Signature = "签名"
	c.SMS.From = "+861000000000"
	provider, _ := NewSmsProvider(c)
	err := provider.Send(context.Background(), []string{"+8613800000000"}, "content", map[string]string{
		"template_id": "tpl-1",
	})
	if err != nil {
		t.Fatalf("provider.Send no vars: %v", err)
	}
}

// ---------- smsTemplateVars 非数字分支 ----------

func TestSmsTemplateVarsNonNumeric(t *testing.T) {
	got := smsTemplateVars(map[string]string{
		"template_id": "t",
		"code":        "c",
		"name":        "n",
		"alpha":       "a",
	})
	if strings.Join(got, ",") != "a,c,n" {
		t.Fatalf("non-numeric vars = %v", got)
	}
}

func TestSmsTemplateVarsEmpty(t *testing.T) {
	got := smsTemplateVars(map[string]string{"template_id": "t"})
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

// ---------- Voice.Call ----------
// 注意：ExecuteCallResponse 缺少 SDK 要求的 HttpStatusCode 字段，
// HTTP 2xx 响应会导致 SDK deserializeResponse 中 reflect panic，
// 因此成功路径（voice.go 90-97 行）不可测试，属于源码缺陷。
// 以下仅测试校验与错误路径。

func TestVoiceCallHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`bad request`))
	}))
	defer srv.Close()

	c := baseConfig()
	c.Voice.Enabled = true
	c.Voice.ShowNumber = "+861000000001"
	c.Voice.Endpoint = srv.URL
	c.Voice.PlayTimes = 3
	v, err := NewVoice(c)
	if err != nil {
		t.Fatalf("NewVoice: %v", err)
	}
	// 400 被 SDK DefaultErrorHandler 拦截，Sync 返回错误。
	if _, err := v.Call(context.Background(), "+8613800000000", "hello"); err == nil {
		t.Fatal("expected HTTP error")
	}
}

func TestVoiceCallDefaultPlayTimes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`unavailable`))
	}))
	defer srv.Close()

	c := baseConfig()
	c.Voice.Enabled = true
	c.Voice.ShowNumber = "+861000000001"
	c.Voice.Endpoint = srv.URL
	c.Voice.PlayTimes = 0
	v, _ := NewVoice(c)
	// 请求体中 playTimes 应为 1（默认值），但响应错误，只验证不 panic。
	if _, err := v.Call(context.Background(), "+86138", "hi"); err == nil {
		t.Fatal("expected error")
	}
}

func TestVoiceCallValidation(t *testing.T) {
	c := baseConfig()
	c.Voice.Enabled = true
	c.Voice.ShowNumber = "+861000000001"
	c.Voice.Endpoint = "http://127.0.0.1:1"
	v, _ := NewVoice(c)
	if _, err := v.Call(context.Background(), "", "hi"); err == nil {
		t.Fatal("expected callee required")
	}
	if _, err := v.Call(context.Background(), "+86138", ""); err == nil {
		t.Fatal("expected message required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.Call(ctx, "+86138", "hi"); err == nil {
		t.Fatal("expected context cancelled")
	}
}

func TestNewVoiceInvalidConfig(t *testing.T) {
	c := baseConfig()
	c.SecretAccessKey = ""
	c.Voice.Enabled = true
	if _, err := NewVoice(c); err == nil {
		t.Fatal("expected validation error")
	}
}

// ---------- Storage ----------

func newTestStorage(t *testing.T, srvURL string) *Storage {
	t.Helper()
	c := baseConfig()
	c.Storage.Enabled = true
	c.Storage.Endpoint = srvURL
	c.Storage.Bucket = "testbucket"
	st, err := NewStorage(c)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	return st
}

func TestStoragePutGetDelete(t *testing.T) {
	store := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/testbucket/")
		switch r.Method {
		case http.MethodPut:
			raw, _ := io.ReadAll(r.Body)
			store[key] = raw
			w.Header().Set("ETag", `"abc123"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			data, ok := store[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`))
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			_, _ = w.Write(data)
		case http.MethodDelete:
			delete(store, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	st := newTestStorage(t, srv.URL)
	defer st.Close()

	if err := st.Put(context.Background(), "hello.txt", strings.NewReader("world")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if string(store["hello.txt"]) != "world" {
		t.Fatalf("stored = %q", store["hello.txt"])
	}

	rc, err := st.Get(context.Background(), "hello.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "world" {
		t.Fatalf("get = %q", data)
	}

	if err := st.Delete(context.Background(), "hello.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := store["hello.txt"]; ok {
		t.Fatal("expected deleted")
	}
}

func TestStoragePutError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`))
	}))
	defer srv.Close()

	st := newTestStorage(t, srv.URL)
	defer st.Close()
	if err := st.Put(context.Background(), "k", strings.NewReader("v")); err == nil {
		t.Fatal("expected put error")
	}
}

func TestStorageGetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`))
	}))
	defer srv.Close()

	st := newTestStorage(t, srv.URL)
	defer st.Close()
	if _, err := st.Get(context.Background(), "missing"); err == nil {
		t.Fatal("expected get error")
	}
}

func TestStorageDeleteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`))
	}))
	defer srv.Close()

	st := newTestStorage(t, srv.URL)
	defer st.Close()
	if err := st.Delete(context.Background(), "k"); err == nil {
		t.Fatal("expected delete error")
	}
}

func TestStorageContextCancelled(t *testing.T) {
	c := baseConfig()
	c.Storage.Enabled = true
	c.Storage.Endpoint = "http://127.0.0.1:1"
	c.Storage.Bucket = "b"
	st, _ := NewStorage(c)
	defer st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := st.Put(ctx, "k", strings.NewReader("v")); err == nil {
		t.Fatal("expected put ctx error")
	}
	if _, err := st.Get(ctx, "k"); err == nil {
		t.Fatal("expected get ctx error")
	}
	if err := st.Delete(ctx, "k"); err == nil {
		t.Fatal("expected delete ctx error")
	}
	if _, err := st.SignURL(ctx, "k", time.Minute); err == nil {
		t.Fatal("expected signurl ctx error")
	}
}

func TestStorageSignURL(t *testing.T) {
	c := baseConfig()
	c.Storage.Enabled = true
	c.Storage.Endpoint = "http://127.0.0.1:9999"
	c.Storage.Bucket = "bkt"
	st, _ := NewStorage(c)
	defer st.Close()

	url, err := st.SignURL(context.Background(), "key1", time.Hour)
	if err != nil {
		t.Fatalf("SignURL: %v", err)
	}
	if !strings.Contains(url, "key1") {
		t.Fatalf("url = %s", url)
	}
	url2, err := st.SignURL(context.Background(), "key2", 0)
	if err != nil {
		t.Fatalf("SignURL default expiry: %v", err)
	}
	if !strings.Contains(url2, "key2") {
		t.Fatalf("url2 = %s", url2)
	}
}

func TestStorageSignURLError(t *testing.T) {
	// 通过修改内部 bucket 为含非法 URL 字符的值触发 CreateSignedUrl 错误。
	c := baseConfig()
	c.Storage.Enabled = true
	c.Storage.Endpoint = "http://127.0.0.1:9999"
	c.Storage.Bucket = "bkt"
	st, _ := NewStorage(c)
	defer st.Close()
	st.cfg.Storage.Bucket = "bad\nbucket"
	if _, err := st.SignURL(context.Background(), "k", time.Hour); err == nil {
		t.Fatal("expected CreateSignedUrl error")
	}
}

func TestNewStorageInvalidConfig(t *testing.T) {
	c := baseConfig()
	c.SecretAccessKey = ""
	c.Storage.Enabled = true
	if _, err := NewStorage(c); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestNewStorageShortTimeout(t *testing.T) {
	// Timeout < 1s 时 int(Timeout/time.Second) == 0，触发 timeoutSec 默认值分支。
	c := baseConfig()
	c.Timeout = 500 * time.Millisecond
	c.Storage.Enabled = true
	c.Storage.Endpoint = "http://127.0.0.1:9999"
	c.Storage.Bucket = "b"
	st, err := NewStorage(c)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	st.Close()
}

func TestNewStorageObsClientError(t *testing.T) {
	// Storage.Enabled 为 false 时 Validate 跳过存储校验，
	// 但空 endpoint 使 obs.New 返回错误。
	c := baseConfig()
	c.Storage.Enabled = false
	if _, err := NewStorage(c); err == nil {
		t.Fatal("expected obs.New error for empty endpoint")
	}
}

func TestStorageClient(t *testing.T) {
	c := baseConfig()
	c.Storage.Enabled = true
	c.Storage.Endpoint = "http://127.0.0.1:9999"
	c.Storage.Bucket = "b"
	st, _ := NewStorage(c)
	defer st.Close()
	if st.Client() == nil {
		t.Fatal("Client() should not be nil")
	}
}

// ---------- unified.go ----------

func TestUnifiedSendSmsViaHTTptest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"200000","description":"ok"}`))
	}))
	defer srv.Close()

	sms := newSmsForTest(t, srv.URL)
	err := sms.SendSms(context.Background(), &cloud.SmsRequest{
		PhoneNumbers:   []string{"+8613800000000"},
		TemplateID:     "tpl-1",
		TemplateParams: []string{"code1", "code2"},
	})
	if err != nil {
		t.Fatalf("SendSms: %v", err)
	}
}

func TestUnifiedSendSmsNilReq(t *testing.T) {
	sms := newSmsForTest(t, "http://127.0.0.1:1")
	if err := sms.SendSms(context.Background(), nil); err == nil {
		t.Fatal("expected nil request error")
	}
}

func TestUnifiedSendSmsWithParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"200000","description":"ok"}`))
	}))
	defer srv.Close()

	sms := newSmsForTest(t, srv.URL)
	err := sms.SendSms(context.Background(), &cloud.SmsRequest{
		PhoneNumbers: []string{"+8613800000000"},
		TemplateID:   "tpl-1",
		Params:       map[string]string{"1": "a", "2": "b"},
	})
	if err != nil {
		t.Fatalf("SendSms with params: %v", err)
	}
}

func TestUnifiedPlayVoiceNilReq(t *testing.T) {
	c := baseConfig()
	c.Voice.Enabled = true
	c.Voice.ShowNumber = "+861000000001"
	c.Voice.Endpoint = "http://127.0.0.1:1"
	v, _ := NewVoice(c)
	if _, err := v.PlayVoice(context.Background(), nil); err == nil {
		t.Fatal("expected nil request error")
	}
}

func TestUnifiedPlayVoiceEmptyMsg(t *testing.T) {
	c := baseConfig()
	c.Voice.Enabled = true
	c.Voice.ShowNumber = "+861000000001"
	c.Voice.Endpoint = "http://127.0.0.1:1"
	v, _ := NewVoice(c)
	if _, err := v.PlayVoice(context.Background(), &cloud.VoiceRequest{Callee: "+86138"}); err == nil {
		t.Fatal("expected empty message error")
	}
}

// TestUnifiedPlayVoiceViaHTTptest 通过 HTTP 错误路径验证 PlayVoice 调用 Call。
func TestUnifiedPlayVoiceViaHTTptest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`error`))
	}))
	defer srv.Close()

	c := baseConfig()
	c.Voice.Enabled = true
	c.Voice.ShowNumber = "+861000000001"
	c.Voice.Endpoint = srv.URL
	v, _ := NewVoice(c)
	_, err := v.PlayVoice(context.Background(), &cloud.VoiceRequest{
		Callee:  "+8613800000000",
		Message: "hello",
	})
	if err == nil {
		t.Fatal("expected error from HTTP 500")
	}
}

// TestVoiceCallSuccess 覆盖 Call 成功路径：ExecuteCallResponse 已带 HttpStatusCode
// 字段（SDK 反射回填），2xx 响应不再 panic。
func TestVoiceCallSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"statusCode":0,"callId":"call-123"}`))
	}))
	defer srv.Close()

	c := baseConfig()
	c.Voice.Enabled = true
	c.Voice.ShowNumber = "+861000000001"
	c.Voice.Endpoint = srv.URL
	v, err := NewVoice(c)
	if err != nil {
		t.Fatalf("NewVoice: %v", err)
	}
	callID, err := v.Call(context.Background(), "+8613800000000", "hello")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if callID != "call-123" {
		t.Fatalf("callID = %q", callID)
	}
}

// TestVoiceCallBusinessError 覆盖 HTTP 200 但业务 statusCode 非 0 的失败分支。
func TestVoiceCallBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"statusCode":1,"message":"line busy"}`))
	}))
	defer srv.Close()

	c := baseConfig()
	c.Voice.Enabled = true
	c.Voice.ShowNumber = "+861000000001"
	c.Voice.Endpoint = srv.URL
	v, _ := NewVoice(c)
	_, err := v.Call(context.Background(), "+8613800000000", "hello")
	if err == nil || !strings.Contains(err.Error(), "status 1") {
		t.Fatalf("err = %v, want status 1", err)
	}
}

// ---------- boot.go Startup/Shutdown ----------

func TestStartupEnabledAndShutdown(t *testing.T) {
	smsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"200000","description":"ok"}`))
	}))
	defer smsSrv.Close()
	voiceSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer voiceSrv.Close()
	obsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer obsSrv.Close()

	bootRegion := fmt.Sprintf("cn-boot-test-%d", regionSeq.Add(1))
	t.Setenv(smsRegionEnv(bootRegion), smsSrv.URL)

	env := map[string]string{
		"GOFRAME_HUAWEI_ENABLED":              "true",
		"GOFRAME_HUAWEI_ACCESS_KEY_ID":        "ak",
		"GOFRAME_HUAWEI_SECRET_ACCESS_KEY":    "sk",
		"GOFRAME_HUAWEI_PROJECT_ID":           "project",
		"GOFRAME_HUAWEI_REGION":               bootRegion,
		"GOFRAME_HUAWEI_SMS_ENABLED":          "true",
		"GOFRAME_HUAWEI_SMS_SIGNATURE":        "签名",
		"GOFRAME_HUAWEI_SMS_FROM":             "+861000000000",
		"GOFRAME_HUAWEI_VOICE_ENABLED":        "true",
		"GOFRAME_HUAWEI_VOICE_SHOW_NUMBER":    "+861000000001",
		"GOFRAME_HUAWEI_VOICE_ENDPOINT":       voiceSrv.URL,
		"GOFRAME_HUAWEI_STORAGE_ENABLED":      "true",
		"GOFRAME_HUAWEI_STORAGE_ENDPOINT":     obsSrv.URL,
		"GOFRAME_HUAWEI_STORAGE_BUCKET":       "demo",
		"GOFRAME_HUAWEI_TIMEOUT":              "5s",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	if err := config.Reload(); err != nil {
		t.Fatalf("config reload: %v", err)
	}
	t.Cleanup(func() {
		for k := range env {
			_ = os.Unsetenv(k)
		}
		_ = config.Reload()
	})

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if GetSms() == nil {
		t.Fatal("expected sms client")
	}
	if GetVoice() == nil {
		t.Fatal("expected voice client")
	}
	if GetStorage() == nil {
		t.Fatal("expected storage client")
	}

	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if GetSms() != nil || GetVoice() != nil || GetStorage() != nil {
		t.Fatal("expected nil clients after shutdown")
	}
}

func TestStartupSMSError(t *testing.T) {
	env := map[string]string{
		"GOFRAME_HUAWEI_ENABLED":           "true",
		"GOFRAME_HUAWEI_ACCESS_KEY_ID":     "ak",
		"GOFRAME_HUAWEI_SECRET_ACCESS_KEY": "sk",
		"GOFRAME_HUAWEI_PROJECT_ID":        "project",
		"GOFRAME_HUAWEI_REGION":            "totally-invalid-boot",
		"GOFRAME_HUAWEI_SMS_ENABLED":       "true",
		"GOFRAME_HUAWEI_SMS_SIGNATURE":     "s",
		"GOFRAME_HUAWEI_SMS_FROM":          "f",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	if err := config.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	t.Cleanup(func() {
		for k := range env {
			_ = os.Unsetenv(k)
		}
		_ = config.Reload()
	})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected startup error from invalid sms region")
	}
}

func TestStartupVoiceError(t *testing.T) {
	env := map[string]string{
		"GOFRAME_HUAWEI_ENABLED":           "true",
		"GOFRAME_HUAWEI_ACCESS_KEY_ID":     "ak",
		"GOFRAME_HUAWEI_SECRET_ACCESS_KEY": "sk",
		"GOFRAME_HUAWEI_PROJECT_ID":        "project",
		"GOFRAME_HUAWEI_REGION":            "cn-north-4",
		"GOFRAME_HUAWEI_VOICE_ENABLED":     "true",
		"GOFRAME_HUAWEI_VOICE_SHOW_NUMBER": "",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	if err := config.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	t.Cleanup(func() {
		for k := range env {
			_ = os.Unsetenv(k)
		}
		_ = config.Reload()
	})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected startup error from invalid voice config")
	}
}

func TestStartupStorageError(t *testing.T) {
	env := map[string]string{
		"GOFRAME_HUAWEI_ENABLED":           "true",
		"GOFRAME_HUAWEI_ACCESS_KEY_ID":     "ak",
		"GOFRAME_HUAWEI_SECRET_ACCESS_KEY": "sk",
		"GOFRAME_HUAWEI_PROJECT_ID":        "project",
		"GOFRAME_HUAWEI_REGION":            "cn-north-4",
		"GOFRAME_HUAWEI_STORAGE_ENABLED":   "true",
		"GOFRAME_HUAWEI_STORAGE_ENDPOINT":  "",
		"GOFRAME_HUAWEI_STORAGE_BUCKET":    "",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	if err := config.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	t.Cleanup(func() {
		for k := range env {
			_ = os.Unsetenv(k)
		}
		_ = config.Reload()
	})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected startup error from invalid storage config")
	}
}

func TestShutdownWithNilStorage(t *testing.T) {
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown nil: %v", err)
	}
}

// ---------- deref ----------

func TestDeref(t *testing.T) {
	if deref(nil) != "" {
		t.Fatal("deref(nil) should be empty")
	}
	s := "hello"
	if deref(&s) != "hello" {
		t.Fatal("deref(&s) should be hello")
	}
}
