package payment

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

// rewriteTransport 把发往微信/支付宝网关的请求重写到本地 httptest 服务器，
// 避免真实网络；orig 为安装覆盖前保存的真实传输层。
type rewriteTransport struct {
	target *httptest.Server
	orig   http.RoundTripper
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = rt.target.Listener.Addr().String()
	clone.Host = ""
	return rt.orig.RoundTrip(clone)
}

// wechatTestMaterial 是测试内自生成的商户/平台密钥与证书材料。
type wechatTestMaterial struct {
	merchantKey *rsa.PrivateKey
	merchantPEM string
	platformKey *rsa.PrivateKey
	certPEM     string
	serialNo    string
	apiV3Key    string
}

func newWechatTestMaterial(t *testing.T) *wechatTestMaterial {
	t.Helper()
	merchantKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	merchantDER, err := x509.MarshalPKCS8PrivateKey(merchantKey)
	if err != nil {
		t.Fatal(err)
	}
	platformKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "goframe-test-platform-cert"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &platformKey.PublicKey, platformKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &wechatTestMaterial{
		merchantKey: merchantKey,
		merchantPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: merchantDER})),
		platformKey: platformKey,
		certPEM:     string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		serialNo:    utils.GetCertificateSerialNumber(*cert),
		apiV3Key:    "0123456789abcdef0123456789abcdef",
	}
}

// aesGCMEncrypt 按 APIv3 规范做 AES-256-GCM 加密（SDK 仅提供解密）。
func aesGCMEncrypt(t *testing.T, key, nonce, associated, plaintext string) string {
	t.Helper()
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	sealed := gcm.Seal(nil, []byte(nonce), []byte(plaintext), []byte(associated))
	return base64.StdEncoding.EncodeToString(sealed)
}

