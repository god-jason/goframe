package payment

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pristineTransport 是包加载时捕获的原始传输层；嵌套启动多个测试环境时，
// rewriteTransport 始终以它为 orig，避免内层请求被外层环境的改写器劫持。
var pristineTransport = http.DefaultTransport

// alipayTestEnv 持有通道、私钥与本地服务器，用于支付宝接口测试。
type alipayTestEnv struct {
	channel *alipayChannel
	priv    *rsa.PrivateKey
	privPEM string
	pubPEM  string
	server  *httptest.Server
}

// startAlipayTestEnv 启动本地网关并注入传输层，使通道的所有请求都命中本地服务器。
func startAlipayTestEnv(t *testing.T, build func(e *alipayTestEnv) http.HandlerFunc) *alipayTestEnv {
	t.Helper()
	privPEM, pubPEM, priv := generateAlipayKeys(t)
	channel, err := newAlipay(AlipayConfig{
		Enabled:    true,
		AppID:      "2021000000000000",
		PrivateKey: privPEM,
		PublicKey:  pubPEM,
		NotifyURL:  "https://merchant.example.com/notify",
	}, 5*time.Second)
	if err != nil {
		t.Fatalf("newAlipay: %v", err)
	}
	env := &alipayTestEnv{channel: channel, priv: priv, privPEM: privPEM, pubPEM: pubPEM}
	env.server = httptest.NewServer(build(env))
	original := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: env.server, orig: pristineTransport}
	t.Cleanup(func() {
		http.DefaultTransport = original
		env.server.Close()
	})
	// 把通道的 HTTP 客户端指向本地服务器（保留超时配置）。
	channel.client.Client = &http.Client{
		Transport: &rewriteTransport{target: env.server, orig: pristineTransport},
		Timeout:   5 * time.Second,
	}
	return env
}

// signAlipayBiz 用支付宝私钥对业务 JSON 做 RSA2 签名，并组装完整应答报文。
func signAlipayBiz(t *testing.T, priv *rsa.PrivateKey, bizField, bizJSON string) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte(bizJSON))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return []byte(fmt.Sprintf(`{"%s":%s,"sign":"%s","sign_type":"RSA2"}`,
		bizField, bizJSON, base64.StdEncoding.EncodeToString(sig)))
}

// alipaySuccessHandler 返回覆盖所有业务接口的成功应答处理器。
func alipaySuccessHandler(env *alipayTestEnv, t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bill.csv" {
			_, _ = w.Write([]byte("bill,date,amount\n2026-11-19,15.00\n"))
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch r.FormValue("method") {
		case "alipay.trade.precreate":
			w.Write(signAlipayBiz(t, env.priv, "alipay_trade_precreate_response",
				`{"code":"10000","msg":"Success","out_trade_no":"`+r.FormValue("out_trade_no")+
					`","qr_code":"https://qr.alipay.com/abc"}`))
		case "alipay.trade.query":
			w.Write(signAlipayBiz(t, env.priv, "alipay_trade_query_response",
				`{"code":"10000","msg":"Success","out_trade_no":"`+r.FormValue("out_trade_no")+
					`","trade_no":"2026112022001400000000","trade_status":"TRADE_SUCCESS",`+
					`"total_amount":"15.00","buyer_user_id":"2088000000000000",`+
					`"send_pay_date":"2026-11-20 10:00:00"}`))
		case "alipay.trade.close":
			w.Write(signAlipayBiz(t, env.priv, "alipay_trade_close_response",
				`{"code":"10000","msg":"Success","out_trade_no":"`+r.FormValue("out_trade_no")+
					`","trade_no":"2026112022001400000000"}`))
		case "alipay.trade.refund":
			w.Write(signAlipayBiz(t, env.priv, "alipay_trade_refund_response",
				`{"code":"10000","msg":"Success","trade_no":"2026112022001400000000",`+
					`"out_trade_no":"`+r.FormValue("out_trade_no")+
					`","refund_fee":"5.00"}`))
		case "alipay.trade.fastpay.refund.query":
			w.Write(signAlipayBiz(t, env.priv, "alipay_trade_fastpay_refund_query_response",
				`{"code":"10000","msg":"Success","trade_no":"2026112022001400000000",`+
					`"out_trade_no":"`+r.FormValue("out_trade_no")+
					`","out_request_no":"`+r.FormValue("out_request_no")+
					`","refund_amount":"5.00","refund_status":"REFUND_SUCCESS",`+
					`"gmt_refund_pay":"2026-11-20 10:05:00"}`))
		case "alipay.data.dataservice.bill.downloadurl.query":
			w.Write(signAlipayBiz(t, env.priv, "alipay_data_dataservice_bill_downloadurl_query_response",
				`{"code":"10000","msg":"Success","bill_type":"trade","bill_download_url":"`+
					env.server.URL+`/bill.csv"}`))
		default:
			w.Write(signAlipayBiz(t, env.priv, "error_response",
				`{"code":"40004","msg":"Business Failed","sub_code":"INVALID_METHOD","sub_msg":"unknown"}`))
		}
	}
}

