package id

import (
	"errors"
	"sync"
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

func TestFallbackWhenV7Fails(t *testing.T) {
	originalV7, originalRandom := newV7, newRandom
	defer func() { newV7, newRandom = originalV7, originalRandom }()

	fixed := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	newV7 = func() (uuid.UUID, error) { return uuid.Nil, errors.New("rand failed") }
	newRandom = func() uuid.UUID { return fixed }

	if got := New(); got != fixed.String() {
		t.Fatalf("New fallback = %q, want %q", got, fixed)
	}
	if got := NewUUID(); got != fixed {
		t.Fatalf("NewUUID fallback = %s, want %s", got, fixed)
	}
}

func TestConcurrentGenerationIsUnique(t *testing.T) {
	const workers, perWorker = 8, 200
	results := make(chan string, workers*perWorker)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				value := New()
				if !IsValid(value) {
					t.Errorf("invalid id generated: %q", value)
					return
				}
				results <- value
			}
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[string]struct{}, workers*perWorker)
	for value := range results {
		if _, exists := seen[value]; exists {
			t.Fatalf("duplicate id: %s", value)
		}
		seen[value] = struct{}{}
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("generated = %d, want %d", len(seen), workers*perWorker)
	}
}

func TestNewUUIDSequence(t *testing.T) {
	first := NewUUID()
	second := NewUUID()
	if first == second {
		t.Fatal("expected unique uuids")
	}
	if first.Version() != 7 || second.Version() != 7 {
		t.Fatalf("versions = %d, %d, want 7", first.Version(), second.Version())
	}
	if !IsValid(New()) || IsValid("123") {
		t.Fatal("unexpected validity results")
	}
}
