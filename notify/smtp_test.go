package notify

import (
	"context"
	"strings"
	"testing"
)

func TestNewSMTPValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"missing host", Config{From: "noreply@example.com"}, "host"},
		{"missing from", Config{Host: "smtp.example.com"}, "from"},
		{"invalid port", Config{Host: "smtp.example.com", Port: 70000, From: "noreply@example.com"}, "port"},
		{"invalid from", Config{Host: "smtp.example.com", From: "not-an-address"}, "from"},
		{"incomplete credentials", Config{Host: "smtp.example.com", From: "noreply@example.com", Username: "user"}, "username"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewSMTP(tc.cfg)
			if err == nil {
				t.Fatal("expected config error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestNewSMTPDefaults(t *testing.T) {
	sender, err := NewSMTP(Config{Host: "smtp.example.com", From: "Noreply <noreply@example.com>"})
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}
	if sender.cfg.Port != smtpDefaultPort {
		t.Fatalf("port = %d", sender.cfg.Port)
	}
	if sender.envelopeFrom != "noreply@example.com" {
		t.Fatalf("envelope from = %q", sender.envelopeFrom)
	}
}

func TestSMTPSendRequiresRecipients(t *testing.T) {
	sender, err := NewSMTP(Config{Host: "smtp.example.com", From: "noreply@example.com"})
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}
	err = sender.Send(context.Background(), Message{Subject: "hi"})
	if err == nil || !strings.Contains(err.Error(), "recipients") {
		t.Fatalf("err = %v, want recipients error", err)
	}
}
