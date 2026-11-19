package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/downloader"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/app"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/h5"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/jsapi"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"

	apperrors "github.com/god-jason/goframe/errors"
)

// wechatChannel 是基于官方 SDK wechatpay-go 的微信支付 APIv3 适配器。
type wechatChannel struct {
	cfg     WechatConfig
	timeout time.Duration

	client  *core.Client
	native  *native.NativeApiService
	jsapi   *jsapi.JsapiApiService
	h5      *h5.H5ApiService
	app     *app.AppApiService
	refunds *refunddomestic.RefundsApiService

	notifyHandler *notify.Handler
	mgr           *downloader.CertificateDownloaderMgr
	cancel        context.CancelFunc
}

// newWechat 构建微信渠道：加载商户私钥、初始化平台证书自动下载器与 SDK 客户端。
func newWechat(ctx context.Context, cfg WechatConfig, timeout time.Duration) (*wechatChannel, error) {
	pem, err := loadKeyMaterial(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("payment: wechat private key: %w", err)
	}
	privKey, err := utils.LoadPrivateKey(string(pem))
	if err != nil {
		return nil, fmt.Errorf("payment: wechat private key: %w", err)
	}

	// 独立的可取消 context 支撑平台证书定时下载，Shutdown 时释放后台协程。
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	mgr := downloader.NewCertificateDownloaderMgr(runCtx)
	if err := mgr.RegisterDownloaderWithPrivateKey(runCtx, privKey, cfg.SerialNo, cfg.MchID, cfg.APIv3Key); err != nil {
		cancel()
		return nil, fmt.Errorf("payment: wechat certificate downloader: %w", err)
	}

	client, err := core.NewClient(runCtx,
		option.WithWechatPayAutoAuthCipherUsingDownloaderMgr(cfg.MchID, cfg.SerialNo, privKey, mgr))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("payment: wechat client: %w", err)
	}

	verifier := verifiers.NewSHA256WithRSAVerifier(mgr.GetCertificateVisitor(cfg.MchID))
	notifyHandler, err := notify.NewRSANotifyHandler(cfg.APIv3Key, verifier)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("payment: wechat notify handler: %w", err)
	}

	return &wechatChannel{
		cfg:           cfg,
		timeout:       timeout,
		client:        client,
		native:        &native.NativeApiService{Client: client},
		jsapi:         &jsapi.JsapiApiService{Client: client},
		h5:            &h5.H5ApiService{Client: client},
		app:           &app.AppApiService{Client: client},
		refunds:       &refunddomestic.RefundsApiService{Client: client},
		notifyHandler: notifyHandler,
		mgr:           mgr,
		cancel:        cancel,
	}, nil
}

// stop 停止平台证书后台下载协程。
func (w *wechatChannel) stop() {
	if w.mgr != nil {
		w.mgr.Stop()
	}
	if w.cancel != nil {
		w.cancel()
	}
}

func (w *wechatChannel) Name() ChannelName { return ChannelWechat }

// callContext 为单次网关调用附加超时。
func (w *wechatChannel) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, w.timeout)
}

// resolveNotifyURL 返回本单回调地址，缺省取渠道配置。
func (w *wechatChannel) resolveNotifyURL(order string) (string, error) {
	resolved := strings.TrimSpace(order)
	if resolved == "" {
		resolved = strings.TrimSpace(w.cfg.NotifyURL)
	}
	if resolved == "" {
		return "", apperrors.BadRequest("payment: wechat notify_url is required")
	}
	return resolved, nil
}

