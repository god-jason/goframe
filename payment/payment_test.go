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