func TestAlipayChannelAgainstTestServer(t *testing.T) {
	env := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return alipaySuccessHandler(e, t)
	})
	ctx := context.Background()

	if env.channel.Name() != ChannelAlipay {
		t.Fatalf("Name = %v", env.channel.Name())
	}

	pre, err := env.channel.Prepay(ctx, Order{
		OutTradeNo: "order-pc", Amount: 1500, Subject: "测试", Scene: SceneNative,
	})
	if err != nil {
		t.Fatalf("precreate: %v", err)
	}
	if pre.CodeURL != "https://qr.alipay.com/abc" {
		t.Fatalf("qr_code = %q", pre.CodeURL)
	}

	// 页面/APP 下单：不发起网络，仅构造 URL，覆盖 ReturnURL 回退分支。
	page, err := env.channel.Prepay(ctx, Order{
		OutTradeNo: "order-pg", Amount: 1500, Subject: "测试", Scene: ScenePage,
	})
	if err != nil {
		t.Fatalf("page prepay: %v", err)
	}
	if !strings.Contains(page.Redirect, "alipay.trade.page.pay") {
		t.Fatalf("page url = %q", page.Redirect)
	}

	// 配置级 ReturnURL 回退分支。
	env.channel.cfg.ReturnURL = "https://merchant.example.com/return"
	pageFallback, err := env.channel.Prepay(ctx, Order{
		OutTradeNo: "order-pg2", Amount: 1500, Subject: "测试", Scene: ScenePage,
	})
	if err != nil {
		t.Fatalf("page fallback: %v", err)
	}
	if !strings.Contains(pageFallback.Redirect, url.QueryEscape("https://merchant.example.com/return")) {
		t.Fatalf("page fallback url = %q", pageFallback.Redirect)
	}

	wap, err := env.channel.Prepay(ctx, Order{
		OutTradeNo: "order-wp", Amount: 1500, Subject: "测试", Scene: SceneH5,
	})
	if err != nil {
		t.Fatalf("wap prepay: %v", err)
	}
	if !strings.Contains(wap.Redirect, "alipay.trade.wap.pay") {
		t.Fatalf("wap url = %q", wap.Redirect)
	}

	app, err := env.channel.Prepay(ctx, Order{
		OutTradeNo: "order-ap", Amount: 1500, Subject: "测试", Scene: SceneApp,
	})
	if err != nil {
		t.Fatalf("app prepay: %v", err)
	}
	if !strings.Contains(app.OrderStr, "alipay.trade.app.pay") {
		t.Fatalf("app order str = %q", app.OrderStr)
	}

	trade, err := env.channel.Query(ctx, "order-1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if trade.State != StateSuccess || trade.Amount != 1500 || trade.Payer != "2088000000000000" {
		t.Fatalf("trade = %+v", trade)
	}
	if trade.SuccessTime.IsZero() {
		t.Fatal("success time not parsed")
	}

	if err := env.channel.Close(ctx, "order-1"); err != nil {
		t.Fatalf("close: %v", err)
	}

	refund, err := env.channel.Refund(ctx, RefundRequest{
		OutTradeNo: "order-1", OutRefundNo: "rf-1", RefundAmount: 500, TotalAmount: 1500,
		Reason: "质量问题", NotifyURL: "https://merchant.example.com/refund-notify",
	})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if refund.Amount != 500 {
		t.Fatalf("refund amount = %d", refund.Amount)
	}

	// 不带可选字段的退款。
	if _, err := env.channel.Refund(ctx, RefundRequest{
		OutTradeNo: "order-1", OutRefundNo: "rf-2", RefundAmount: 500, TotalAmount: 1500,
	}); err != nil {
		t.Fatalf("refund plain: %v", err)
	}

	queryRefund, err := env.channel.QueryRefund(ctx, "rf-1")
	if err != nil {
		t.Fatalf("query refund: %v", err)
	}
	if queryRefund.State != StateSuccess || queryRefund.Amount != 500 {
		t.Fatalf("query refund = %+v", queryRefund)
	}
	if queryRefund.SuccessTime.IsZero() {
		t.Fatal("refund time not parsed")
	}

	// 账单下载：覆盖 http.DefaultClient 的传输覆盖分支。
	bill, err := env.channel.DownloadBill(ctx, BillRequest{Date: time.Now().AddDate(0, 0, -1)})
	if err != nil {
		t.Fatalf("download bill: %v", err)
	}
	if !strings.Contains(string(bill.Content), "bill") {
		t.Fatalf("bill content = %q", string(bill.Content))
	}
}

