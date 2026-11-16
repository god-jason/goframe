package cache

import (
	"context"
	"testing"
	"time"

	"github.com/god-jason/goframe/config"
)

func TestMemoryCacheExistsAfterExpiry(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	if err := c.Set(ctx, "short", payload{Name: "v"}, 10*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	ok, err := c.Exists(ctx, "short")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if ok {
		t.Fatal("expected expired key to be removed")
	}
}

func TestMemoryCacheSetMarshalError(t *testing.T) {
	c := NewMemory()
	defer c.Close()

	if err := c.Set(context.Background(), "bad", make(chan int), time.Minute); err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestNewFallsBackToMemory(t *testing.T) {
	c, err := New(config.RedisConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	if _, ok := c.(*MemoryCache); !ok {
		t.Fatalf("expected memory cache, got %T", c)
	}
}
