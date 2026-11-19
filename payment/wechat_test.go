package payment

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
)

func TestMapWechatTradeState(t *testing.T) {
	cases := map[string]TradeState{
		"SUCCESS":    StateSuccess,
		"REFUND":     StateRefunding,
		"NOTPAY":     StatePending,
		"USERPAYING": StatePending,
		"CLOSED":     StateClosed,
		"REVOKED":    StateClosed,
		"PAYERROR":   StateFailed,
		"WEIRD":      StateUnknown,
	}
	for state, want := range cases {
		if got := mapWechatTradeState(state); got != want {
			t.Fatalf("mapWechatTradeState(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestMapWechatRefundState(t *testing.T) {
	cases := map[string]TradeState{
		"SUCCESS":    StateSuccess,
		"PROCESSING": StatePending,
		"CLOSED":     StateClosed,
		"ABNORMAL":   StateFailed,
		"OTHER":      StateUnknown,
	}
	for status, want := range cases {
		if got := mapWechatRefundState(status); got != want {
			t.Fatalf("mapWechatRefundState(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestWechatContentHelpers(t *testing.T) {
	var content map[string]interface{}
	if err := json.Unmarshal([]byte(`{
		"out_trade_no": "order-9",
		"trade_state": "SUCCESS",
		"success_time": "2026-11-19T10:00:00+08:00",
		"payer": {"openid": "oX1"},
		"amount": {"total": 888, "payer_total": 888}
	}`), &content); err != nil {
		t.Fatal(err)
	}
	if got := contentString(content, "out_trade_no"); got != "order-9" {
		t.Fatalf("out_trade_no = %q", got)
	}
	if got := contentNestedString(content, "payer", "openid"); got != "oX1" {
		t.Fatalf("openid = %q", got)
	}
	if got := contentNestedInt64(content, "amount", "total"); got != 888 {
		t.Fatalf("total = %d", got)
	}
	if got := contentNestedInt64(content, "missing", "total"); got != 0 {
		t.Fatalf("missing should be 0, got %d", got)
	}
	if got := contentNestedString(content, "missing", "openid"); got != "" {
		t.Fatalf("missing nested should be empty, got %q", got)
	}
}

func TestMapWechatTransaction(t *testing.T) {
	total := int64(1500)
	openid := "oABC"
	success := "2026-11-19T10:00:00+08:00"
	state := "SUCCESS"
	outTradeNo := "order-1"
	txnID := "42000"
	txn := &payments.Transaction{
		OutTradeNo:    &outTradeNo,
		TransactionId: &txnID,
		TradeState:    &state,
		SuccessTime:   &success,
		Amount:        &payments.TransactionAmount{Total: &total},
		Payer:         &payments.TransactionPayer{Openid: &openid},
	}
	trade := mapWechatTransaction(txn)
	if trade.State != StateSuccess || trade.Amount != 1500 || trade.Payer != "oABC" {
		t.Fatalf("mapped trade = %+v", trade)
	}
	if trade.SuccessTime.IsZero() {
		t.Fatal("success time not parsed")
	}
}

func TestMapWechatRefund(t *testing.T) {
	refundAmount := int64(500)
	outRefundNo := "rf-1"
	status := refunddomestic.Status("SUCCESS")
	refund := &refunddomestic.Refund{
		OutRefundNo: &outRefundNo,
		Status:      &status,
		Amount:      &refunddomestic.Amount{Refund: &refundAmount},
	}
	result := mapWechatRefund(refund)
	if result.State != StateSuccess || result.Amount != 500 || result.OutRefundNo != "rf-1" {
		t.Fatalf("mapped refund = %+v", result)
	}
}

func TestWechatPrepayValidation(t *testing.T) {
	w := &wechatChannel{
		cfg:     WechatConfig{AppID: "wx1", MchID: "190000"},
		timeout: time.Second,
	}
	ctx := context.Background()

	base := Order{OutTradeNo: "order-1", Amount: 1, Subject: "test", Scene: SceneNative, NotifyURL: "https://example.com/notify"}

	// 不支持的场景与缺失参数在触网前拦截。
	order := base
	order.Scene = ScenePage
	if _, err := w.Prepay(ctx, order); err == nil {
		t.Fatal("page scene must be rejected for wechat")
	}

	order = base
	order.Scene = SceneJSAPI
	if _, err := w.Prepay(ctx, order); err == nil {
		t.Fatal("jsapi without openid must be rejected")
	}

	order = base
	order.Scene = SceneH5
	if _, err := w.Prepay(ctx, order); err == nil {
		t.Fatal("h5 without client ip must be rejected")
	}

	order = base
	order.NotifyURL = ""
	if _, err := w.Prepay(ctx, order); err == nil {
		t.Fatal("missing notify url must be rejected")
	}

	order = base
	order.Amount = 0
	if _, err := w.Prepay(ctx, order); err == nil {
		t.Fatal("zero amount must be rejected")
	}
}

func TestWechatResolveNotifyURL(t *testing.T) {
	w := &wechatChannel{cfg: WechatConfig{NotifyURL: "https://default/notify"}}
	if got, err := w.resolveNotifyURL(""); err != nil || got != "https://default/notify" {
		t.Fatalf("fallback = %q, %v", got, err)
	}
	if got, err := w.resolveNotifyURL("https://per-order/notify"); err != nil || got != "https://per-order/notify" {
		t.Fatalf("override = %q, %v", got, err)
	}
	empty := &wechatChannel{}
	if _, err := empty.resolveNotifyURL(""); err == nil {
		t.Fatal("missing notify url must error")
	}
}