func TestAlipayChannelInputGuards(t *testing.T) {
	env := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return alipaySuccessHandler(e, t)
	})
	ctx := context.Background()

	if _, err := env.channel.Query(ctx, " "); err == nil {
		t.Fatal("blank out_trade_no must fail")
	}
	if err := env.channel.Close(ctx, ""); err == nil {
		t.Fatal("blank close must fail")
	}
	if _, err := env.channel.Refund(ctx, RefundRequest{}); err == nil {
		t.Fatal("empty refund must fail")
	}
	if _, err := env.channel.QueryRefund(ctx, ""); err == nil {
		t.Fatal("blank refund query must fail")
	}
	if _, err := env.channel.DownloadBill(ctx, BillRequest{}); err == nil {
		t.Fatal("bill without date must fail")
	}
	// 未配置通知地址。
	env.channel.cfg.NotifyURL = ""
	if _, err := env.channel.Prepay(ctx, Order{
		OutTradeNo: "order-x", Amount: 100, Subject: "x", Scene: SceneNative,
	}); err == nil {
		t.Fatal("prepay without notify url must fail")
	}
}

func TestAlipayChannelAPIErrors(t *testing.T) {
	env := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Write(signAlipayBiz(t, e.priv, "error_response",
				`{"code":"40004","msg":"Business Failed","sub_code":"ACQ.TRADE_NOT_EXIST","sub_msg":"交易不存在"}`))
		}
	})
	ctx := context.Background()

	if _, err := env.channel.Prepay(ctx, Order{
		OutTradeNo: "order-1", Amount: 1, Subject: "x", Scene: SceneNative,
	}); err == nil {
		t.Fatal("prepay must fail")
	}
	if _, err := env.channel.Query(ctx, "order-1"); err == nil {
		t.Fatal("query must fail")
	}
	if err := env.channel.Close(ctx, "order-1"); err == nil {
		t.Fatal("close must fail")
	}
	if _, err := env.channel.Refund(ctx, RefundRequest{
		OutTradeNo: "order-1", OutRefundNo: "rf-1", RefundAmount: 1, TotalAmount: 1,
	}); err == nil {
		t.Fatal("refund must fail")
	}
	if _, err := env.channel.QueryRefund(ctx, "rf-1"); err == nil {
		t.Fatal("query refund must fail")
	}
	if _, err := env.channel.DownloadBill(ctx, BillRequest{Date: time.Now()}); err == nil {
		t.Fatal("bill must fail")
	}
}

