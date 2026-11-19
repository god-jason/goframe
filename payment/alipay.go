package payment

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	alipay "github.com/smartwalle/alipay/v3"

	apperrors "github.com/god-jason/goframe/errors"
)

// alipayChannel 是基于官方 SDK smartwalle/alipay/v3 的支付宝适配器。
type alipayChannel struct {
	cfg     AlipayConfig
	timeout time.Duration
	client  *alipay.Client
}

// newAlipay 构建支付宝渠道：加载应用私钥与支付宝公钥（或证书）。
func newAlipay(cfg AlipayConfig, timeout time.Duration) (*alipayChannel, error) {
	pem, err := loadKeyMaterial(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("payment: alipay private key: %w", err)
	}
	client, err := alipay.New(cfg.AppID, string(pem), cfg.Production)
	if err != nil {
		return nil, fmt.Errorf("payment: alipay client: %w", err)
	}

	if cfg.certMode() {
		if err := client.LoadAppPublicCertFromFile(cfg.AppCertFile); err != nil {
			return nil, fmt.Errorf("payment: alipay app cert: %w", err)
		}
		if err := client.LoadAliPayPublicCertFromFile(cfg.AlipayCertFile); err != nil {
			return nil, fmt.Errorf("payment: alipay public cert: %w", err)
		}
		if err := client.LoadAliPayRootCertFromFile(cfg.RootCertFile); err != nil {
			return nil, fmt.Errorf("payment: alipay root cert: %w", err)
		}
	} else {
		publicKey, err := loadKeyMaterial(cfg.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("payment: alipay public key: %w", err)
		}
		if err := client.LoadAliPayPublicKey(string(publicKey)); err != nil {
			return nil, fmt.Errorf("payment: alipay public key: %w", err)
		}
	}

	return &alipayChannel{cfg: cfg, timeout: timeout, client: client}, nil
}

func (a *alipayChannel) Name() ChannelName { return ChannelAlipay }

// callContext 为单次网关调用附加超时。
func (a *alipayChannel) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, a.timeout)
}

// Prepay 支付宝统一下单，支持 native/page/h5/app 场景。
func (a *alipayChannel) Prepay(ctx context.Context, order Order) (*PrepayResult, error) {
	if err := validateOrder(order); err != nil {
		return nil, err
	}
	notifyURL := strings.TrimSpace(order.NotifyURL)
	if notifyURL == "" {
		notifyURL = strings.TrimSpace(a.cfg.NotifyURL)
	}
	if notifyURL == "" {
		return nil, apperrors.BadRequest("payment: alipay notify_url is required")
	}

	ctx, cancel := a.callContext(ctx)
	defer cancel()

	trade := alipay.Trade{
		Subject:     order.Subject,
		OutTradeNo:  order.OutTradeNo,
		TotalAmount: fenToYuan(order.Amount),
		Body:        order.Body,
		NotifyURL:   notifyURL,
	}
	if !order.ExpireAt.IsZero() {
		trade.TimeExpire = order.ExpireAt.Format("2006-01-02 15:04:05")
	}
	result := &PrepayResult{Channel: ChannelAlipay, Scene: order.Scene, OutTradeNo: order.OutTradeNo}

	switch order.Scene {
	case SceneNative:
		trade.ProductCode = "FACE_TO_FACE_PAYMENT"
		resp, err := a.client.TradePreCreate(ctx, alipay.TradePreCreate{Trade: trade})
		if err != nil {
			return nil, wrapAlipayError("alipay precreate", err)
		}
		result.CodeURL = resp.QRCode
		result.Raw = resp
	case ScenePage:
		trade.ProductCode = "FAST_INSTANT_TRADE_PAY"
		trade.ReturnURL = a.resolveReturnURL(order.ReturnURL)
		pageURL, err := a.client.TradePagePay(alipay.TradePagePay{Trade: trade})
		if err != nil {
			return nil, wrapAlipayError("alipay page pay", err)
		}
		result.Redirect = pageURL.String()
		result.Raw = pageURL
	case SceneH5:
		trade.ProductCode = "QUICK_WAP_WAY"
		trade.ReturnURL = a.resolveReturnURL(order.ReturnURL)
		pageURL, err := a.client.TradeWapPay(alipay.TradeWapPay{Trade: trade})
		if err != nil {
			return nil, wrapAlipayError("alipay wap pay", err)
		}
		result.Redirect = pageURL.String()
		result.Raw = pageURL
	case SceneApp:
		trade.ProductCode = "QUICK_MSECURITY_PAY"
		orderStr, err := a.client.TradeAppPay(alipay.TradeAppPay{Trade: trade})
		if err != nil {
			return nil, wrapAlipayError("alipay app pay", err)
		}
		result.OrderStr = orderStr
		result.Raw = orderStr
	default:
		return nil, apperrors.BadRequest("payment: alipay does not support scene: " + string(order.Scene))
	}
	return result, nil
}