// Prepay 微信统一下单，支持 native/jsapi/h5/app 场景。
func (w *wechatChannel) Prepay(ctx context.Context, order Order) (*PrepayResult, error) {
	if err := validateOrder(order); err != nil {
		return nil, err
	}
	switch order.Scene {
	case SceneNative, SceneJSAPI, SceneH5, SceneApp:
	default:
		return nil, apperrors.BadRequest("payment: wechat does not support scene: " + string(order.Scene))
	}
	notifyURL, err := w.resolveNotifyURL(order.NotifyURL)
	if err != nil {
		return nil, err
	}
	if order.Scene == SceneH5 && strings.TrimSpace(order.ClientIP) == "" {
		return nil, apperrors.BadRequest("payment: wechat h5 requires order.client_ip")
	}
	if order.Scene == SceneJSAPI && strings.TrimSpace(order.OpenID) == "" {
		return nil, apperrors.BadRequest("payment: wechat jsapi requires order.open_id")
	}

	ctx, cancel := w.callContext(ctx)
	defer cancel()

	amount := order.Amount
	currency := "CNY"
	result := &PrepayResult{Channel: ChannelWechat, Scene: order.Scene, OutTradeNo: order.OutTradeNo}

	switch order.Scene {
	case SceneNative:
		req := native.PrepayRequest{
			Appid:        &w.cfg.AppID,
			Mchid:        &w.cfg.MchID,
			Description:  &order.Subject,
			OutTradeNo:   &order.OutTradeNo,
			NotifyUrl:    &notifyURL,
			Amount:       &native.Amount{Total: &amount, Currency: &currency},
		}
		applyWechatCommon(&req.Attach, &req.TimeExpire, order)
		resp, _, err := w.native.Prepay(ctx, req)
		if err != nil {
			return nil, wrapWechatError("wechat prepay", err)
		}
		result.CodeURL = derefString(resp.CodeUrl)
		result.Raw = resp
	case SceneJSAPI:
		req := jsapi.PrepayRequest{
			Appid:        &w.cfg.AppID,
			Mchid:        &w.cfg.MchID,
			Description:  &order.Subject,
			OutTradeNo:   &order.OutTradeNo,
			NotifyUrl:    &notifyURL,
			Amount:       &jsapi.Amount{Total: &amount, Currency: &currency},
			Payer:        &jsapi.Payer{Openid: &order.OpenID},
		}
		applyWechatCommon(&req.Attach, &req.TimeExpire, order)
		resp, _, err := w.jsapi.PrepayWithRequestPayment(ctx, req)
		if err != nil {
			return nil, wrapWechatError("wechat prepay", err)
		}
		result.PrepayID = derefString(resp.PrepayId)
		result.PayParams = map[string]string{
			"appid":     derefString(resp.Appid),
			"timestamp": derefString(resp.TimeStamp),
			"nonceStr":  derefString(resp.NonceStr),
			"package":   derefString(resp.Package),
			"signType":  derefString(resp.SignType),
			"paySign":   derefString(resp.PaySign),
		}
		result.Raw = resp
	case SceneH5:
		h5Type := "Wap"
		req := h5.PrepayRequest{
			Appid:       &w.cfg.AppID,
			Mchid:       &w.cfg.MchID,
			Description: &order.Subject,
			OutTradeNo:  &order.OutTradeNo,
			NotifyUrl:   &notifyURL,
			Amount:      &h5.Amount{Total: &amount, Currency: &currency},
			SceneInfo: &h5.SceneInfo{
				PayerClientIp: &order.ClientIP,
				H5Info:        &h5.H5Info{Type: &h5Type},
			},
		}
		applyWechatCommon(&req.Attach, &req.TimeExpire, order)
		resp, _, err := w.h5.Prepay(ctx, req)
		if err != nil {
			return nil, wrapWechatError("wechat prepay", err)
		}
		result.H5URL = derefString(resp.H5Url)
		result.Raw = resp
	case SceneApp:
		req := app.PrepayRequest{
			Appid:       &w.cfg.AppID,
			Mchid:       &w.cfg.MchID,
			Description: &order.Subject,
			OutTradeNo:  &order.OutTradeNo,
			NotifyUrl:   &notifyURL,
			Amount:      &app.Amount{Total: &amount, Currency: &currency},
		}
		applyWechatCommon(&req.Attach, &req.TimeExpire, order)
		resp, _, err := w.app.PrepayWithRequestPayment(ctx, req)
		if err != nil {
			return nil, wrapWechatError("wechat prepay", err)
		}
		result.PrepayID = derefString(resp.PrepayId)
		result.PayParams = map[string]string{
			"appid":     w.cfg.AppID,
			"partnerid": derefString(resp.PartnerId),
			"prepayid":  derefString(resp.PrepayId),
			"package":   derefString(resp.Package),
			"noncestr":  derefString(resp.NonceStr),
			"timestamp": derefString(resp.TimeStamp),
			"sign":      derefString(resp.Sign),
		}
		result.Raw = resp
	}
	return result, nil
}

// applyWechatCommon 写入各场景共有的可选字段。
func applyWechatCommon(attach **string, expire **time.Time, order Order) {
	if order.Body != "" {
		*attach = &order.Body
	}
	if !order.ExpireAt.IsZero() {
		t := order.ExpireAt
		*expire = &t
	}
}

// Query 按商户订单号查询微信交易。
func (w *wechatChannel) Query(ctx context.Context, outTradeNo string) (*Trade, error) {
	if strings.TrimSpace(outTradeNo) == "" {
		return nil, apperrors.BadRequest("payment: out_trade_no is required")
	}
	ctx, cancel := w.callContext(ctx)
	defer cancel()

	resp, _, err := w.native.QueryOrderByOutTradeNo(ctx, native.QueryOrderByOutTradeNoRequest{
		OutTradeNo: &outTradeNo,
		Mchid:      &w.cfg.MchID,
	})
	if err != nil {
		return nil, wrapWechatError("wechat query", err)
	}
	return mapWechatTransaction(resp), nil
}

