package id

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewUUIDIsV7(t *testing.T) {
	value := NewUUID()
	if value == uuid.Nil {
		t.Fatal("expected non-nil uuid")
	}
	if value.Version() != 7 {
		t.Fatalf("version = %d, want 7", value.Version())
	}
	if New() == New() {
		t.Fatal("expected unique ids")
	}
}

func TestParse(t *testing.T) {
	value := New()
	parsed, err := Parse(value)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.String() != value {
		t.Fatalf("parsed = %q, want %q", parsed, value)
	}
	if _, err := Parse("invalid"); err == nil {
		t.Fatal("expected parse error")
	}
}
