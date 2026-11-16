package audit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/god-jason/goframe/logger"
)

func newAuditLogger(t *testing.T) (logger.Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.log")
	log, err := logger.New(logger.Options{Level: "info", Format: "json", Output: path})
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })
	return log, path
}

func readLog(t *testing.T, log logger.Logger, path string) string {
	t.Helper()
	if err := log.Sync(); err != nil {
		t.Fatalf("sync logger: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return string(data)
}

func TestLogAuditorRecordsEvent(t *testing.T) {
	log, path := newAuditLogger(t)
	auditor := NewLogAuditor(log, 4096)

	err := auditor.Record(context.Background(), Event{
		ActorType:    "user",
		ActorID:      "u1",
		UserID:       "u1",
		TenantID:     "t1",
		Action:       "user.update",
		ResourceType: "user",
		ResourceID:   "u2",
		Result:       "success",
		SourceIP:     "127.0.0.1",
		RequestID:    "rid-1",
		Detail:       map[string]any{"field": "email"},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	content := readLog(t, log, path)
	for _, expected := range []string{
		`"message":"audit event"`,
		`"action":"user.update"`,
		`"actor_id":"u1"`,
		`"tenant_id":"t1"`,
		`"result":"success"`,
		`"request_id":"rid-1"`,
		`"created_at"`,
		`field`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("log does not contain %q: %s", expected, content)
		}
	}
}

func TestLogAuditorTruncatesDetail(t *testing.T) {
	log, path := newAuditLogger(t)
	auditor := NewLogAuditor(log, 8)

	longValue := strings.Repeat("a", 200)
	if err := auditor.Record(context.Background(), Event{
		Action: "user.update",
		Result: "success",
		Detail: map[string]any{"secret": longValue},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	content := readLog(t, log, path)
	if strings.Contains(content, longValue) {
		t.Fatal("expected detail to be truncated")
	}
	if !strings.Contains(content, `"detail"`) {
		t.Fatalf("expected detail field, got %s", content)
	}
}

func TestLogAuditorCanceledContext(t *testing.T) {
	log, _ := newAuditLogger(t)
	auditor := NewLogAuditor(log, 1024)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := auditor.Record(ctx, Event{Action: "x"}); err == nil {
		t.Fatal("expected canceled context error")
	}
}

func TestNopAuditor(t *testing.T) {
	var auditor Auditor = NopAuditor{}
	if err := auditor.Record(context.Background(), Event{}); err != nil {
		t.Fatalf("Record: %v", err)
	}
}
