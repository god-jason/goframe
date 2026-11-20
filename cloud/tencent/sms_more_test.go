package tencent

// 本文件补齐 sms.go 中 deref 的空指针分支：
// 响应缺少 RequestId 时 deref(nil) 返回空字符串。

import (
	"context"
	"net/http"
	"testing"
)

func TestSmsSendNilRequestID(t *testing.T) {
	sms := smsAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Response":{"SendStatusSet":[{"Code":"Ok","Message":"success"}]}}`))
	})
	requestID, err := sms.Send(context.Background(), []string{"+86138"}, "签名", "ID1", nil)
	if err != nil || requestID != "" {
		t.Fatalf("nil request id: requestID=%q err=%v", requestID, err)
	}
}
