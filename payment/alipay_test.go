package payment

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	alipay "github.com/smartwalle/alipay/v3"
	"github.com/smartwalle/nsign"
)

// generateAlipayKeys 生成测试用 RSA 密钥对（PKCS1 私钥 + PKIX 公钥 PEM）。
func generateAlipayKeys(t *testing.T) (privPEM, pubPEM string, priv *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privBlock := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubBlock := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	return string(privBlock), string(pubBlock), key
}

func newTestAlipayChannel(t *testing.T) *alipayChannel {
	t.Helper()
	privPEM, pubPEM, _ := generateAlipayKeys(t)
	channel, err := newAlipay(AlipayConfig{
		Enabled:    true,
		AppID:      "2021000000000000",
		PrivateKey: privPEM,
		PublicKey:  pubPEM,
		NotifyURL:  "https://example.com/payment/notify/alipay",
	}, 5*time.Second)
	if err != nil {
		t.Fatalf("newAlipay: %v", err)
	}
	return channel
}

func TestFenToYuan(t *testing.T) {
	cases := map[int64]string{
		0:       "0.00",
		1:       "0.01",
		99:      "0.99",
		100:     "1.00",
		12345:   "123.45",
		100000:  "1000.00",
		-250:    "-2.50",
	}
	for cents, want := range cases {
		if got := fenToYuan(cents); got != want {
			t.Fatalf("fenToYuan(%d) = %q, want %q", cents, got, want)
		}
	}
}

func TestYuanToFen(t *testing.T) {
	cases := map[string]int64{
		"0.01":    1,
		"1.00":    100,
		"123.45":  12345,
		"1000":    100000,
		"9.9":     990,
	}
	for yuan, want := range cases {
		got, err := yuanToFen(yuan)
		if err != nil || got != want {
			t.Fatalf("yuanToFen(%q) = %d, %v; want %d", yuan, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "1.2.3"} {
		if _, err := yuanToFen(bad); err == nil {
			t.Fatalf("yuanToFen(%q) must fail", bad)
		}
	}
}

func TestMapAlipayTradeState(t *testing.T) {
	cases := map[alipay.TradeStatus]TradeState{
		alipay.TradeStatusWaitBuyerPay: StatePending,
		alipay.TradeStatusSuccess:      StateSuccess,
		alipay.TradeStatusFinished:     StateSuccess,
		alipay.TradeStatusClosed:       StateClosed,
		alipay.TradeStatus("OTHER"):    StateUnknown,
	}
	for status, want := range cases {
		if got := mapAlipayTradeState(status); got != want {
			t.Fatalf("mapAlipayTradeState(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestAlipayPrepayPageAndApp(t *testing.T) {
	channel := newTestAlipayChannel(t)
	ctx := context.Background()

	// 电脑网站支付：纯本地签名生成跳转 URL。
	page, err := channel.Prepay(ctx, Order{
		OutTradeNo: "order-page-1", Amount: 8880, Subject: "测试商品",
		Scene: ScenePage, ReturnURL: "https://example.com/done",
	})
	if err != nil {
		t.Fatalf("page prepay: %v", err)
	}
	if !strings.Contains(page.Redirect, "sign=") || !strings.Contains(page.Redirect, "order-page-1") {
		t.Fatalf("page redirect = %q", page.Redirect)
	}
	if !strings.Contains(page.Redirect, "alipay.trade.page.pay") {
		t.Fatalf("page redirect missing api name: %q", page.Redirect)
	}

	// APP 支付：本地生成 orderStr。
	appResult, err := channel.Prepay(ctx, Order{
		OutTradeNo: "order-app-1", Amount: 100, Subject: "测试商品", Scene: SceneApp,
	})
	if err != nil {
		t.Fatalf("app prepay: %v", err)
	}
	if !strings.Contains(appResult.OrderStr, "alipay.trade.app.pay") || !strings.Contains(appResult.OrderStr, "sign=") {
		t.Fatalf("app orderStr = %q", appResult.OrderStr)
	}

	// 不支持的场景。
	if _, err := channel.Prepay(ctx, Order{
		OutTradeNo: "order-1", Amount: 100, Subject: "x", Scene: SceneJSAPI,
	}); err == nil {
		t.Fatal("jsapi scene must be rejected for alipay")
	}
}

// signAlipayNotification 按支付宝规则对回调表单做 RSA2 签名。
func signAlipayNotification(t *testing.T, priv *rsa.PrivateKey, values url.Values) string {
	t.Helper()
	signer := nsign.New(
		nsign.WithMethod(nsign.NewRSAMethod(crypto.SHA256, priv, nil)),
		nsign.WithEncoder(alipay.Encoder{}),
	)
	signature, err := signer.SignValues(values, nsign.WithIgnore("sign", "sign_type", "alipay_cert_sn"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return base64.StdEncoding.EncodeToString(signature)
}

func TestAlipayParseNotify(t *testing.T) {
	privPEM, pubPEM, priv := generateAlipayKeys(t)
	channel, err := newAlipay(AlipayConfig{
		Enabled: true, AppID: "2021000000000000", PrivateKey: privPEM, PublicKey: pubPEM,
	}, 5*time.Second)
	if err != nil {
		t.Fatalf("newAlipay: %v", err)
	}

	values := url.Values{}
	values.Set("notify_time", "2026-11-19 10:00:00")
	values.Set("notify_type", "trade_status_sync")
	values.Set("app_id", "2021000000000000")
	values.Set("trade_no", "2026111922001")
	values.Set("out_trade_no", "order-notify-1")
	values.Set("trade_status", "TRADE_SUCCESS")
	values.Set("total_amount", "12.34")
	values.Set("buyer_id", "2088000000000000")
	values.Set("gmt_payment", "2026-11-19 10:00:01")
	values.Set("subject", "测试商品")
	values.Set("sign_type", "RSA2")
	values.Set("sign", signAlipayNotification(t, priv, values))

	req := httptest.NewRequest(http.MethodPost, "/payment/notify/alipay",
		strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	result, err := channel.ParseNotify(context.Background(), req)
	if err != nil {
		t.Fatalf("ParseNotify: %v", err)
	}
	if result.Channel != ChannelAlipay || result.Type != NotifyPayment {
		t.Fatalf("channel/type = %v/%v", result.Channel, result.Type)
	}
	if result.OutTradeNo != "order-notify-1" || result.State != StateSuccess || result.Amount != 1234 {
		t.Fatalf("mapped result = %+v", result)
	}
	if result.IdempotencyKey != "order-notify-1:TRADE_SUCCESS" {
		t.Fatalf("idempotency key = %q", result.IdempotencyKey)
	}
	if result.SuccessTime.IsZero() {
		t.Fatal("success time not parsed")
	}

	// 篡改签名必须验签失败。
	values.Set("sign", "bm90LWEtdmFsaWQtc2lnbmF0dXJl")
	badReq := httptest.NewRequest(http.MethodPost, "/payment/notify/alipay",
		strings.NewReader(values.Encode()))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := channel.ParseNotify(context.Background(), badReq); err == nil {
		t.Fatal("invalid signature must fail verification")
	}
}
