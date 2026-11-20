package payment

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/lock"
)

// fakeChannel 是离线测试用的渠道实现。
type fakeChannel struct {
	name      ChannelName
	result    *NotifyResult
	parseErr  error
	parseCall int
}

func (f *fakeChannel) Name() ChannelName { return f.name }

func (f *fakeChannel) ParseNotify(context.Context, *http.Request) (*NotifyResult, error) {
	f.parseCall++
	if f.parseErr != nil {
		return nil, f.parseErr
	}
	return f.result, nil
}

func (f *fakeChannel) Prepay(context.Context, Order) (*PrepayResult, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeChannel) Query(context.Context, string) (*Trade, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeChannel) Close(context.Context, string) error { return errors.New("not implemented") }
func (f *fakeChannel) Refund(context.Context, RefundRequest) (*RefundResult, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeChannel) QueryRefund(context.Context, string) (*RefundResult, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeChannel) DownloadBill(context.Context, BillRequest) (*BillFile, error) {
	return nil, errors.New("not implemented")
}

// memStore 是测试用的内存 cache.Cache。
type memStore struct {
	data map[string]string
}

func newMemStore() *memStore { return &memStore{data: make(map[string]string)} }

func (s *memStore) Get(_ context.Context, key string, value any) error {
	if v, ok := s.data[key]; ok {
		if p, ok := value.(*string); ok {
			*p = v
			return nil
		}
	}
	return cache.ErrMiss
}
func (s *memStore) Set(_ context.Context, key string, value any, _ time.Duration) error {
	s.data[key] = toString(value)
	return nil
}
func (s *memStore) Delete(_ context.Context, key string) error { delete(s.data, key); return nil }
func (s *memStore) Exists(_ context.Context, key string) (bool, error) {
	_, ok := s.data[key]
	return ok, nil
}
func (s *memStore) Close() error { return nil }

func toString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func newTestManager(ch Channel, store cache.Cache) *Manager {
	return &Manager{
		channels:      map[ChannelName]Channel{ch.Name(): ch},
		locker:        lock.NewMemory(),
		store:         store,
		doneTTL:       time.Hour,
		processingTTL: 30 * time.Second,
	}
}

func paymentResult() *NotifyResult {
	return &NotifyResult{
		Channel:        ChannelWechat,
		Type:           NotifyPayment,
		IdempotencyKey: "order-1:SUCCESS",
		OutTradeNo:     "order-1",
		State:          StateSuccess,
		Amount:         100,
	}
}

func TestHandleNotifyDispatchOrder(t *testing.T) {
	clearHandlers()
	defer clearHandlers()

	var calls []string
	OnNotify(ChannelWechat,
		func(_ context.Context, _ *NotifyResult) error { calls = append(calls, "first"); return nil },
		func(_ context.Context, _ *NotifyResult) error { calls = append(calls, "second"); return nil },
	)

	ch := &fakeChannel{name: ChannelWechat, result: paymentResult()}
	manager := newTestManager(ch, newMemStore())
	if _, err := manager.HandleNotify(context.Background(), ChannelWechat, httptest.NewRequest(http.MethodPost, "/", nil)); err != nil {
		t.Fatalf("HandleNotify: %v", err)
	}
	if len(calls) != 2 || calls[0] != "first" || calls[1] != "second" {
		t.Fatalf("handler order = %v", calls)
	}
}

func TestHandleNotifyShortCircuit(t *testing.T) {
	clearHandlers()
	defer clearHandlers()

	second := false
	OnNotify(ChannelWechat,
		func(context.Context, *NotifyResult) error { return errors.New("boom") },
		func(context.Context, *NotifyResult) error { second = true; return nil },
	)

	ch := &fakeChannel{name: ChannelWechat, result: paymentResult()}
	manager := newTestManager(ch, newMemStore())
	if _, err := manager.HandleNotify(context.Background(), ChannelWechat, httptest.NewRequest(http.MethodPost, "/", nil)); err == nil {
		t.Fatal("expected error from failing handler")
	}
	if second {
		t.Fatal("second handler must not run after failure")
	}
	// 失败不得写入 done 标记。
	if _, ok := manager.store.(*memStore).data[notifyKeyPrefix+"wechat:order-1:SUCCESS:done"]; ok {
		t.Fatal("done marker must not be set on failure")
	}
}

