package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func newMiniRedisCache(t *testing.T, prefix string) (*RedisCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	c, err := NewRedis(Config{Addr: server.Addr(), PoolSize: 2, KeyPrefix: prefix})
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, server
}

func TestRedisCacheRoundTrip(t *testing.T) {
	c, _ := newMiniRedisCache(t, "test:")
	ctx := context.Background()

	if err := c.Set(ctx, "user:1", payload{Name: "alice"}, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var got payload
	if err := c.Get(ctx, "user:1", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "alice" {
		t.Fatalf("got %q", got.Name)
	}

	ok, err := c.Exists(ctx, "user:1")
	if err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}

	if err := c.Delete(ctx, "user:1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := c.Get(ctx, "user:1", &got); !errors.Is(err, ErrMiss) {
		t.Fatalf("Get after delete = %v, want ErrMiss", err)
	}
	ok, err = c.Exists(ctx, "user:1")
	if err != nil || ok {
		t.Fatalf("Exists after delete = %v, %v", ok, err)
	}

	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if c.Client() == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestRedisCacheDecodeError(t *testing.T) {
	c, _ := newMiniRedisCache(t, "test:")
	ctx := context.Background()

	if err := c.Client().Set(ctx, c.key("bad"), "not-json", 0).Err(); err != nil {
		t.Fatalf("seed raw value: %v", err)
	}
	var got payload
	if err := c.Get(ctx, "bad", &got); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestRedisCacheSetMarshalError(t *testing.T) {
	c, _ := newMiniRedisCache(t, "test:")

	if err := c.Set(context.Background(), "bad", make(chan int), time.Minute); err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestRedisCacheClosedClientErrors(t *testing.T) {
	server := miniredis.RunT(t)
	c, err := NewRedis(Config{Addr: server.Addr(), KeyPrefix: "test:"})
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ctx := context.Background()
	var got payload
	if err := c.Get(ctx, "k", &got); err == nil {
		t.Fatal("expected Get error")
	}
	if err := c.Set(ctx, "k", payload{}, 0); err == nil {
		t.Fatal("expected Set error")
	}
	if err := c.Delete(ctx, "k"); err == nil {
		t.Fatal("expected Delete error")
	}
	if _, err := c.Exists(ctx, "k"); err == nil {
		t.Fatal("expected Exists error")
	}
	if err := c.Ping(ctx); err == nil {
		t.Fatal("expected Ping error")
	}
	if err := c.Publish(ctx, "chan", []byte("x")); err == nil {
		t.Fatal("expected Publish error")
	}
	if _, err := c.Eval(ctx, "return 1", nil); err == nil {
		t.Fatal("expected Eval error")
	}
}

func TestRedisCachePublishEmptyChannel(t *testing.T) {
	c, _ := newMiniRedisCache(t, "test:")

	if err := c.Publish(context.Background(), "", []byte("x")); err == nil {
		t.Fatal("expected empty channel error")
	}
}

func TestRedisCachePublishAndSubscribe(t *testing.T) {
	c, _ := newMiniRedisCache(t, "test:")
	ctx := context.Background()

	pubsub := c.Subscribe(ctx, "events")
	t.Cleanup(func() { _ = pubsub.Close() })
	ch := pubsub.Channel()

	// 等待订阅建立，再发布消息。
	time.Sleep(50 * time.Millisecond)
	if err := c.Publish(ctx, "events", []byte("hello")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case msg := <-ch:
		if msg.Payload != "hello" {
			t.Fatalf("payload = %q", msg.Payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("did not receive published message")
	}
}

func TestRedisCacheEval(t *testing.T) {
	c, _ := newMiniRedisCache(t, "test:")

	result, err := c.Eval(context.Background(), "return 1", nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if n, ok := result.(int64); !ok || n != 1 {
		t.Fatalf("result = %#v, want 1", result)
	}
}

func TestNewRedisPingFailure(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	addr := server.Addr()
	server.Close()

	if c, err := NewRedis(Config{Addr: addr}); err == nil {
		_ = c.Close()
		t.Fatal("expected ping failure")
	}
}

func TestNewWithRedisEnabled(t *testing.T) {
	server := miniredis.RunT(t)

	c, err := New(Config{Enabled: true, Addr: server.Addr(), KeyPrefix: "test:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	if _, ok := c.(*RedisCache); !ok {
		t.Fatalf("expected RedisCache, got %T", c)
	}
}
