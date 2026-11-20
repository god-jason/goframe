package tencent

// 本文件通过 httptest 模拟腾讯云 VMS API 服务端，覆盖语音通知与统一接口适配路径。
// 说明：voice.go 中 vmsapi.NewClient 的错误返回分支逻辑上不可达
// （SDK 构造函数恒返回 nil error），属于防御性代码，无法在测试中触发。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/god-jason/goframe/cloud"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// voiceAt 启动模拟 VMS API 的 httptest 服务，返回端点指向它的语音客户端。
// cfgPlayTimes 用于验证播放次数回退逻辑。
func voiceAt(t *testing.T, cfgPlayTimes int, handler http.HandlerFunc) *Voice {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := baseConfig()
	c.Voice.Enabled = true
	c.Voice.SdkAppID = "1400000000"
	c.Voice.PlayTimes = cfgPlayTimes
	v, err := NewVoice(c)
	if err != nil {
		t.Fatalf("NewVoice: %v", err)
	}
	p := profile.NewClientProfile()
	p.HttpProfile.Endpoint = strings.TrimPrefix(srv.URL, "http://")
	p.HttpProfile.Scheme = "HTTP"
	v.client.WithProfile(p)
	return v
}

func ttsOK(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"Response":{"RequestId":"r-2"}}`))
}

func TestSendTtsVoiceOverHTTP(t *testing.T) {
	var gotBody map[string]any
	v := voiceAt(t, 2, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		ttsOK(w, r)
	})
	requestID, err := v.SendTtsVoice(context.Background(), "13800000000", "TPL1", 3)
	if err != nil {
		t.Fatalf("send tts: %v", err)
	}
	if requestID != "r-2" {
		t.Fatalf("requestID = %s", requestID)
	}
	if gotBody["VoiceSdkAppid"] != "1400000000" || gotBody["TemplateId"] != "TPL1" {
		t.Fatalf("body = %+v", gotBody)
	}
	if gotBody["PlayTimes"] != float64(3) {
		t.Fatalf("PlayTimes = %v", gotBody["PlayTimes"])
	}
}

func TestSendTtsVoicePlayTimesFallback(t *testing.T) {
	// 请求未指定播放次数时回退到配置值。
	var gotBody map[string]any
	v := voiceAt(t, 2, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		ttsOK(w, r)
	})
	if _, err := v.SendTtsVoice(context.Background(), "138", "TPL1", 0); err != nil {
		t.Fatalf("send tts: %v", err)
	}
	if gotBody["PlayTimes"] != float64(2) {
		t.Fatalf("cfg playTimes = %v", gotBody["PlayTimes"])
	}

	// 配置也为 0 时使用默认值 1。
	v2 := voiceAt(t, 0, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		ttsOK(w, r)
	})
	if _, err := v2.SendTtsVoice(context.Background(), "138", "TPL1", -1); err != nil {
		t.Fatalf("send tts: %v", err)
	}
	if gotBody["PlayTimes"] != float64(1) {
		t.Fatalf("default playTimes = %v", gotBody["PlayTimes"])
	}
}

func TestSendTtsVoiceErrors(t *testing.T) {
	// Response 缺失时报空响应错误。
	v := voiceAt(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	if _, err := v.SendTtsVoice(context.Background(), "138", "TPL1", 1); err == nil ||
		!strings.Contains(err.Error(), "empty response") {
		t.Fatalf("empty response: %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.SendTtsVoice(cancelled, "138", "TPL1", 1); err == nil {
		t.Fatal("expected ctx error")
	}

	bad := voiceAt(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"Response":{"Error":{"Code":"FailedOperation","Message":"bad","RequestId":"r"}}}`))
	})
	if _, err := bad.SendTtsVoice(context.Background(), "138", "TPL1", 1); err == nil {
		t.Fatal("expected sdk error")
	}
}

func TestPlayVoiceUnified(t *testing.T) {
	v := voiceAt(t, 0, ttsOK)

	if _, err := v.PlayVoice(context.Background(), nil); err == nil {
		t.Fatal("expected nil request error")
	}
	if _, err := v.PlayVoice(context.Background(), &cloud.VoiceRequest{Callee: "138"}); err == nil ||
		!strings.Contains(err.Error(), "template_id") {
		t.Fatalf("missing template: %v", err)
	}
	callID, err := v.PlayVoice(context.Background(), &cloud.VoiceRequest{
		Callee:     "13800000000",
		TemplateID: "TPL1",
		PlayTimes:  2,
	})
	if err != nil {
		t.Fatalf("play voice: %v", err)
	}
	if callID != "r-2" {
		t.Fatalf("callID = %s", callID)
	}

	bad := voiceAt(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"Response":{"Error":{"Code":"FailedOperation","Message":"bad"}}}`))
	})
	if _, err := bad.PlayVoice(context.Background(), &cloud.VoiceRequest{Callee: "138", TemplateID: "TPL1"}); err == nil {
		t.Fatal("expected failure")
	}
}
