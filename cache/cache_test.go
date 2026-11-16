package cache

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestMemoryCacheConcurrentAccess(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for worker := 0; worker < 20; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				key := fmt.Sprintf("key:%d:%d", worker, i)
				if err := c.Set(ctx, key, payload{Name: key}, time.Minute); err != nil {
					errs <- err
					return
				}
				var got payload
				if err := c.Get(ctx, key, &got); err != nil {
					errs <- err
					return
				}
				if got.Name != key {
					errs <- fmt.Errorf("value mismatch: %s", key)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestMemoryCacheValueMustBePointer(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	if err := c.Set(ctx, "k", payload{Name: "v"}, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var value payload
	if err := c.Get(ctx, "k", value); err == nil {
		t.Fatal("expected error when value is not a pointer")
	}
}

func TestMemoryCacheWithoutTTL(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	if err := c.Set(ctx, "persistent", payload{Name: "v"}, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var got payload
	if err := c.Get(ctx, "persistent", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
}

func TestMemoryCacheCloseIdempotent(t *testing.T) {
	c := NewMemory()
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestMemoryCacheCanceledContext(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.Set(ctx, "k", payload{}, time.Minute); err == nil {
		t.Fatal("expected canceled context error on Set")
	}
	var got payload
	if err := c.Get(ctx, "k", &got); err == nil {
		t.Fatal("expected canceled context error on Get")
	}
	if _, err := c.Exists(ctx, "k"); err == nil {
		t.Fatal("expected canceled context error on Exists")
	}
}