// resolveReturnURL 返回同步跳转地址，缺省取渠道配置。
func (a *alipayChannel) resolveReturnURL(order string) string {
	if strings.TrimSpace(order) != "" {
		return order
	}
	return strings.TrimSpace(a.cfg.ReturnURL)
}

// Query 按商户订单号查询支付宝交易。
func (a *alipayChannel) Query(ctx context.Context, outTradeNo string) (*Trade, error) {
	if strings.TrimSpace(outTradeNo) == "" {
		return nil, apperrors.BadRequest("payment: out_trade_no is required")
	}
	ctx, cancel := a.callContext(ctx)
	defer cancel()

	resp, err := a.client.TradeQuery(ctx, alipay.TradeQuery{OutTradeNo: outTradeNo})
	if err != nil {
		return nil, wrapAlipayError("alipay query", err)
	}
	trade := &Trade{
		Channel:       ChannelAlipay,
		OutTradeNo:    resp.OutTradeNo,
		TransactionID: resp.TradeNo,
		State:         mapAlipayTradeState(resp.TradeStatus),
		Payer:         firstNonEmpty(resp.BuyerUserId, resp.BuyerOpenId),
		Raw:           resp,
	}
	if amount, err := yuanToFen(resp.TotalAmount); err == nil {
		trade.Amount = amount
	}
	if payTime, err := time.ParseInLocation("2006-01-02 15:04:05", resp.SendPayDate, time.Local); err == nil {
		trade.SuccessTime = payTime
	}
	return trade, nil
}

// Close 关闭支付宝未支付订单。
func (a *alipayChannel) Close(ctx context.Context, outTradeNo string) error {
	if strings.TrimSpace(outTradeNo) == "" {
		return apperrors.BadRequest("payment: out_trade_no is required")
	}
	ctx, cancel := a.callContext(ctx)
	defer cancel()

	if _, err := a.client.TradeClose(ctx, alipay.TradeClose{OutTradeNo: outTradeNo}); err != nil {
		return wrapAlipayError("alipay close", err)
	}
	return nil
}

// Refund 申请支付宝退款。
func (a *alipayChannel) Refund(ctx context.Context, req RefundRequest) (*RefundResult, error) {
	if err := validateRefund(req); err != nil {
		return nil, err
	}
	ctx, cancel := a.callContext(ctx)
	defer cancel()

	resp, err := a.client.TradeRefund(ctx, alipay.TradeRefund{
		OutTradeNo:   req.OutTradeNo,
		OutRequestNo: req.OutRefundNo,
		RefundAmount: fenToYuan(req.RefundAmount),
		RefundReason: req.Reason,
	})
	if err != nil {
		return nil, wrapAlipayError("alipay refund", err)
	}
	result := &RefundResult{
		Channel:     ChannelAlipay,
		OutTradeNo:  resp.OutTradeNo,
		OutRefundNo: req.OutRefundNo,
		RefundID:    resp.TradeNo,
		State:       StateSuccess,
		Raw:         resp,
	}
	if amount, err := yuanToFen(resp.RefundFee); err == nil {
		result.Amount = amount
	}
	return result, nil
}

// QueryRefund 按商户退款单号查询支付宝退款。
func (a *alipayChannel) QueryRefund(ctx context.Context, outRefundNo string) (*RefundResult, error) {
	if strings.TrimSpace(outRefundNo) == "" {
		return nil, apperrors.BadRequest("payment: out_refund_no is required")
	}
	ctx, cancel := a.callContext(ctx)
	defer cancel()

	resp, err := a.client.TradeFastPayRefundQuery(ctx, alipay.TradeFastPayRefundQuery{
		OutRequestNo: outRefundNo,
	})
	if err != nil {
		return nil, wrapAlipayError("alipay refund query", err)
	}
	result := &RefundResult{
		Channel:     ChannelAlipay,
		OutTradeNo:  resp.OutTradeNo,
		OutRefundNo: resp.OutRequestNo,
		RefundID:    resp.TradeNo,
		State:       StatePending,
		Raw:         resp,
	}
	if resp.RefundStatus == "REFUND_SUCCESS" {
		result.State = StateSuccess
	}
	if amount, err := yuanToFen(resp.RefundAmount); err == nil {
		result.Amount = amount
	}
	if payTime, err := time.ParseInLocation("2006-01-02 15:04:05", resp.GMTRefundPay, time.Local); err == nil {
		result.SuccessTime = payTime
	}
	return result, nil
}