// Close 关闭微信未支付订单。
func (w *wechatChannel) Close(ctx context.Context, outTradeNo string) error {
	if strings.TrimSpace(outTradeNo) == "" {
		return apperrors.BadRequest("payment: out_trade_no is required")
	}
	ctx, cancel := w.callContext(ctx)
	defer cancel()

	if _, err := w.native.CloseOrder(ctx, native.CloseOrderRequest{
		OutTradeNo: &outTradeNo,
		Mchid:      &w.cfg.MchID,
	}); err != nil {
		return wrapWechatError("wechat close", err)
	}
	return nil
}

// Refund 申请微信退款。
func (w *wechatChannel) Refund(ctx context.Context, req RefundRequest) (*RefundResult, error) {
	if err := validateRefund(req); err != nil {
		return nil, err
	}
	ctx, cancel := w.callContext(ctx)
	defer cancel()

	notifyURL := strings.TrimSpace(req.NotifyURL)
	amount := &refunddomestic.AmountReq{
		Refund: &req.RefundAmount,
		Total:  &req.TotalAmount,
	}
	create := refunddomestic.CreateRequest{
		OutTradeNo:  &req.OutTradeNo,
		OutRefundNo: &req.OutRefundNo,
		Amount:      amount,
	}
	if req.Reason != "" {
		create.Reason = &req.Reason
	}
	if notifyURL != "" {
		create.NotifyUrl = &notifyURL
	}

	resp, _, err := w.refunds.Create(ctx, create)
	if err != nil {
		return nil, wrapWechatError("wechat refund", err)
	}
	return mapWechatRefund(resp), nil
}

// QueryRefund 按商户退款单号查询微信退款。
func (w *wechatChannel) QueryRefund(ctx context.Context, outRefundNo string) (*RefundResult, error) {
	if strings.TrimSpace(outRefundNo) == "" {
		return nil, apperrors.BadRequest("payment: out_refund_no is required")
	}
	ctx, cancel := w.callContext(ctx)
	defer cancel()

	resp, _, err := w.refunds.QueryByOutRefundNo(ctx, refunddomestic.QueryByOutRefundNoRequest{
		OutRefundNo: &outRefundNo,
	})
	if err != nil {
		return nil, wrapWechatError("wechat refund query", err)
	}
	return mapWechatRefund(resp), nil
}

// DownloadBill 下载微信交易账单：先申请下载地址，再拉取账单内容。
func (w *wechatChannel) DownloadBill(ctx context.Context, req BillRequest) (*BillFile, error) {
	if req.Date.IsZero() {
		return nil, apperrors.BadRequest("payment: bill date is required")
	}
	ctx, cancel := w.callContext(ctx)
	defer cancel()

	billType := strings.TrimSpace(req.BillType)
	if billType == "" {
		billType = "ALL"
	}
	query := url.Values{}
	query.Set("bill_date", req.Date.Format("2006-01-02"))
	query.Set("bill_type", billType)

	apiResult, err := w.client.Get(ctx, "/v3/bill/tradebill?"+query.Encode())
	if err != nil {
		return nil, wrapWechatError("wechat bill apply", err)
	}
	defer apiResult.Response.Body.Close()

	var payload struct {
		DownloadURL string `json:"download_url"`
		HashType    string `json:"hash_type"`
		HashValue   string `json:"hash_value"`
	}
	if err := json.NewDecoder(apiResult.Response.Body).Decode(&payload); err != nil {
		return nil, apperrors.Internal("payment: decode wechat bill response").WithCause(err)
	}
	if payload.DownloadURL == "" {
		return nil, apperrors.Internal("payment: wechat bill download_url missing")
	}

	file := &BillFile{DownloadURL: payload.DownloadURL, HashType: payload.HashType, HashValue: payload.HashValue}
	contentResult, err := w.client.Get(ctx, payload.DownloadURL)
	if err != nil {
		return nil, wrapWechatError("wechat bill download", err)
	}
	defer contentResult.Response.Body.Close()
	content, err := io.ReadAll(contentResult.Response.Body)
	if err != nil {
		return nil, apperrors.Internal("payment: read wechat bill content").WithCause(err)
	}
	file.Content = content
	return file, nil
}

