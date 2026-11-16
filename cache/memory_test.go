package cache

import (
	"context"
	"testing"
	"time"
)

type payload struct {
	Name string `json:"name"`
}

func TestMemoryCacheSetGetDelete(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	if err := c.Set(ctx, "user:1", payload{Name: "alice"}, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	var got payload
	if err := c.Get(ctx, "user:1", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "alice" {
		t.Fatalf("got %q, want alice", got.Name)
	}

	ok, err := c.Exists(ctx, "user:1")
	if err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}

	if err := c.Delete(ctx, "user:1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := c.Get(ctx, "user:1", &got); err != ErrMiss {
		t.Fatalf("Get after delete = %v, want ErrMiss", err)
	}
}

func TestMemoryCacheExpiry(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	if err := c.Set(ctx, "k", payload{Name: "v"}, 10*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	var got payload
	if err := c.Get(ctx, "k", &got); err != ErrMiss {
		t.Fatalf("Get expired = %v, want ErrMiss", err)
	}
}