func TestHandleNotifyPanicRecovered(t *testing.T) {
	clearHandlers()
	defer clearHandlers()

	OnNotify(ChannelWechat, func(context.Context, *NotifyResult) error { panic("kaboom") })

	ch := &fakeChannel{name: ChannelWechat, result: paymentResult()}
	manager := newTestManager(ch, newMemStore())
	if _, err := manager.HandleNotify(context.Background(), ChannelWechat, httptest.NewRequest(http.MethodPost, "/", nil)); err == nil {
		t.Fatal("panic must convert to error")
	}
}

func TestHandleNotifyNoHandler(t *testing.T) {
	clearHandlers()
	defer clearHandlers()

	ch := &fakeChannel{name: ChannelWechat, result: paymentResult()}
	manager := newTestManager(ch, newMemStore())
	if _, err := manager.HandleNotify(context.Background(), ChannelWechat, httptest.NewRequest(http.MethodPost, "/", nil)); err == nil {
		t.Fatal("missing handler must fail")
	}
}

func TestHandleNotifyDoneIdempotency(t *testing.T) {
	clearHandlers()
	defer clearHandlers()

	handled := 0
	OnNotify(ChannelWechat, func(context.Context, *NotifyResult) error { handled++; return nil })

	ch := &fakeChannel{name: ChannelWechat, result: paymentResult()}
	manager := newTestManager(ch, newMemStore())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if _, err := manager.HandleNotify(context.Background(), ChannelWechat, req); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := manager.HandleNotify(context.Background(), ChannelWechat, req); err != nil {
		t.Fatalf("second: %v", err)
	}
	if handled != 1 {
		t.Fatalf("handler ran %d times, want 1", handled)
	}
	if ch.parseCall != 2 {
		t.Fatalf("ParseNotify calls = %d, want 2 (verify still runs, dedupe after)", ch.parseCall)
	}
}