// ParseNotify 验签并解密微信回调。回调内容以通用 map 解析，
// 依据事件类型区分支付/退款通知并生成幂等键。
func (w *wechatChannel) ParseNotify(ctx context.Context, r *http.Request) (*NotifyResult, error) {
	var content notify.ContentMap
	request, err := w.notifyHandler.ParseNotifyRequest(ctx, r, &content)
	if err != nil {
		return nil, err
	}

	result := &NotifyResult{
		Channel:       ChannelWechat,
		Type:          NotifyPayment,
		OutTradeNo:    contentString(content, "out_trade_no"),
		TransactionID: contentString(content, "transaction_id"),
		Payer:         contentNestedString(content, "payer", "openid"),
		Raw:           content,
	}
	if success, err := time.Parse(time.RFC3339, contentString(content, "success_time")); err == nil {
		result.SuccessTime = success
	}

	if strings.HasPrefix(request.EventType, "REFUND") {
		result.Type = NotifyRefund
		result.OutRefundNo = contentString(content, "out_refund_no")
		status := contentString(content, "refund_status")
		result.State = mapWechatRefundState(status)
		result.Amount = contentNestedInt64(content, "amount", "total")
		result.RefundAmount = contentNestedInt64(content, "amount", "refund")
		result.IdempotencyKey = result.OutRefundNo + ":" + status
		return result, nil
	}

	state := contentString(content, "trade_state")
	result.State = mapWechatTradeState(state)
	result.Amount = contentNestedInt64(content, "amount", "total")
	result.IdempotencyKey = result.OutTradeNo + ":" + state
	return result, nil
}

// mapWechatTradeState 将微信交易状态映射为统一状态。
func mapWechatTradeState(state string) TradeState {
	switch state {
	case "SUCCESS":
		return StateSuccess
	case "REFUND":
		return StateRefunding
	case "NOTPAY", "USERPAYING":
		return StatePending
	case "CLOSED", "REVOKED":
		return StateClosed
	case "PAYERROR":
		return StateFailed
	default:
		return StateUnknown
	}
}

// mapWechatRefundState 将微信退款状态映射为统一状态。
func mapWechatRefundState(status string) TradeState {
	switch status {
	case "SUCCESS":
		return StateSuccess
	case "PROCESSING":
		return StatePending
	case "CLOSED":
		return StateClosed
	case "ABNORMAL":
		return StateFailed
	default:
		return StateUnknown
	}
}

// mapWechatTransaction 将微信交易查询结果转换为统一结构。
func mapWechatTransaction(txn *payments.Transaction) *Trade {
	trade := &Trade{
		Channel:       ChannelWechat,
		OutTradeNo:    derefString(txn.OutTradeNo),
		TransactionID: derefString(txn.TransactionId),
		State:         mapWechatTradeState(derefString(txn.TradeState)),
		Payer:         "",
		Raw:           txn,
	}
	if txn.Amount != nil {
		trade.Amount = derefInt64(txn.Amount.Total)
	}
	if txn.Payer != nil {
		trade.Payer = derefString(txn.Payer.Openid)
	}
	if success := derefString(txn.SuccessTime); success != "" {
		if parsed, err := time.Parse(time.RFC3339, success); err == nil {
			trade.SuccessTime = parsed
		}
	}
	return trade
}

// mapWechatRefund 将微信退款结果转换为统一结构。
func mapWechatRefund(refund *refunddomestic.Refund) *RefundResult {
	result := &RefundResult{
		Channel:     ChannelWechat,
		OutTradeNo:  derefString(refund.OutTradeNo),
		OutRefundNo: derefString(refund.OutRefundNo),
		RefundID:    derefString(refund.RefundId),
		Raw:         refund,
	}
	if refund.Status != nil {
		result.State = mapWechatRefundState(string(*refund.Status))
	}
	if refund.Amount != nil {
		result.Amount = derefInt64(refund.Amount.Refund)
	}
	if refund.SuccessTime != nil {
		result.SuccessTime = *refund.SuccessTime
	}
	return result
}

// wrapWechatError 把 SDK 错误包装为统一应用错误。
func wrapWechatError(action string, err error) error {
	return apperrors.Internal("payment: " + action + " failed").WithCause(err)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// contentString 从回调内容中读取字符串字段。
func contentString(content map[string]interface{}, key string) string {
	value, ok := content[key]
	if !ok {
		return ""
	}
	if str, ok := value.(string); ok {
		return str
	}
	return ""
}

// contentNestedString 读取二级嵌套的字符串字段。
func contentNestedString(content map[string]interface{}, outer, key string) string {
	nested, ok := content[outer].(map[string]interface{})
	if !ok {
		return ""
	}
	return contentString(nested, key)
}

// contentNestedInt64 读取二级嵌套的数值字段（JSON 数字解析为 float64）。
func contentNestedInt64(content map[string]interface{}, outer, key string) int64 {
	nested, ok := content[outer].(map[string]interface{})
	if !ok {
		return 0
	}
	switch value := nested[key].(type) {
	case float64:
		return int64(value)
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}
