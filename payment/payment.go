package payment

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/god-jason/goframe/cache"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/lock"
)

// ChannelName 是支付渠道名。
type ChannelName string

const (
	// ChannelWechat 微信支付渠道。
	ChannelWechat ChannelName = "wechat"
	// ChannelAlipay 支付宝渠道。
	ChannelAlipay ChannelName = "alipay"
)

// Scene 是支付场景。
type Scene string

const (
	// SceneNative 微信扫码支付（仅微信）。
	SceneNative Scene = "native"
	// SceneJSAPI 微信 JSAPI/小程序支付（仅微信）。
	SceneJSAPI Scene = "jsapi"
	// SceneH5 手机网站支付（微信 H5 / 支付宝 WAP）。
	SceneH5 Scene = "h5"
	// SceneApp APP 支付。
	SceneApp Scene = "app"
	// ScenePage 电脑网站支付（仅支付宝）。
	ScenePage Scene = "page"
)

// Order 是统一下单请求，金额单位为分。
type Order struct {
	// OutTradeNo 是商户订单号，必填且全局唯一。
	OutTradeNo string
	// Amount 是订单金额，单位分，必须大于 0。
	Amount int64
	// Subject 是订单标题，必填。
	Subject string
	// Body 是订单描述。
	Body string
	// NotifyURL 是异步回调地址；为空时使用渠道默认配置。
	NotifyURL string
	// ReturnURL 是同步跳转地址（支付宝 page/h5）；为空时使用渠道默认配置。
	ReturnURL string
	// Scene 是支付场景，必填。
	Scene Scene
	// OpenID 是微信 JSAPI 场景的用户标识。
	OpenID string
	// ClientIP 是用户端 IP，微信 H5 场景必填。
	ClientIP string
	// ExpireAt 是订单失效时间；零值表示不设置。
	ExpireAt time.Time
	// Extra 是渠道透传扩展字段。
	Extra map[string]string
}

// PrepayResult 是统一下单结果，按场景填充对应字段。
type PrepayResult struct {
	Channel    ChannelName    `json:"channel"`
	Scene      Scene          `json:"scene"`
	OutTradeNo string         `json:"out_trade_no"`
	CodeURL    string         `json:"code_url,omitempty"`     // native 二维码串
	PrepayID   string         `json:"prepay_id,omitempty"`    // jsapi/app 预支付会话标识
	H5URL      string         `json:"h5_url,omitempty"`       // 微信 H5 跳转地址
	OrderStr   string         `json:"order_str,omitempty"`    // 支付宝 APP 支付串
	Redirect   string         `json:"redirect,omitempty"`     // 支付宝 page/h5 跳转 URL
	PayParams  map[string]string `json:"pay_params,omitempty"` // JSAPI/APP 调起支付参数
	Raw        any            `json:"-"`                      // 渠道原始响应
}

// TradeState 是统一交易状态。
type TradeState string

const (
	StatePending  TradeState = "pending"  // 待支付/处理中
	StateSuccess  TradeState = "success"  // 支付成功
	StateRefunding TradeState = "refunding" // 转入退款
	StateClosed   TradeState = "closed"   // 已关闭
	StateFailed   TradeState = "failed"   // 支付失败/退款异常
	StateUnknown  TradeState = "unknown"
)

// Trade 是订单查询结果。
type Trade struct {
	Channel       ChannelName `json:"channel"`
	OutTradeNo    string      `json:"out_trade_no"`
	TransactionID string      `json:"transaction_id"`
	State         TradeState  `json:"state"`
	Amount        int64       `json:"amount"` // 分
	Payer         string      `json:"payer"`
	SuccessTime   time.Time   `json:"success_time"`
	Raw           any         `json:"-"`
}

// RefundRequest 是退款请求，金额单位为分。
type RefundRequest struct {
	OutTradeNo   string
	OutRefundNo  string // 商户退款单号，必填且唯一
	Reason       string
	RefundAmount int64
	TotalAmount  int64 // 原订单总金额
	NotifyURL    string
}

// RefundResult 是退款结果。
type RefundResult struct {
	Channel     ChannelName `json:"channel"`
	OutTradeNo  string      `json:"out_trade_no"`
	OutRefundNo string      `json:"out_refund_no"`
	RefundID    string      `json:"refund_id"`
	State       TradeState  `json:"state"` // success/pending/closed/failed
	Amount      int64       `json:"amount"`
	SuccessTime time.Time   `json:"success_time"`
	Raw         any         `json:"-"`
}

// BillRequest 是账单下载请求。
type BillRequest struct {
	Date     time.Time // 账单日期
	BillType string    // 渠道账单类型；为空使用默认（微信 ALL，支付宝 trade）
}

// BillFile 是账单文件信息。
type BillFile struct {
	DownloadURL string `json:"download_url"`
	HashType    string `json:"hash_type,omitempty"`
	HashValue   string `json:"hash_value,omitempty"`
	Content     []byte `json:"-"`
}

// NotifyType 是回调类型。
type NotifyType string

const (
	NotifyPayment NotifyType = "payment"
	NotifyRefund  NotifyType = "refund"
)

