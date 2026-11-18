package captcha

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"strings"
	"testing"
	"time"
)

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

func TestGenerateAndVerify(t *testing.T) {
	store := NewMemoryStore()
	manager, err := New(Config{Length: 4, TTL: time.Minute}, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	challenge, err := manager.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if challenge.ID == "" {
		t.Fatal("expected challenge id")
	}
	if challenge.ExpiresIn != 60 {
		t.Fatalf("ExpiresIn = %d, want 60", challenge.ExpiresIn)
	}
	if !bytes.HasPrefix(challenge.Image, pngMagic) {
		t.Fatal("expected PNG magic header")
	}

	img, err := png.Decode(bytes.NewReader(challenge.Image))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if img.Bounds().Dx() != 160 || img.Bounds().Dy() != 60 {
		t.Fatalf("image size = %dx%d, want 160x60", img.Bounds().Dx(), img.Bounds().Dy())
	}

	code, err := store.Get(context.Background(), challenge.ID)
	if err != nil {
		t.Fatalf("read stored code: %v", err)
	}
	if len(code) != 4 {
		t.Fatalf("code length = %d, want 4", len(code))
	}

	if err := manager.Verify(context.Background(), challenge.ID, " "+code+" "); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := manager.Verify(context.Background(), challenge.ID, code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Verify error = %v, want ErrNotFound", err)
	}
}

func TestVerifyWrongCodeConsumesChallenge(t *testing.T) {
	store := NewMemoryStore()
	manager, err := New(Config{Length: 5}, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	challenge, err := manager.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	code, err := store.Get(context.Background(), challenge.ID)
	if err != nil {
		t.Fatalf("read stored code: %v", err)
	}

	wrong := "0" + code[1:]
	if wrong == code {
		wrong = "1" + code[1:]
	}
	if err := manager.Verify(context.Background(), challenge.ID, wrong); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("Verify wrong code error = %v, want ErrInvalidCode", err)
	}
	if err := manager.Verify(context.Background(), challenge.ID, code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Verify after failure error = %v, want ErrNotFound", err)
	}
}

func TestVerifyExpiredChallenge(t *testing.T) {
	store := NewMemoryStore()
	manager, err := New(Config{Length: 4, TTL: 10 * time.Millisecond}, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	challenge, err := manager.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	code, err := store.Get(context.Background(), challenge.ID)
	if err != nil {
		t.Fatalf("read stored code: %v", err)
	}

	time.Sleep(40 * time.Millisecond)
	if err := manager.Verify(context.Background(), challenge.ID, code); !errors.Is(err, ErrExpired) {
		t.Fatalf("Verify expired error = %v, want ErrExpired", err)
	}
}

func TestVerifyMissingChallenge(t *testing.T) {
	manager, err := New(Config{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.Verify(context.Background(), "", "1234"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty id error = %v, want ErrNotFound", err)
	}
	if err := manager.Verify(context.Background(), "unknown", "1234"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreExpirationAndDelete(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	if _, err := store.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing error = %v, want ErrNotFound", err)
	}
	if err := store.Set(ctx, "short", "1234", 5*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	if _, err := store.Get(ctx, "short"); !errors.Is(err, ErrExpired) {
		t.Fatalf("Get expired error = %v, want ErrExpired", err)
	}

	if err := store.Set(ctx, "keep", "5678", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Delete(ctx, "keep"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, "keep"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted error = %v, want ErrNotFound", err)
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	if _, err := New(Config{Length: 11}, nil); err == nil {
		t.Fatal("expected length over 10 to be rejected")
	}
	if _, err := New(Config{Width: 20, Height: 10}, nil); err == nil {
		t.Fatal("expected tiny image size to be rejected")
	}
}

func TestGeneratedCodeContainsDigitsOnly(t *testing.T) {
	store := NewMemoryStore()
	manager, err := New(Config{Length: 6}, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	challenge, err := manager.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	code, err := store.Get(context.Background(), challenge.ID)
	if err != nil {
		t.Fatalf("read stored code: %v", err)
	}
	if strings.Trim(code, "0123456789") != "" {
		t.Fatalf("code %q contains non-digit characters", code)
	}
}