// sign 用平台私钥对消息做 SHA256-RSA 签名。
func (m *wechatTestMaterial) sign(t *testing.T, message string) string {
	t.Helper()
	sig, err := utils.SignSHA256WithRSA(message, m.platformKey)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// writeSigned 写出带合法 Wechatpay-* 验签头的应答。
func (m *wechatTestMaterial) writeSigned(t *testing.T, w http.ResponseWriter, status int, body []byte) {
	t.Helper()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "TESTNONCE0123456789ABCDEF"
	w.Header().Set("Wechatpay-Timestamp", timestamp)
	w.Header().Set("Wechatpay-Nonce", nonce)
	w.Header().Set("Wechatpay-Serial", m.serialNo)
	w.Header().Set("Wechatpay-Signature", m.sign(t, timestamp+"\n"+nonce+"\n"+string(body)+"\n"))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// certificatesBody 构造 /v3/certificates 的加密平台证书应答。
func (m *wechatTestMaterial) certificatesBody(t *testing.T) []byte {
	t.Helper()
	nonce := "certnonce123"
	body, err := json.Marshal(map[string]any{
		"data": []map[string]any{{
			"serial_no":       m.serialNo,
			"effective_time":  "2020-01-01T00:00:00+08:00",
			"expire_time":     "2099-01-01T00:00:00+08:00",
			"encrypt_certificate": map[string]any{
				"algorithm":       "AEAD_AES_256_GCM",
				"nonce":           nonce,
				"associated_data": "certificate",
				"ciphertext":      aesGCMEncrypt(t, m.apiV3Key, nonce, "certificate", m.certPEM),
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// notifyRequest 构造一条验签与解密均可通过的微信回调请求。
func (m *wechatTestMaterial) notifyRequest(t *testing.T, eventType, plaintext string) *http.Request {
	t.Helper()
	nonce := "notifynonce1"
	body, err := json.Marshal(map[string]any{
		"id":            "notify-id-1",
		"create_time":   "2026-11-20T10:00:00+08:00",
		"event_type":    eventType,
		"resource_type": "encrypt-resource",
		"summary":       "test",
		"resource": map[string]any{
			"original_type":   "transaction",
			"algorithm":       "AEAD_AES_256_GCM",
			"nonce":           nonce,
			"associated_data": "transaction",
			"ciphertext":      aesGCMEncrypt(t, m.apiV3Key, nonce, "transaction", plaintext),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/payment/notify/wechat", bytes.NewReader(body))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	reqNonce := "NOTIFYREQNONCE1234567890123"
	req.Header.Set("Wechatpay-Timestamp", timestamp)
	req.Header.Set("Wechatpay-Nonce", reqNonce)
	req.Header.Set("Wechatpay-Serial", m.serialNo)
	req.Header.Set("Wechatpay-Signature", m.sign(t, timestamp+"\n"+reqNonce+"\n"+string(body)+"\n"))
	return req
}

// startWechatTestEnv 启动 httptest 服务器、安装传输层覆盖并构建真实 wechatChannel。
func startWechatTestEnv(t *testing.T, build func(m *wechatTestMaterial) http.HandlerFunc) (*wechatChannel, *wechatTestMaterial) {
	t.Helper()
	material := newWechatTestMaterial(t)
	server := httptest.NewServer(build(material))
	original := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: server, orig: pristineTransport}
	t.Cleanup(func() {
		http.DefaultTransport = original
		server.Close()
	})

	cfg := WechatConfig{
		Enabled:    true,
		AppID:      "wxtestappid001",
		MchID:      "1900000109",
		SerialNo:   "MCHTESTSERIAL",
		PrivateKey: material.merchantPEM,
		APIv3Key:   material.apiV3Key,
		NotifyURL:  "https://merchant.example.com/notify",
	}
	channel, err := newWechat(context.Background(), cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("newWechat: %v", err)
	}
	t.Cleanup(channel.stop)
	return channel, material
}

const wechatRefundJSON = `{"refund_id":"503002026","out_trade_no":"order-1","out_refund_no":"rf-1",` +
	`"user_received_account":"balance","success_time":"2026-11-19T10:05:00+08:00",` +
	`"create_time":"2026-11-19T10:04:00+08:00","status":"SUCCESS","channel":"ORIGINAL",` +
	`"amount":{"total":1500,"refund":500,"payer_total":1500,"payer_refund":500,` +
	`"settlement_refund":500,"settlement_total":1500,"discount_refund":0,"currency":"CNY","refund_currency":"CNY"}}`

// wechatSuccessHandler 返回覆盖全部下单/查询/退款/账单路径的成功应答处理器。
func wechatSuccessHandler(m *wechatTestMaterial, t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v3/certificates":
			m.writeSigned(t, w, http.StatusOK, m.certificatesBody(t))
		case r.URL.Path == "/v3/pay/transactions/native":
			m.writeSigned(t, w, http.StatusOK, []byte(`{"code_url":"weixin://wxpay/bizpayurl?pr=TEST"}`))
		case r.URL.Path == "/v3/pay/transactions/jsapi":
			m.writeSigned(t, w, http.StatusOK, []byte(`{"prepay_id":"wx201testprepayid"}`))
		case r.URL.Path == "/v3/pay/transactions/h5":
			m.writeSigned(t, w, http.StatusOK, []byte(`{"h5_url":"https://wx.tenpay.com/cashier_abc"}`))
		case r.URL.Path == "/v3/pay/transactions/app":
			m.writeSigned(t, w, http.StatusOK, []byte(`{"prepay_id":"wx201testappid"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/close"):
			m.writeSigned(t, w, http.StatusOK, nil)
		case strings.HasPrefix(r.URL.Path, "/v3/pay/transactions/out-trade-no/"):
			m.writeSigned(t, w, http.StatusOK, []byte(`{"transaction_id":"4200001","out_trade_no":"order-1",`+
				`"trade_state":"SUCCESS","success_time":"2026-11-19T10:00:00+08:00",`+
				`"payer":{"openid":"oX1"},"amount":{"total":1500,"currency":"CNY"}}`))
		case r.URL.Path == "/v3/refund/domestic/refunds":
			m.writeSigned(t, w, http.StatusOK, []byte(wechatRefundJSON))
		case strings.HasPrefix(r.URL.Path, "/v3/refund/domestic/refunds/"):
			m.writeSigned(t, w, http.StatusOK, []byte(wechatRefundJSON))
		case r.URL.Path == "/v3/bill/tradebill":
			m.writeSigned(t, w, http.StatusOK, []byte(`{"download_url":"/v3/billdownload/file",`+
				`"hash_type":"SHA1","hash_value":"deadbeef"}`))
		case r.URL.Path == "/v3/billdownload/file":
			m.writeSigned(t, w, http.StatusOK, []byte("bill,header\nbill,row\n"))
		default:
			m.writeSigned(t, w, http.StatusNotFound, []byte(`{"code":"NOT_FOUND","message":"no route"}`))
		}
	}
}

func TestWechatChannelAgainstTestServer(t *testing.T) {
	channel, _ := startWechatTestEnv(t, func(m *wechatTestMaterial) http.HandlerFunc {
		return wechatSuccessHandler(m, t)
	})
	ctx := context.Background()

	if channel.Name() != ChannelWechat {
		t.Fatalf("Name = %v", channel.Name())
	}

	// native：带 Body 与 ExpireAt，覆盖公共可选字段分支。
	native, err := channel.Prepay(ctx, Order{
		OutTradeNo: "order-1", Amount: 1500, Subject: "测试", Scene: SceneNative,
		Body: "附加信息", ExpireAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("native prepay: %v", err)
	}
	if native.CodeURL != "weixin://wxpay/bizpayurl?pr=TEST" {
		t.Fatalf("code_url = %q", native.CodeURL)
	}

	jsapi, err := channel.Prepay(ctx, Order{
		OutTradeNo: "order-2", Amount: 1500, Subject: "测试", Scene: SceneJSAPI, OpenID: "oX1",
	})
	if err != nil {
		t.Fatalf("jsapi prepay: %v", err)
	}
	if jsapi.PrepayID != "wx201testprepayid" || jsapi.PayParams["package"] != "prepay_id=wx201testprepayid" {
		t.Fatalf("jsapi result = %+v", jsapi)
	}
	if jsapi.PayParams["paySign"] == "" || jsapi.PayParams["appid"] != "wxtestappid001" {
		t.Fatalf("jsapi pay params = %+v", jsapi.PayParams)
	}

	h5, err := channel.Prepay(ctx, Order{
		OutTradeNo: "order-3", Amount: 1500, Subject: "测试", Scene: SceneH5, ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("h5 prepay: %v", err)
	}
	if h5.H5URL != "https://wx.tenpay.com/cashier_abc" {
		t.Fatalf("h5_url = %q", h5.H5URL)
	}

	app, err := channel.Prepay(ctx, Order{
		OutTradeNo: "order-4", Amount: 1500, Subject: "测试", Scene: SceneApp,
	})
	if err != nil {
		t.Fatalf("app prepay: %v", err)
	}
	if app.PrepayID != "wx201testappid" || app.PayParams["prepayid"] != "wx201testappid" {
		t.Fatalf("app result = %+v", app)
	}

	trade, err := channel.Query(ctx, "order-1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if trade.State != StateSuccess || trade.Amount != 1500 || trade.Payer != "oX1" {
		t.Fatalf("trade = %+v", trade)
	}

	if err := channel.Close(ctx, "order-1"); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 退款：wechat.go 已补齐 AmountReq.Currency（SDK v0.2.21 必填校验字段），
	// 覆盖成功返回路径 mapWechatRefund 及 Reason/NotifyURL 可选字段分支。
	refund, err := channel.Refund(ctx, RefundRequest{
		OutTradeNo: "order-1", OutRefundNo: "rf-1", RefundAmount: 500, TotalAmount: 1500,
		Reason: "质量问题", NotifyURL: "https://merchant.example.com/refund-notify",
	})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if refund.OutRefundNo != "rf-1" || refund.RefundID != "503002026" {
		t.Fatalf("refund = %+v", refund)
	}

	queryRefund, err := channel.QueryRefund(ctx, "rf-1")
	if err != nil {
		t.Fatalf("query refund: %v", err)
	}
	if queryRefund.State != StateSuccess || queryRefund.OutRefundNo != "rf-1" {
		t.Fatalf("query refund = %+v", queryRefund)
	}

	bill, err := channel.DownloadBill(ctx, BillRequest{Date: time.Now().AddDate(0, 0, -1)})
	if err != nil {
		t.Fatalf("download bill: %v", err)
	}
	if bill.HashType != "SHA1" || !strings.Contains(string(bill.Content), "bill,header") {
		t.Fatalf("bill = %+v", bill)
	}

	// 显式指定账单类型，覆盖 billType 非空分支。
	if _, err := channel.DownloadBill(ctx, BillRequest{
		Date: time.Now().AddDate(0, 0, -1), BillType: "SUCCESS",
	}); err != nil {
		t.Fatalf("download bill with type: %v", err)
	}
}

func TestWechatChannelInputGuards(t *testing.T) {
	channel, _ := startWechatTestEnv(t, func(m *wechatTestMaterial) http.HandlerFunc {
		return wechatSuccessHandler(m, t)
	})
	ctx := context.Background()

	if _, err := channel.Query(ctx, " "); err == nil {
		t.Fatal("query with blank out_trade_no must fail")
	}
	if err := channel.Close(ctx, ""); err == nil {
		t.Fatal("close with blank out_trade_no must fail")
	}
	if _, err := channel.Refund(ctx, RefundRequest{}); err == nil {
		t.Fatal("refund without fields must fail")
	}
	if _, err := channel.QueryRefund(ctx, ""); err == nil {
		t.Fatal("query refund with blank no must fail")
	}
	if _, err := channel.DownloadBill(ctx, BillRequest{}); err == nil {
		t.Fatal("bill without date must fail")
	}
}

func TestWechatChannelAPIErrors(t *testing.T) {
	channel, _ := startWechatTestEnv(t, func(m *wechatTestMaterial) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v3/certificates" {
				m.writeSigned(t, w, http.StatusOK, m.certificatesBody(t))
				return
			}
			m.writeSigned(t, w, http.StatusInternalServerError, []byte(`{"code":"SYSTEM_ERROR","message":"boom"}`))
		}
	})
	ctx := context.Background()

	if _, err := channel.Prepay(ctx, Order{
		OutTradeNo: "order-1", Amount: 1, Subject: "x", Scene: SceneNative,
	}); err == nil {
		t.Fatal("native prepay must fail")
	}
	if _, err := channel.Prepay(ctx, Order{
		OutTradeNo: "order-2", Amount: 1, Subject: "x", Scene: SceneJSAPI, OpenID: "o",
	}); err == nil {
		t.Fatal("jsapi prepay must fail")
	}
	if _, err := channel.Prepay(ctx, Order{
		OutTradeNo: "order-3", Amount: 1, Subject: "x", Scene: SceneH5, ClientIP: "1.2.3.4",
	}); err == nil {
		t.Fatal("h5 prepay must fail")
	}
	if _, err := channel.Prepay(ctx, Order{
		OutTradeNo: "order-4", Amount: 1, Subject: "x", Scene: SceneApp,
	}); err == nil {
		t.Fatal("app prepay must fail")
	}
	if _, err := channel.Query(ctx, "order-1"); err == nil {
		t.Fatal("query must fail")
	}
	if err := channel.Close(ctx, "order-1"); err == nil {
		t.Fatal("close must fail")
	}
	if _, err := channel.Refund(ctx, RefundRequest{
		OutTradeNo: "order-1", OutRefundNo: "rf-1", RefundAmount: 1, TotalAmount: 1,
	}); err == nil {
		t.Fatal("refund must fail")
	}
	if _, err := channel.QueryRefund(ctx, "rf-1"); err == nil {
		t.Fatal("query refund must fail")
	}
	if _, err := channel.DownloadBill(ctx, BillRequest{Date: time.Now()}); err == nil {
		t.Fatal("bill apply must fail")
	}
}

func TestWechatDownloadBillBranches(t *testing.T) {
	// 应答下载申请成功但账单内容接口 500，覆盖 download 错误分支。
	channel, _ := startWechatTestEnv(t, func(m *wechatTestMaterial) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v3/certificates":
				m.writeSigned(t, w, http.StatusOK, m.certificatesBody(t))
			case "/v3/bill/tradebill":
				m.writeSigned(t, w, http.StatusOK, []byte(`{"download_url":"/v3/billdownload/file"}`))
			default:
				m.writeSigned(t, w, http.StatusInternalServerError, []byte(`{"code":"FAIL"}`))
			}
		}
	})
	if _, err := channel.DownloadBill(context.Background(), BillRequest{Date: time.Now()}); err == nil {
		t.Fatal("bill content download must fail")
	}

	// 申请应答不是合法 JSON，覆盖解码错误分支。
	badJSON, _ := startWechatTestEnv(t, func(m *wechatTestMaterial) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v3/certificates":
				m.writeSigned(t, w, http.StatusOK, m.certificatesBody(t))
			default:
				m.writeSigned(t, w, http.StatusOK, []byte(`not-json`))
			}
		}
	})
	if _, err := badJSON.DownloadBill(context.Background(), BillRequest{Date: time.Now()}); err == nil {
		t.Fatal("bill decode failure must return error")
	}

	// 申请应答缺少 download_url，覆盖 Internal 分支。
	missingURL, _ := startWechatTestEnv(t, func(m *wechatTestMaterial) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v3/certificates":
				m.writeSigned(t, w, http.StatusOK, m.certificatesBody(t))
			default:
				m.writeSigned(t, w, http.StatusOK, []byte(`{"hash_type":"SHA1"}`))
			}
		}
	})
	if _, err := missingURL.DownloadBill(context.Background(), BillRequest{Date: time.Now()}); err == nil {
		t.Fatal("missing download_url must return error")
	}
}

func TestNewWechatFailures(t *testing.T) {
	ctx := context.Background()

	// 空私钥：loadKeyMaterial 报 key material is empty。
	if _, err := newWechat(ctx, WechatConfig{}, time.Second); err == nil {
		t.Fatal("empty private key must fail")
	}

	// PEM 形状但内容非法：utils.LoadPrivateKey 报错。
	if _, err := newWechat(ctx, WechatConfig{
		PrivateKey: "-----BEGIN PRIVATE KEY-----\nZm9v\n-----END PRIVATE KEY-----\n",
	}, time.Second); err == nil {
		t.Fatal("malformed private key must fail")
	}

	// 平台证书下载失败：注册下载器即报错，覆盖 cancel 释放分支。
	material := newWechatTestMaterial(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	original := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: server, orig: original}
	defer func() {
		http.DefaultTransport = original
		server.Close()
	}()
	if _, err := newWechat(ctx, WechatConfig{
		PrivateKey: material.merchantPEM, APIv3Key: material.apiV3Key, MchID: "1900000109",
	}, time.Second); err == nil {
		t.Fatal("certificate downloader failure must propagate")
	}
}

func TestWechatParseNotify(t *testing.T) {
	channel, material := startWechatTestEnv(t, func(m *wechatTestMaterial) http.HandlerFunc {
		return wechatSuccessHandler(m, t)
	})
	ctx := context.Background()

	// 支付成功回调。
	req := material.notifyRequest(t, "TRANSACTION.SUCCESS",
		`{"out_trade_no":"order-n1","transaction_id":"4200001","trade_state":"SUCCESS",`+
			`"success_time":"2026-11-20T10:00:01+08:00","payer":{"openid":"oX9"},"amount":{"total":1234}}`)
	result, err := channel.ParseNotify(ctx, req)
	if err != nil {
		t.Fatalf("parse payment notify: %v", err)
	}
	if result.Type != NotifyPayment || result.State != StateSuccess || result.Amount != 1234 {
		t.Fatalf("payment notify = %+v", result)
	}
	if result.OutTradeNo != "order-n1" || result.Payer != "oX9" || result.IdempotencyKey != "order-n1:SUCCESS" {
		t.Fatalf("payment notify fields = %+v", result)
	}
	if result.SuccessTime.IsZero() {
		t.Fatal("success time not parsed")
	}

	// 退款回调。
	req = material.notifyRequest(t, "REFUND.SUCCESS",
		`{"out_trade_no":"order-n1","out_refund_no":"rf-n1","refund_status":"SUCCESS",`+
			`"amount":{"total":1234,"refund":100}}`)
	refund, err := channel.ParseNotify(ctx, req)
	if err != nil {
		t.Fatalf("parse refund notify: %v", err)
	}
	if refund.Type != NotifyRefund || refund.State != StateSuccess ||
		refund.OutRefundNo != "rf-n1" || refund.RefundAmount != 100 || refund.Amount != 1234 {
		t.Fatalf("refund notify = %+v", refund)
	}
	if refund.IdempotencyKey != "rf-n1:SUCCESS" {
		t.Fatalf("refund idempotency key = %q", refund.IdempotencyKey)
	}

	// 验签失败。
	bad := material.notifyRequest(t, "TRANSACTION.SUCCESS", `{"out_trade_no":"x"}`)
	bad.Header.Set("Wechatpay-Signature", "AAAA")
	if _, err := channel.ParseNotify(ctx, bad); err == nil {
		t.Fatal("bad signature must fail")
	}
}

func TestWechatMappingNilBranches(t *testing.T) {
	// 直接构造缺字段对象，覆盖 nil 指针与解析失败分支。
	if derefString(nil) != "" || derefInt64(nil) != 0 {
		t.Fatal("deref nil must return zero values")
	}

	trade := mapWechatTransaction(&payments.Transaction{})
	if trade.Amount != 0 || trade.Payer != "" || !trade.SuccessTime.IsZero() {
		t.Fatalf("empty txn = %+v", trade)
	}
	badTime := "not-a-time"
	trade = mapWechatTransaction(&payments.Transaction{SuccessTime: &badTime})
	if !trade.SuccessTime.IsZero() {
		t.Fatal("bad success time must be ignored")
	}

	refund := mapWechatRefund(&refunddomestic.Refund{})
	if refund.State != "" || refund.Amount != 0 || !refund.SuccessTime.IsZero() {
		t.Fatalf("empty refund = %+v", refund)
	}

	content := map[string]interface{}{
		"str":     "value",
		"num":     float64(7),
		"amount":  map[string]interface{}{"total": json.Number("88"), "bad": json.Number("x"), "str": "y"},
		"missing": "not-a-map",
	}
	if got := contentString(content, "num"); got != "" {
		t.Fatalf("non-string contentString = %q", got)
	}
	if got := contentString(content, "absent"); got != "" {
		t.Fatalf("absent contentString = %q", got)
	}
	if got := contentNestedString(content, "missing", "x"); got != "" {
		t.Fatalf("nested non-map = %q", got)
	}
	if got := contentNestedInt64(content, "amount", "total"); got != 88 {
		t.Fatalf("json.Number = %d", got)
	}
	if got := contentNestedInt64(content, "amount", "bad"); got != 0 {
		t.Fatalf("bad json.Number = %d", got)
	}
	if got := contentNestedInt64(content, "amount", "str"); got != 0 {
		t.Fatalf("string value = %d", got)
	}
	if got := contentNestedInt64(content, "missing", "x"); got != 0 {
		t.Fatalf("non-map int64 = %d", got)
	}
}

// 说明：DownloadBill 中 io.ReadAll 的错误分支无法覆盖——SDK 应答验签器
// 会先完整读取并回填 Body，ReadAll 之后不会再出现读错误，属于防御性代码。