// NotifyResult 是验签解密后的回调结果。
type NotifyResult struct {
	Channel ChannelName `json:"channel"`
	Type    NotifyType  `json:"type"`
	// IdempotencyKey 用于回调去重，由渠道单号与状态组合而成。
	IdempotencyKey string      `json:"idempotency_key"`
	OutTradeNo     string      `json:"out_trade_no"`
	TransactionID  string      `json:"transaction_id"`
	OutRefundNo    string      `json:"out_refund_no"`
	State          TradeState  `json:"state"`
	Amount         int64       `json:"amount"` // 分
	RefundAmount   int64       `json:"refund_amount"`
	Payer          string      `json:"payer"`
	SuccessTime    time.Time   `json:"success_time"`
	Raw            any         `json:"-"`
}

// NotifyHandler 处理验签并去重后的回调。返回 error 时渠道将收到失败应答并重试。
type NotifyHandler func(ctx context.Context, result *NotifyResult) error

// Channel 是支付渠道统一接口，由微信/支付宝适配器实现。
type Channel interface {
	// Name 返回渠道名。
	Name() ChannelName
	// Prepay 统一下单。
	Prepay(ctx context.Context, order Order) (*PrepayResult, error)
	// Query 按商户订单号查询交易。
	Query(ctx context.Context, outTradeNo string) (*Trade, error)
	// Close 关闭未支付订单。
	Close(ctx context.Context, outTradeNo string) error
	// Refund 申请退款。
	Refund(ctx context.Context, req RefundRequest) (*RefundResult, error)
	// QueryRefund 按商户退款单号查询退款。
	QueryRefund(ctx context.Context, outRefundNo string) (*RefundResult, error)
	// DownloadBill 下载对账账单。
	DownloadBill(ctx context.Context, req BillRequest) (*BillFile, error)
	// ParseNotify 校验并解析渠道异步回调请求。
	ParseNotify(ctx context.Context, r *http.Request) (*NotifyResult, error)
}

// Manager 聚合已启用的渠道、回调幂等与分发逻辑。
type Manager struct {
	channels      map[ChannelName]Channel
	locker        lock.Locker
	store         cache.Cache
	doneTTL       time.Duration
	processingTTL time.Duration
}

// notifyHandlers 是业务回调处理器注册表。独立于 Manager，
// 使业务可在组件启动前后任意时机调用 OnNotify。
var notifyHandlers = struct {
	mu       sync.RWMutex
	byName   map[ChannelName][]NotifyHandler
}{byName: make(map[ChannelName][]NotifyHandler)}

// OnNotify 为指定渠道注册回调处理器。可注册多个，按注册顺序执行，
// 任一返回错误即短路并向渠道应答失败。
func OnNotify(name ChannelName, handlers ...NotifyHandler) {
	if len(handlers) == 0 {
		return
	}
	notifyHandlers.mu.Lock()
	defer notifyHandlers.mu.Unlock()
	notifyHandlers.byName[name] = append(notifyHandlers.byName[name], handlers...)
}

// handlersFor 返回指定渠道的处理器快照。
func handlersFor(name ChannelName) []NotifyHandler {
	notifyHandlers.mu.RLock()
	defer notifyHandlers.mu.RUnlock()
	return append([]NotifyHandler(nil), notifyHandlers.byName[name]...)
}

// clearHandlers 清空处理器，仅供测试使用。
func clearHandlers() {
	notifyHandlers.mu.Lock()
	defer notifyHandlers.mu.Unlock()
	notifyHandlers.byName = make(map[ChannelName][]NotifyHandler)
}

// Channel 返回指定渠道；未启用时返回 nil。
func (m *Manager) Channel(name ChannelName) Channel {
	if m == nil {
		return nil
	}
	return m.channels[name]
}

// Wechat 返回微信渠道；未启用时返回 nil。
func (m *Manager) Wechat() Channel { return m.Channel(ChannelWechat) }

// Alipay 返回支付宝渠道；未启用时返回 nil。
func (m *Manager) Alipay() Channel { return m.Channel(ChannelAlipay) }

// validateOrder 校验下单请求的公共必填项。
func validateOrder(order Order) error {
	if order.OutTradeNo == "" {
		return apperrors.BadRequest("payment: order.out_trade_no is required")
	}
	if order.Amount <= 0 {
		return apperrors.BadRequest("payment: order.amount must be positive")
	}
	if order.Subject == "" {
		return apperrors.BadRequest("payment: order.subject is required")
	}
	if order.Scene == "" {
		return apperrors.BadRequest("payment: order.scene is required")
	}
	return nil
}

// validateRefund 校验退款请求的必填项。
func validateRefund(req RefundRequest) error {
	if req.OutRefundNo == "" {
		return apperrors.BadRequest("payment: refund.out_refund_no is required")
	}
	if req.OutTradeNo == "" {
		return apperrors.BadRequest("payment: refund.out_trade_no is required")
	}
	if req.RefundAmount <= 0 {
		return apperrors.BadRequest("payment: refund.refund_amount must be positive")
	}
	if req.TotalAmount <= 0 {
		return apperrors.BadRequest("payment: refund.total_amount must be positive")
	}
	return nil
}