func TestAlipayDownloadBillBranches(t *testing.T) {
	// 账单 URL 无效 → NewRequest 报错，返回 (file, nil)。
	env := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Write(signAlipayBiz(t, e.priv, "alipay_data_dataservice_bill_downloadurl_query_response",
				`{"code":"10000","msg":"Success","bill_type":"trade","bill_download_url":"://bad"}`))
		}
	})
	file, err := env.channel.DownloadBill(context.Background(), BillRequest{Date: time.Now()})
	if err != nil {
		t.Fatalf("invalid bill url must return file, nil: %v", err)
	}
	if file == nil || file.DownloadURL != "://bad" {
		t.Fatalf("file = %+v", file)
	}

	// 账单 URL 命中被服务器主动中断的路径 → Do 报传输错误。
	env2 := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/abort" {
				panic(http.ErrAbortHandler)
			}
			w.Write(signAlipayBiz(t, e.priv, "alipay_data_dataservice_bill_downloadurl_query_response",
				`{"code":"10000","msg":"Success","bill_type":"trade","bill_download_url":"`+
					e.server.URL+`/abort"}`))
		}
	})
	if _, err := env2.channel.DownloadBill(context.Background(), BillRequest{Date: time.Now()}); err == nil {
		t.Fatal("bill do error must propagate")
	}

	// 账单下载返回 404 → 状态码错误。
	env3 := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/bill.csv" {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			w.Write(signAlipayBiz(t, e.priv, "alipay_data_dataservice_bill_downloadurl_query_response",
				`{"code":"10000","msg":"Success","bill_type":"trade","bill_download_url":"`+
					e.server.URL+`/bill.csv"}`))
		}
	})
	if _, err := env3.channel.DownloadBill(context.Background(), BillRequest{Date: time.Now()}); err == nil {
		t.Fatal("bill status must fail")
	}

	// 账单下载 Body 读取失败 → io.ReadAll 错误。
	env4 := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/bill.csv" {
				w.Header().Set("Content-Length", "100")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				_, _ = w.Write([]byte("partial"))
				if hj, ok := w.(http.Hijacker); ok {
					if conn, _, err := hj.Hijack(); err == nil {
						_ = conn.Close()
					}
				}
				return
			}
			w.Write(signAlipayBiz(t, e.priv, "alipay_data_dataservice_bill_downloadurl_query_response",
				`{"code":"10000","msg":"Success","bill_type":"trade","bill_download_url":"`+
					e.server.URL+`/bill.csv"}`))
		}
	})
	if _, err := env4.channel.DownloadBill(context.Background(), BillRequest{Date: time.Now()}); err == nil {
		t.Fatal("bill read error must propagate")
	}
}

func TestAlipayParseSkipBranches(t *testing.T) {
	// 查询应答：total_amount 与 send_pay_date 非法 → 跳过解析。
	env := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Write(signAlipayBiz(t, e.priv, "alipay_trade_query_response",
				`{"code":"10000","msg":"Success","out_trade_no":"o","trade_no":"t",`+
					`"trade_status":"WAIT_BUYER_PAY","total_amount":"abc",`+
					`"send_pay_date":"not-a-date"}`))
		}
	})
	trade, err := env.channel.Query(context.Background(), "order-1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if trade.Amount != 0 || !trade.SuccessTime.IsZero() {
		t.Fatalf("bad fields must be skipped: %+v", trade)
	}
	if trade.State != StatePending {
		t.Fatalf("state = %v", trade.State)
	}

	// 退款应答：refund_fee 非法 → 跳过。
	env2 := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Write(signAlipayBiz(t, e.priv, "alipay_trade_refund_response",
				`{"code":"10000","msg":"Success","trade_no":"t","out_trade_no":"o","refund_fee":"abc"}`))
		}
	})
	refund, err := env2.channel.Refund(context.Background(), RefundRequest{
		OutTradeNo: "o", OutRefundNo: "rf", RefundAmount: 1, TotalAmount: 1,
	})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if refund.Amount != 0 {
		t.Fatalf("refund amount = %d", refund.Amount)
	}

	// 退款查询：状态非 SUCCESS、时间非法 → 保持 Pending。
	env3 := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Write(signAlipayBiz(t, e.priv, "alipay_trade_fastpay_refund_query_response",
				`{"code":"10000","msg":"Success","trade_no":"t","out_trade_no":"o","out_request_no":"rf",`+
					`"refund_amount":"0.05","refund_status":"REFUND_PROCESSING","gmt_refund_pay":"bad"}`))
		}
	})
	qr, err := env3.channel.QueryRefund(context.Background(), "rf")
	if err != nil {
		t.Fatalf("query refund: %v", err)
	}
	if qr.State != StatePending || !qr.SuccessTime.IsZero() {
		t.Fatalf("query refund = %+v", qr)
	}
}

