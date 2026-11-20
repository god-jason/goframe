package notify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/taskqueue"
)

type recordingSender struct {
	mu   sync.Mutex
	msgs []Message
	err  error
}

func (s *recordingSender) Send(_ context.Context, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msg)
	return s.err
}

func (s *recordingSender) received() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.msgs...)
}

func TestNormalizeAndBuildSender(t *testing.T) {
	got := Config{}.normalize()
	if got.Channel != "smtp" || got.Queue != "notify" || got.MaxRetry != 0 {
		t.Fatalf("normalize = %+v", got)
	}
	if neg := (Config{MaxRetry: -1}).normalize(); neg.MaxRetry != 3 {
		t.Fatalf("negative max_retry normalize = %d", neg.MaxRetry)
	}
	if _, err := (Config{Channel: "carrier-pigeon"}).buildSender(); err == nil {
		t.Fatal("expected error for unknown channel")
	}
	if _, err := (Config{Channel: "webhook"}).buildSender(); err == nil {
		t.Fatal("expected error for invalid webhook url")
	}
	sender, err := (Config{Channel: "smtp", SMTP: SMTPConfig{Host: "smtp.example.com", From: "noreply@example.com"}}).buildSender()
	if err != nil || sender == nil {
		t.Fatalf("smtp build: %v", err)
	}
}

func TestSendFallbackDirect(t *testing.T) {
	defer SetSender(nil)
	SetSender(nil)
	if err := Send(context.Background(), Message{To: []string{"a@b.c"}}); err == nil {
		t.Fatal("expected error without sender")
	}

	fake := &recordingSender{}
	SetSender(fake)
	msg := Message{To: []string{"user@example.com"}, Subject: "hi"}
	if err := Send(context.Background(), msg); err != nil {
		t.Fatalf("direct send: %v", err)
	}
	if got := fake.received(); len(got) != 1 || got[0].Subject != "hi" {
		t.Fatalf("received = %+v", got)
	}
}

func TestHandleQueueTask(t *testing.T) {
	defer SetSender(nil)

	if err := handleQueueTask(context.Background(), []byte("{")); err == nil {
		t.Fatal("expected decode error")
	}

	SetSender(nil)
	payload, _ := json.Marshal(Message{Subject: "x"})
	if err := handleQueueTask(context.Background(), payload); err == nil {
		t.Fatal("expected error without sender")
	}

	fake := &recordingSender{}
	SetSender(fake)
	if err := handleQueueTask(context.Background(), payload); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if got := fake.received(); len(got) != 1 || got[0].Subject != "x" {
		t.Fatalf("received = %+v", got)
	}
}

func TestSendViaTaskQueue(t *testing.T) {
	defer SetSender(nil)
	fake := &recordingSender{}
	SetSender(fake)

	mr := miniredis.RunT(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "taskqueue:\n" +
		"  enabled: true\n" +
		"  addr: " + mr.Addr() + "\n" +
		"  queues:\n" +
		"    default: 3\n" +
		"    notify: 5\n" +
		"notify:\n" +
		"  enabled: true\n" +
		"  channel: smtp\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config.SetFile(path)
	defer func() {
		_ = taskqueue.Shutdown(context.Background())
		config.SetFile("")
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := taskqueue.Startup(context.Background()); err != nil {
		t.Fatalf("taskqueue startup: %v", err)
	}

	msg := Message{To: []string{"user@example.com"}, Subject: "queued", Body: "hello"}
	if err := Send(context.Background(), msg); err != nil {
		t.Fatalf("enqueue send: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if got := fake.received(); len(got) > 0 {
			if got[0].Subject != "queued" || got[0].Body != "hello" {
				t.Fatalf("received = %+v", got[0])
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for queued notification")
}
