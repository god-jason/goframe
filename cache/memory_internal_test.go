package cache

import (
	"context"
	"testing"
	"time"
)

func TestMemoryCacheDeleteCanceledContext(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.Delete(ctx, "k"); err == nil {
		t.Fatal("expected canceled context error on Delete")
	}
}

func TestMemoryCachePing(t *testing.T) {
	c := NewMemory()
	defer c.Close()

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Ping(ctx); err == nil {
		t.Fatal("expected canceled context error on Ping")
	}
}

func TestMemoryCacheExistsMissing(t *testing.T) {
	c := NewMemory()
	defer c.Close()

	ok, err := c.Exists(context.Background(), "missing")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if ok {
		t.Fatal("expected missing key to be absent")
	}
}

func TestMemoryCacheEvictExpiredEntry(t *testing.T) {
	c := NewMemoryWithOptions(MemoryOptions{MaxEntries: 1})
	defer c.Close()
	ctx := context.Background()

	if err := c.Set(ctx, "old", payload{Name: "old"}, 5*time.Millisecond); err != nil {
		t.Fatalf("Set old: %v", err)
	}
	time.Sleep(15 * time.Millisecond)

	if err := c.Set(ctx, "new", payload{Name: "new"}, 0); err != nil {
		t.Fatalf("Set new: %v", err)
	}

	c.mu.RLock()
	_, hasOld := c.items["old"]
	size := len(c.items)
	c.mu.RUnlock()
	if hasOld || size != 1 {
		t.Fatalf("expected expired entry evicted, hasOld=%v size=%d", hasOld, size)
	}
}

func TestMemoryCacheJanitorRemovesExpired(t *testing.T) {
	old := janitorInterval
	janitorInterval = 5 * time.Millisecond
	defer func() { janitorInterval = old }()

	c := NewMemoryWithOptions(MemoryOptions{MaxEntries: 10})
	defer c.Close()
	ctx := context.Background()

	if err := c.Set(ctx, "gone", payload{Name: "v"}, 5*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := c.Set(ctx, "stay", payload{Name: "v"}, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		_, hasGone := c.items["gone"]
		c.mu.RUnlock()
		if !hasGone {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("janitor did not remove expired entry")
}