func TestAlipaySignatureErrors(t *testing.T) {
	// 应答签名非法 → 验签失败。
	env := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"alipay_trade_query_response":{"code":"10000","msg":"Success"},"sign":"AAAA","sign_type":"RSA2"}`))
		}
	})
	if _, err := env.channel.Query(context.Background(), "order-1"); err == nil {
		t.Fatal("bad signature must fail")
	}

	// 传输层错误：Handler 主动中断 → 客户端收到 EOF。
	env2 := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			panic(http.ErrAbortHandler)
		}
	})
	if _, err := env2.channel.Query(context.Background(), "order-1"); err == nil {
		t.Fatal("transport error must fail")
	}
}

func TestAlipayParseNotifyRefundBranch(t *testing.T) {
	env := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return alipaySuccessHandler(e, t)
	})
	form := url.Values{
		"trade_no":        []string{"2026112022001400000000"},
		"out_trade_no":    []string{"order-1"},
		"trade_status":    []string{"TRADE_SUCCESS"},
		"total_amount":    []string{"15.00"},
		"buyer_id":        []string{"2088000000000000"},
		"out_request_no":  []string{"rf-1"},
		"refund_amount":   []string{"5.00"},
		"gmt_refund":      []string{"2026-11-20 10:05:00"},
	}
	form.Set("sign", signAlipayNotification(t, env.priv, form))
	form.Set("sign_type", "RSA2")
	req := httptest.NewRequest(http.MethodPost, "/payment/notify/alipay",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	result, err := env.channel.ParseNotify(context.Background(), req)
	if err != nil {
		t.Fatalf("parse refund notify: %v", err)
	}
	if result.Type != NotifyRefund || result.OutRefundNo != "rf-1" ||
		result.RefundAmount != 500 || result.State != StateSuccess {
		t.Fatalf("refund notify = %+v", result)
	}
	if result.IdempotencyKey != "order-1:rf-1:TRADE_SUCCESS" {
		t.Fatalf("idempotency key = %q", result.IdempotencyKey)
	}
	if result.SuccessTime.IsZero() {
		t.Fatal("refund time not parsed")
	}

	// 退款时间非法 → 跳过解析。
	form.Set("gmt_refund", "bad")
	form.Set("sign", signAlipayNotification(t, env.priv, form))
	req = httptest.NewRequest(http.MethodPost, "/payment/notify/alipay",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	result, err = env.channel.ParseNotify(context.Background(), req)
	if err != nil {
		t.Fatalf("parse refund notify with bad time: %v", err)
	}
	if !result.SuccessTime.IsZero() {
		t.Fatal("bad refund time must be skipped")
	}
}

func TestAlipayParseNotifyErrors(t *testing.T) {
	env := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return alipaySuccessHandler(e, t)
	})
	// 非法表单编码 → ParseForm 失败。
	req := httptest.NewRequest(http.MethodPost, "/payment/notify/alipay",
		strings.NewReader("%zz=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := env.channel.ParseNotify(context.Background(), req); err == nil {
		t.Fatal("bad form must fail")
	}

	// 缺少 sign 字段 → 验签失败。
	form := url.Values{"out_trade_no": []string{"order-1"}, "trade_status": []string{"TRADE_SUCCESS"}}
	req = httptest.NewRequest(http.MethodPost, "/payment/notify/alipay",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := env.channel.ParseNotify(context.Background(), req); err == nil {
		t.Fatal("missing sign must fail")
	}

	// 验签失败。
	form = url.Values{"out_trade_no": []string{"order-1"}, "trade_status": []string{"TRADE_SUCCESS"}}
	form.Set("sign", "AAAA")
	form.Set("sign_type", "RSA2")
	req = httptest.NewRequest(http.MethodPost, "/payment/notify/alipay",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := env.channel.ParseNotify(context.Background(), req); err == nil {
		t.Fatal("bad signature must fail")
	}
}

func TestNewAlipayFailures(t *testing.T) {
	// 空私钥。
	if _, err := newAlipay(AlipayConfig{Enabled: true, AppID: "a", PublicKey: "x"}, time.Second); err == nil {
		t.Fatal("empty private key must fail")
	}
	// 非法私钥。
	if _, err := newAlipay(AlipayConfig{
		Enabled: true, AppID: "a", PrivateKey: "-----BEGIN RSA PRIVATE KEY-----\nZm9v\n-----END RSA PRIVATE KEY-----\n",
		PublicKey: "x",
	}, time.Second); err == nil {
		t.Fatal("malformed private key must fail")
	}
	// 空公钥（loadKeyMaterial 报 key material is empty）。
	privPEM, _, _ := generateAlipayKeys(t)
	if _, err := newAlipay(AlipayConfig{
		Enabled: true, AppID: "a", PrivateKey: privPEM,
	}, time.Second); err == nil {
		t.Fatal("empty public key must fail")
	}
	// 非法公钥（loadKeyMaterial 返回非 PEM 内容）。
	if _, err := newAlipay(AlipayConfig{
		Enabled: true, AppID: "a", PrivateKey: privPEM, PublicKey: "not-a-pem",
	}, time.Second); err == nil {
		t.Fatal("malformed public key must fail")
	}
}

func TestNewAlipayCertMode(t *testing.T) {
	dir := t.TempDir()
	appKey, appCertPEM, _ := writeSelfCert(t, dir, "app.crt")
	_, aliCertPEM, aliRSA := writeSelfCert(t, dir, "ali.crt")
	rootPEM := writeRootCert(t, dir, "root.crt")

	// 证书模式成功路径。
	cfg := AlipayConfig{
		Enabled:          true,
		AppID:            "2021000000000000",
		PrivateKey:       appKey,
		AppCertFile:      filepath.Join(dir, "app.crt"),
		AlipayCertFile:   filepath.Join(dir, "ali.crt"),
		RootCertFile:     filepath.Join(dir, "root.crt"),
		NotifyURL:        "https://merchant.example.com/notify",
	}
	channel, err := newAlipay(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("cert mode: %v", err)
	}
	if channel == nil {
		t.Fatal("channel nil")
	}

	// 用 alipay 私钥签名一条应答（不带 alipay_cert_sn），验证走 getVerifier 默认分支。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(signAlipayBiz(t, aliRSA, "alipay_trade_query_response",
			`{"code":"10000","msg":"Success","out_trade_no":"o","trade_no":"t","trade_status":"WAIT_BUYER_PAY"}`))
	}))
	defer server.Close()
	original := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: server, orig: original}
	defer func() { http.DefaultTransport = original }()
	channel.client.Client = &http.Client{
		Transport: &rewriteTransport{target: server, orig: original},
		Timeout:   5 * time.Second,
	}
	if _, err := channel.Query(context.Background(), "o"); err != nil {
		t.Fatalf("cert mode query: %v", err)
	}

	// 证书文件不存在 → 三个分支依次覆盖。
	_, err = newAlipay(AlipayConfig{
		Enabled: true, AppID: "a", PrivateKey: appKey,
		AppCertFile: filepath.Join(dir, "missing.crt"),
	}, time.Second)
	if err == nil {
		t.Fatal("missing app cert must fail")
	}
	_, err = newAlipay(AlipayConfig{
		Enabled: true, AppID: "a", PrivateKey: appKey,
		AppCertFile:    filepath.Join(dir, "app.crt"),
		AlipayCertFile: filepath.Join(dir, "missing.crt"),
	}, time.Second)
	if err == nil {
		t.Fatal("missing alipay cert must fail")
	}
	_, err = newAlipay(AlipayConfig{
		Enabled: true, AppID: "a", PrivateKey: appKey,
		AppCertFile:    filepath.Join(dir, "app.crt"),
		AlipayCertFile: filepath.Join(dir, "ali.crt"),
		RootCertFile:   filepath.Join(dir, "missing.crt"),
	}, time.Second)
	if err == nil {
		t.Fatal("missing root cert must fail")
	}

	// 证书内容非法 → 解码失败。
	bad := filepath.Join(dir, "bad.crt")
	if err := os.WriteFile(bad, []byte("not a cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newAlipay(AlipayConfig{
		Enabled: true, AppID: "a", PrivateKey: appKey,
		AppCertFile: bad,
	}, time.Second); err == nil {
		t.Fatal("bad app cert must fail")
	}

	// 避免 unused import。
	_ = appCertPEM
	_ = aliCertPEM
	_ = rootPEM
}

// writeSelfCert 生成自签名证书并写入文件，返回私钥 PEM、证书 PEM 与私钥对象。
func writeSelfCert(t *testing.T, dir, name string) (string, string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if err := os.WriteFile(filepath.Join(dir, name), []byte(certPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	return keyPEM, certPEM, key
}

// writeRootCert 写入一个自签名根证书（SHA256-RSA，会被 LoadAliPayRootCert 加入 SN 集合）。
func writeRootCert(t *testing.T, dir, name string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		SignatureAlgorithm:    x509.SHA256WithRSA,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if err := os.WriteFile(filepath.Join(dir, name), []byte(certPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPEM
}

// TestAlipayPrepayValidationAndExpire 覆盖下单公共校验失败与预下单过期时间分支。
func TestAlipayPrepayValidationAndExpire(t *testing.T) {
	env := startAlipayTestEnv(t, func(e *alipayTestEnv) http.HandlerFunc {
		return alipaySuccessHandler(e, t)
	})

	// 校验失败：缺少 out_trade_no。
	if _, err := env.channel.Prepay(context.Background(), Order{Amount: 100, Subject: "s", Scene: SceneNative}); err == nil {
		t.Fatal("invalid order must fail validation")
	}

	// 预下单携带过期时间 → trade.TimeExpire 被序列化。
	expire := time.Now().Add(15 * time.Minute)
	pre, err := env.channel.Prepay(context.Background(), Order{
		OutTradeNo: "order-exp", Amount: 1500, Subject: "过期", Scene: SceneNative, ExpireAt: expire,
	})
	if err != nil || pre.CodeURL == "" {
		t.Fatalf("prepay with expire: %+v, %v", pre, err)
	}
}

// TestNewAlipayBadPublicKey 覆盖公钥解析失败分支。
func TestNewAlipayBadPublicKey(t *testing.T) {
	priv, _, _ := generateAlipayKeys(t)
	_, err := newAlipay(AlipayConfig{
		Enabled:    true,
		AppID:      "2021000000000001",
		PrivateKey: priv,
		PublicKey:  "-----BEGIN PUBLIC KEY-----\nZm9v\n-----END PUBLIC KEY-----",
	}, time.Second)
	if err == nil {
		t.Fatal("malformed alipay public key must fail")
	}
}

// 说明：alipay.go 中 io.ReadAll 的读错误分支与 http.NewRequestWithContext 错误分支
// 已通过 DownloadBill 分支测试覆盖。其余不可达分支：
// TradePagePay/TradeWapPay/TradeAppPay 的构造错误分支——三者均为纯本地 URL/字符串
// 构造，SDK 在密钥合法（构建通道时已校验）的前提下不会返回错误，测试无法在不
// 修改源码、不伪造 SDK 内部字段的情况下触达，属于逻辑不可达的防御分支。
var _ = io.ReadAll
var _ = errors.New
