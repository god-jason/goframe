package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type capturedWebhook struct {
	method      string
	contentType string
	signature   string
	custom      string
	message     Message
}

func TestWebhookSendPayloadAndSignature(t *testing.T) {
	received := make(chan capturedWebhook, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var message Message
		if err := json.Unmarshal(body, &message); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		received <- capturedWebhook{
			method:      r.Method,
			contentType: r.Header.Get("Content-Type"),
			signature:   r.Header.Get("X-Signature"),
			custom:      r.Header.Get("X-Custom"),
			message:     message,
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sender, err := NewWebhook(WebhookConfig{
		URL:     server.URL,
		Secret:  "s3cr3t",
		Headers: map[string]string{"X-Custom": "yes"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}

	message := Message{
		To:       []string{"user@example.com"},
		Subject:  "subject",
		Body:     "body",
		HTML:     true,
		Metadata: map[string]string{"trace": "abc"},
	}
	if err := sender.Send(context.Background(), message); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-received:
		if got.method != http.MethodPost {
			t.Fatalf("method = %s", got.method)
		}
		if got.contentType != "application/json" {
			t.Fatalf("content-type = %q", got.contentType)
		}
		if got.custom != "yes" {
			t.Fatalf("custom header = %q", got.custom)
		}
		if !reflect.DeepEqual(got.message, message) {
			t.Fatalf("message = %+v", got.message)
		}
		if got.signature != signWebhook(message, "s3cr3t") {
			t.Fatalf("signature = %q", got.signature)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for webhook request")
	}
}

func TestWebhookSendRejectsErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	sender, err := NewWebhook(WebhookConfig{URL: server.URL, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	err = sender.Send(context.Background(), Message{Body: "x"})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want status error", err)
	}
}

func TestNewWebhookValidation(t *testing.T) {
	for _, rawURL := range []string{"", "ftp://example.com/hook", "example.com/hook"} {
		if _, err := NewWebhook(WebhookConfig{URL: rawURL}); err == nil {
			t.Fatalf("expected url %q to be rejected", rawURL)
		}
	}
}

func signWebhook(message Message, secret string) string {
	body, _ := json.Marshal(message)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
