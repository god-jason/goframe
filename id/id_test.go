package id

import (
	"testing"
)

func TestNewIsUnique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		value := New()
		if value == "" {
			t.Fatal("id must not be empty")
		}
		if _, exists := seen[value]; exists {
			t.Fatalf("duplicate id generated: %s", value)
		}
		seen[value] = struct{}{}
		if !IsValid(value) {
			t.Fatalf("generated id is not valid: %s", value)
		}
	}
}

func TestParseAndValidate(t *testing.T) {
	value := New()
	if _, err := Parse(value); err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	if IsValid("not-a-uuid") {
		t.Fatal("expected invalid uuid to be rejected")
	}
	if IsValid("") {
		t.Fatal("expected empty string to be rejected")
	}
}
