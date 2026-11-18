package audit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/god-jason/goframe/logger"
)

func setAuditConfig(t *testing.T, cfg Config) {
	t.Helper()
	old := *GetConfig()
	*GetConfig() = cfg
	t.Cleanup(func() {
		*GetConfig() = old
		_ = Shutdown(context.Background())
	})
}

type captureLogger struct {
	mu      sync.Mutex
	entries [][]logger.Field
}

func (l *captureLogger) record(fields []logger.Field) {
	l.mu.Lock()
	l.entries = append(l.entries, append([]logger.Field(nil), fields...))
	l.mu.Unlock()
}

func (l *captureLogger) Debug(string, ...logger.Field) {}
func (l *captureLogger) Warn(string, ...logger.Field)  {}
func (l *captureLogger) Error(string, ...logger.Field) {}
func (l *captureLogger) Info(_ string, fields ...logger.Field) {
	l.record(fields)
}
func (l *captureLogger) With(...logger.Field) logger.Logger { return l }
func (l *captureLogger) Sync() error                        { return nil }

func (l *captureLogger) lastEntry() []logger.Field {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) == 0 {
		return nil
	}
	return l.entries[len(l.entries)-1]
}

func TestNewLogAuditorDefaultMaxDetail(t *testing.T) {
	if auditor := NewLogAuditor(logger.NewNop(), 0); auditor.maxDetail != 4<<10 {
		t.Fatalf("maxDetail = %d, want 4096", auditor.maxDetail)
	}
	if auditor := NewLogAuditor(logger.NewNop(), -1); auditor.maxDetail != 4<<10 {
		t.Fatalf("maxDetail = %d, want 4096", auditor.maxDetail)
	}
	if auditor := NewLogAuditor(logger.NewNop(), 10); auditor.maxDetail != 10 {
		t.Fatalf("maxDetail = %d, want 10", auditor.maxDetail)
	}
}

func TestLogAuditorSkipsUnmarshalableDetail(t *testing.T) {
	sink := &captureLogger{}
	auditor := NewLogAuditor(sink, 1024)
	if err := auditor.Record(context.Background(), Event{
		Action: "user.update",
		Result: "success",
		Detail: map[string]any{"bad": make(chan int)},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	entry := sink.lastEntry()
	if len(entry) == 0 {
		t.Fatal("expected log entry")
	}
	for _, field := range entry {
		if field.Key == "detail" {
			t.Fatal("detail should be skipped when JSON encoding fails")
		}
	}
}

func TestLogAuditorKeepsCreatedAt(t *testing.T) {
	sink := &captureLogger{}
	auditor := NewLogAuditor(sink, 1024)
	created := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

	if err := auditor.Record(context.Background(), Event{Action: "login", CreatedAt: created}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	found := false
	for _, field := range sink.lastEntry() {
		if field.Key == "created_at" {
			found = true
			if field.Integer != created.UnixNano() {
				t.Fatalf("created_at = %d, want %d", field.Integer, created.UnixNano())
			}
		}
	}
	if !found {
		t.Fatal("missing created_at field")
	}
}

func TestLogAuditorConcurrentRecords(t *testing.T) {
	auditor := NewLogAuditor(logger.NewNop(), 64)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = auditor.Record(context.Background(), Event{
					Action: "user.update",
					Result: "success",
					Detail: map[string]any{"n": j, "payload": "0123456789abcdef0123456789abcdef"},
				})
			}
		}()
	}
	wg.Wait()
}

func TestBootLifecycle(t *testing.T) {
	setAuditConfig(t, Config{Enabled: false})
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup(disabled): %v", err)
	}
	if _, ok := Get().(NopAuditor); !ok {
		t.Fatalf("Get = %T, want NopAuditor", Get())
	}

	cfg := Config{Enabled: true, MaxDetailSize: 32}
	setAuditConfig(t, cfg)
	if GetConfig().MaxDetailSize.Bytes() != 32 {
		t.Fatalf("GetConfig max detail = %d", GetConfig().MaxDetailSize.Bytes())
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	auditor, ok := Get().(*LogAuditor)
	if !ok {
		t.Fatalf("Get = %T, want *LogAuditor", Get())
	}
	if auditor.maxDetail != 32 {
		t.Fatalf("maxDetail = %d, want 32", auditor.maxDetail)
	}

	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if _, ok := Get().(NopAuditor); !ok {
		t.Fatalf("Get after shutdown = %T, want NopAuditor", Get())
	}
}