// DownloadBill 下载支付宝业务账单：先获取下载地址，再拉取账单内容。
func (a *alipayChannel) DownloadBill(ctx context.Context, req BillRequest) (*BillFile, error) {
	if req.Date.IsZero() {
		return nil, apperrors.BadRequest("payment: bill date is required")
	}
	ctx, cancel := a.callContext(ctx)
	defer cancel()

	billType := strings.TrimSpace(req.BillType)
	if billType == "" {
		billType = "trade"
	}
	resp, err := a.client.BillDownloadURLQuery(ctx, alipay.BillDownloadURLQuery{
		BillType: billType,
		BillDate: req.Date.Format("2006-01-02"),
	})
	if err != nil {
		return nil, wrapAlipayError("alipay bill query", err)
	}
	file := &BillFile{DownloadURL: resp.BillDownloadURL}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, resp.BillDownloadURL, nil)
	if err != nil {
		return file, nil
	}
	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, apperrors.Unavailable("payment: download alipay bill failed").WithCause(err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return nil, apperrors.Unavailable("payment: download alipay bill failed").WithCause(
			fmt.Errorf("status %d", httpResp.StatusCode))
	}
	content, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, apperrors.Internal("payment: read alipay bill content").WithCause(err)
	}
	file.Content = content
	return file, nil
}

// ParseNotify 校验签名并解析支付宝异步回调。
func (a *alipayChannel) ParseNotify(ctx context.Context, r *http.Request) (*NotifyResult, error) {
	notification, err := a.client.GetTradeNotification(r)
	if err != nil {
		return nil, err
	}

	result := &NotifyResult{
		Channel:       ChannelAlipay,
		Type:          NotifyPayment,
		OutTradeNo:    notification.OutTradeNo,
		TransactionID: notification.TradeNo,
		State:         mapAlipayTradeState(notification.TradeStatus),
		Payer:         firstNonEmpty(notification.BuyerId, notification.BuyerOpenId),
		Raw:           notification,
	}
	if amount, err := yuanToFen(notification.TotalAmount); err == nil {
		result.Amount = amount
	}

	isRefund := notification.OutRequestNo != "" || notification.RefundAmount != ""
	if isRefund {
		result.Type = NotifyRefund
		result.OutRefundNo = notification.OutRequestNo
		if amount, err := yuanToFen(notification.RefundAmount); err == nil {
			result.RefundAmount = amount
		}
		result.IdempotencyKey = notification.OutTradeNo + ":" + notification.OutRequestNo + ":" + string(notification.TradeStatus)
		if refundTime, err := time.ParseInLocation("2006-01-02 15:04:05", notification.GmtRefund, time.Local); err == nil {
			result.SuccessTime = refundTime
		}
		return result, nil
	}

	result.IdempotencyKey = notification.OutTradeNo + ":" + string(notification.TradeStatus)
	if payTime, err := time.ParseInLocation("2006-01-02 15:04:05", notification.GmtPayment, time.Local); err == nil {
		result.SuccessTime = payTime
	}
	return result, nil
}

// mapAlipayTradeState 将支付宝交易状态映射为统一状态。
func mapAlipayTradeState(status alipay.TradeStatus) TradeState {
	switch status {
	case alipay.TradeStatusWaitBuyerPay:
		return StatePending
	case alipay.TradeStatusSuccess, alipay.TradeStatusFinished:
		return StateSuccess
	case alipay.TradeStatusClosed:
		return StateClosed
	default:
		return StateUnknown
	}
}

// fenToYuan 把分转换为支付宝要求的两位小数元字符串。
func fenToYuan(cents int64) string {
	negative := cents < 0
	if negative {
		cents = -cents
	}
	yuan := fmt.Sprintf("%d.%02d", cents/100, cents%100)
	if negative {
		yuan = "-" + yuan
	}
	return yuan
}

// yuanToFen 把元字符串转换为分。
func yuanToFen(yuan string) (int64, error) {
	yuan = strings.TrimSpace(yuan)
	if yuan == "" {
		return 0, strconv.ErrSyntax
	}
	amount, err := strconv.ParseFloat(yuan, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(amount * 100)), nil
}

// wrapAlipayError 把 SDK 错误包装为统一应用错误。
func wrapAlipayError(action string, err error) error {
	return apperrors.Internal("payment: " + action + " failed").WithCause(err)
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
