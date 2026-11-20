package aliyun

// 本文件通过 httptest 模拟 dyvmsapi 服务端，覆盖语音单呼与统一接口适配路径。
// 说明：voice.go 中 json.Marshal(map[string]string) 的错误分支逻辑上不可达，
// 属于防御性代码，无法在测试中触发。
// NewVoice 中 dyvmsapi.NewClient 的错误分支同样不可达：
// SDK 仅在 AK/SK 同时非空时构造静态凭证（此时校验必过），凭证类型固定合法，
// 且 Endpoint 恒为非空，Init 不会返回错误。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/god-jason/goframe/cloud"
)

// voiceAt 启动模拟 dyvmsapi 的 httptest 服务，返回端点指向它的语音客户端与最近一次请求的查询参数。
// SDK 默认使用 HTTPS，而测试服务是 HTTP，故构造后覆盖 client.Protocol。
func voiceAt(t *testing.T, handler http.HandlerFunc) (*Voice, *url.Values) {
	t.Helper()
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	c := baseConfig()
	c.Voice.Enabled = true
	c.Voice.CalledShowNumber = "057100000000"
	c.Voice.Endpoint = strings.TrimPrefix(srv.URL, "http://")
	v, err := NewVoice(c)
	if err != nil {
		t.Fatalf("NewVoice: %v", err)
	}
	v.client.Protocol = tea.String("HTTP")
	return v, &query
}

func TestSingleCallByTtsOverHTTP(t *testing.T) {
	v, query := voiceAt(t, respondJSON(`{"Code":"OK","Message":"OK","RequestId":"req-1","CallId":"call-1"}`))
	callID, err := v.SingleCallByTts(context.Background(), "13800000000", "TTS_1", map[string]string{"code": "8888"})
	if err != nil {
		t.Fatalf("tts call: %v", err)
	}
	if callID != "call-1" {
		t.Fatalf("callID = %s", callID)
	}
	if got := (*query).Get("CalledShowNumber"); got != "057100000000" {
		t.Fatalf("CalledShowNumber = %s", got)
	}
	if got := (*query).Get("TtsCode"); got != "TTS_1" {
		t.Fatalf("TtsCode = %s", got)
	}
	if got := (*query).Get("TtsParam"); !strings.Contains(got, "8888") {
		t.Fatalf("TtsParam = %s", got)
	}
}

func TestSingleCallByTtsErrors(t *testing.T) {
	v, _ := voiceAt(t, respondJSON(`{"Code":"isv.CALLED_NUMBER_DAILY_LIMIT","Message":"次数限制"}`))
	if _, err := v.SingleCallByTts(context.Background(), "138", "TTS_1", nil); err == nil ||
		!strings.Contains(err.Error(), "isv.CALLED_NUMBER_DAILY_LIMIT") {
		t.Fatalf("non-OK code: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.SingleCallByTts(cancelled, "138", "TTS_1", nil); err == nil {
		t.Fatal("expected ctx error")
	}
	bad, _ := voiceAt(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if _, err := bad.SingleCallByTts(context.Background(), "138", "TTS_1", nil); err == nil {
		t.Fatal("expected transport error")
	}
}

func TestSingleCallByVoiceOverHTTP(t *testing.T) {
	v, query := voiceAt(t, respondJSON(`{"Code":"OK","Message":"OK","RequestId":"req-2","CallId":"call-2"}`))
	callID, err := v.SingleCallByVoice(context.Background(), "13800000000", "VOICE_1")
	if err != nil {
		t.Fatalf("voice call: %v", err)
	}
	if callID != "call-2" {
		t.Fatalf("callID = %s", callID)
	}
	if got := (*query).Get("VoiceCode"); got != "VOICE_1" {
		t.Fatalf("VoiceCode = %s", got)
	}
}

func TestSingleCallByVoiceErrors(t *testing.T) {
	v, _ := voiceAt(t, respondJSON(`{"Code":"isv.VOICE_FILE_ILLEGAL","Message":"文件不合法"}`))
	if _, err := v.SingleCallByVoice(context.Background(), "138", "VOICE_1"); err == nil ||
		!strings.Contains(err.Error(), "isv.VOICE_FILE_ILLEGAL") {
		t.Fatalf("non-OK code: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.SingleCallByVoice(cancelled, "138", "VOICE_1"); err == nil {
		t.Fatal("expected ctx error")
	}
	bad, _ := voiceAt(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if _, err := bad.SingleCallByVoice(context.Background(), "138", "VOICE_1"); err == nil {
		t.Fatal("expected transport error")
	}
}

func TestPlayVoiceUnified(t *testing.T) {
	v, query := voiceAt(t, respondJSON(`{"Code":"OK","Message":"OK","RequestId":"req-1","CallId":"call-9"}`))

	if _, err := v.PlayVoice(context.Background(), nil); err == nil {
		t.Fatal("expected nil request error")
	}
	if _, err := v.PlayVoice(context.Background(), &cloud.VoiceRequest{Callee: "138"}); err == nil ||
		!strings.Contains(err.Error(), "template_id") {
		t.Fatalf("missing template: %v", err)
	}
	callID, err := v.PlayVoice(context.Background(), &cloud.VoiceRequest{
		Callee:     "13800000000",
		TemplateID: "TTS_1",
		Params:     map[string]string{"code": "1234"},
	})
	if err != nil {
		t.Fatalf("play voice: %v", err)
	}
	if callID != "call-9" {
		t.Fatalf("callID = %s", callID)
	}
	if (*query).Get("TtsCode") != "TTS_1" || (*query).Get("CalledNumber") != "13800000000" {
		t.Fatalf("query = %v", *query)
	}
	if !strings.Contains((*query).Get("TtsParam"), "1234") {
		t.Fatalf("TtsParam = %s", (*query).Get("TtsParam"))
	}

	// 失败路径。
	bad, _ := voiceAt(t, respondJSON(`{"Code":"isv.ACCOUNT_NOT_EXISTS","Message":"账号不存在"}`))
	if _, err := bad.PlayVoice(context.Background(), &cloud.VoiceRequest{Callee: "138", TemplateID: "TTS_1"}); err == nil {
		t.Fatal("expected failure")
	}
}