func TestHandleNotifyProcessingLock(t *testing.T) {
	clearHandlers()
	defer clearHandlers()

	handled := false
	OnNotify(ChannelWechat, func(context.Context, *NotifyResult) error { handled = true; return nil })

	ch := &fakeChannel{name: ChannelWechat, result: paymentResult()}
	manager := newTestManager(ch, newMemStore())

	key := notifyKeyPrefix + "wechat:order-1:SUCCESS:processing"
	unlock, err := manager.locker.TryLock(context.Background(), key, time.Minute)
	if err != nil {
		t.Fatalf("pre-lock: %v", err)
	}
	if _, err := manager.HandleNotify(context.Background(), ChannelWechat, httptest.NewRequest(http.MethodPost, "/", nil)); !errors.Is(err, lock.ErrLocked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
	if handled {
		t.Fatal("handler must not run while locked")
	}
	if err := unlock(context.Background()); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if _, err := manager.HandleNotify(context.Background(), ChannelWechat, httptest.NewRequest(http.MethodPost, "/", nil)); err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	if !handled {
		t.Fatal("handler must run after lock released")
	}
}

func TestHandleNotifyParseError(t *testing.T) {
	ch := &fakeChannel{name: ChannelWechat, parseErr: errors.New("bad sign")}
	manager := newTestManager(ch, newMemStore())
	if _, err := manager.HandleNotify(context.Background(), ChannelWechat, httptest.NewRequest(http.MethodPost, "/", nil)); err == nil {
		t.Fatal("parse failure must return error")
	}
}

func TestManagerChannelAccessors(t *testing.T) {
	ch := &fakeChannel{name: ChannelAlipay}
	manager := newTestManager(ch, nil)
	if manager.Alipay() == nil || manager.Channel(ChannelAlipay) != ch {
		t.Fatal("channel accessor incorrect")
	}
	if manager.Wechat() != nil {
		t.Fatal("disabled channel must be nil")
	}
	var nilManager *Manager
	if nilManager.Channel(ChannelWechat) != nil {
		t.Fatal("nil manager must return nil channel")
	}
}

// TestValidateGuards 覆盖下单与退款公共校验的必填项分支。
func TestValidateGuards(t *testing.T) {
	if err := validateOrder(Order{Amount: 1, Subject: "s", Scene: SceneNative}); err == nil {
		t.Fatal("empty out_trade_no must fail")
	}
	if err := validateOrder(Order{OutTradeNo: "o", Amount: 1, Scene: SceneNative}); err == nil {
		t.Fatal("empty subject must fail")
	}
	if err := validateOrder(Order{OutTradeNo: "o", Amount: 1, Subject: "s"}); err == nil {
		t.Fatal("empty scene must fail")
	}
	if err := validateRefund(RefundRequest{OutRefundNo: "r", RefundAmount: 1, TotalAmount: 1}); err == nil {
		t.Fatal("empty out_trade_no must fail")
	}
	if err := validateRefund(RefundRequest{OutRefundNo: "r", OutTradeNo: "o", TotalAmount: 1}); err == nil {
		t.Fatal("zero refund amount must fail")
	}
	if err := validateRefund(RefundRequest{OutRefundNo: "r", OutTradeNo: "o", RefundAmount: 1}); err == nil {
		t.Fatal("zero total amount must fail")
	}
}

// TestOnNotifyNoHandlers 空处理器列表应直接返回，不注册任何内容。
func TestOnNotifyNoHandlers(t *testing.T) {
	clearHandlers()
	t.Cleanup(clearHandlers)
	OnNotify(ChannelWechat)
	if len(handlersFor(ChannelWechat)) != 0 {
		t.Fatal("OnNotify without handlers must not register anything")
	}
}

// errUnlockLocker 解锁时报错的锁，覆盖 dispatch 的解锁失败日志分支。
type errUnlockLocker struct{}

func (errUnlockLocker) TryLock(context.Context, string, time.Duration) (lock.UnlockFunc, error) {
	return func(context.Context) error { return errors.New("unlock failed") }, nil
}

func (errUnlockLocker) Lock(ctx context.Context, key string, ttl time.Duration) (lock.UnlockFunc, error) {
	return errUnlockLocker{}.TryLock(ctx, key, ttl)
}

// setErrStore 包装内存缓存，仅 Set 报错，覆盖 done 标记失败日志分支。
type setErrStore struct{ *memStore }

func (s setErrStore) Set(context.Context, string, any, time.Duration) error {
	return errors.New("set failed")
}

// TestDispatchFailureBranches 覆盖解锁失败与 done 标记失败两个日志分支。
func TestDispatchFailureBranches(t *testing.T) {
	clearHandlers()
	t.Cleanup(clearHandlers)
	OnNotify(ChannelWechat, func(context.Context, *NotifyResult) error { return nil })

	// 解锁失败：处理仍然成功。
	m := newTestManager(&fakeChannel{name: ChannelWechat, result: paymentResult()}, newMemStore())
	m.locker = errUnlockLocker{}
	if _, err := m.HandleNotify(context.Background(), ChannelWechat, httptest.NewRequest(http.MethodPost, "/", nil)); err != nil {
		t.Fatalf("unlock error must not fail dispatch: %v", err)
	}

	// Set 失败：done 标记失败仅记日志，处理成功。
	m2 := newTestManager(&fakeChannel{name: ChannelWechat, result: paymentResult()}, setErrStore{newMemStore()})
	if _, err := m2.HandleNotify(context.Background(), ChannelWechat, httptest.NewRequest(http.MethodPost, "/", nil)); err != nil {
		t.Fatalf("set error must not fail dispatch: %v", err)
	}
}

// TestHandleNotifyChannelNotEnabled 未启用渠道时回调直接报 NotFound。
func TestHandleNotifyChannelNotEnabled(t *testing.T) {
	m := newTestManager(&fakeChannel{name: ChannelAlipay, result: paymentResult()}, newMemStore())
	if _, err := m.HandleNotify(context.Background(), ChannelWechat, httptest.NewRequest(http.MethodPost, "/", nil)); err == nil {
		t.Fatal("disabled channel must return error")
	}
}
