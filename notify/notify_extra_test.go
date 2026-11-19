package notify

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTPSettings 控制本地假 SMTP 服务器的行为。
type fakeSMTPSettings struct {
	greeting            string
	greetingDelay       time.Duration
	advertiseStartTLS   bool
	closeOnStartTLS     bool
	authCode            int
	mailCode            int
	rcptCode            int
	dataCode            int
	quitCode            int
	closeAfterDataReply bool
}

type fakeSMTPServer struct {
	listener net.Listener
	settings fakeSMTPSettings
	closed   chan struct{}

	mu         sync.Mutex
	messages   []string
	recipients []string
	authCalls  int
	wg         sync.WaitGroup
}

func newFakeSMTPServer(t *testing.T, settings fakeSMTPSettings) *fakeSMTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeSMTPServer{
		listener: listener,
		settings: settings,
		closed:   make(chan struct{}),
	}
	server.wg.Add(1)
	go server.accept()
	t.Cleanup(server.Close)
	return server
}

func (s *fakeSMTPServer) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			s.handle(conn)
		}()
	}
}

func (s *fakeSMTPServer) Close() {
	select {
	case <-s.closed:
		return
	default:
	}
	close(s.closed)
	_ = s.listener.Close()
	s.wg.Wait()
}

func (s *fakeSMTPServer) addr() (string, int) {
	tcp := s.listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", tcp.Port
}

func (s *fakeSMTPServer) firstMessage() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return ""
	}
	return s.messages[0]
}

func (s *fakeSMTPServer) recipientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recipients)
}

func (s *fakeSMTPServer) authCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authCalls
}

func (s *fakeSMTPServer) handle(conn net.Conn) {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	send := func(line string) bool {
		if _, err := writer.WriteString(line + "\r\n"); err != nil {
			return false
		}
		return writer.Flush() == nil
	}

	if s.settings.greetingDelay > 0 {
		select {
		case <-time.After(s.settings.greetingDelay):
		case <-s.closed:
			return
		}
	}
	greeting := s.settings.greeting
	if greeting == "" {
		greeting = "220 fake smtp"
	}
	if !send(greeting) {
		return
	}

	codeOr := func(code, fallback int) int {
		if code == 0 {
			return fallback
		}
		return code
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			features := []string{"fake"}
			if s.settings.advertiseStartTLS {
				features = append(features, "STARTTLS")
			}
			features = append(features, "AUTH PLAIN")
			for i, feature := range features {
				separator := "-"
				if i == len(features)-1 {
					separator = " "
				}
				if !send("250" + separator + feature) {
					return
				}
			}
		case strings.HasPrefix(upper, "HELO"):
			if !send("250 fake") {
				return
			}
		case upper == "STARTTLS":
			if !s.settings.closeOnStartTLS {
				if !send("454 TLS unavailable") {
					return
				}
				continue
			}
			_ = send("220 ready")
			// 关闭连接模拟 TLS 握手失败。
			return
		case strings.HasPrefix(upper, "AUTH"):
			s.mu.Lock()
			s.authCalls++
			s.mu.Unlock()
			if !send(fmt.Sprintf("%d fake", codeOr(s.settings.authCode, 235))) {
				return
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			if !send(fmt.Sprintf("%d fake", codeOr(s.settings.mailCode, 250))) {
				return
			}
		case strings.HasPrefix(upper, "RCPT TO:"):
			s.mu.Lock()
			s.recipients = append(s.recipients, line)
			s.mu.Unlock()
			if !send(fmt.Sprintf("%d fake", codeOr(s.settings.rcptCode, 250))) {
				return
			}
		case upper == "DATA":
			if s.settings.dataCode != 0 && s.settings.dataCode != 354 {
				if !send(fmt.Sprintf("%d fake", s.settings.dataCode)) {
					return
				}
				continue
			}
			if !send("354 send data") {
				return
			}
			if s.settings.closeAfterDataReply {
				return
			}
			var body strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if dataLine == ".\r\n" || dataLine == ".\n" {
					break
				}
				body.WriteString(dataLine)
			}
			s.mu.Lock()
			s.messages = append(s.messages, body.String())
			s.mu.Unlock()
			if !send("250 queued") {
				return
			}
		case upper == "QUIT":
			_ = send(fmt.Sprintf("%d bye", codeOr(s.settings.quitCode, 221)))
			return
		default:
			if !send("500 unknown") {
				return
			}
		}
	}
}

func newTestSMTPSender(t *testing.T, server *fakeSMTPServer, cfg SMTPConfig) *SMTPSender {
	t.Helper()
	host, port := server.addr()
	cfg.Host = host
	cfg.Port = port
	if cfg.From == "" {
		cfg.From = "noreply@example.com"
	}
	sender, err := NewSMTP(cfg)
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}
	return sender
}

func TestSMTPSendSuccess(t *testing.T) {
	server := newFakeSMTPServer(t, fakeSMTPSettings{})
	sender := newTestSMTPSender(t, server, SMTPConfig{Username: "user", Password: "pass"})

	message := Message{
		To:      []string{"Alice <alice@example.com>", "bob@example.com"},
		Subject: "hello\r\nBcc: attacker@example.com",
		Body:    "line1\nline2\r\nline3\rline4",
		HTML:    true,
	}
	if err := sender.Send(context.Background(), message); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if server.authCount() != 1 {
		t.Fatalf("auth calls = %d, want 1", server.authCount())
	}
	if server.recipientCount() != 2 {
		t.Fatalf("recipients = %d, want 2", server.recipientCount())
	}
	payload := server.firstMessage()
	for _, expected := range []string{
		"Content-Type: text/html; charset=UTF-8",
		"To: alice@example.com, bob@example.com",
		"Subject: helloBcc: attacker@example.com",
		"line1\r\nline2\r\nline3\r\nline4",
	} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("payload missing %q: %q", expected, payload)
		}
	}
}

func TestSMTPSendAuthFailure(t *testing.T) {
	server := newFakeSMTPServer(t, fakeSMTPSettings{authCode: 535})
	sender := newTestSMTPSender(t, server, SMTPConfig{Username: "user", Password: "pass"})

	err := sender.Send(context.Background(), Message{To: []string{"a@example.com"}, Body: "x"})
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("err = %v, want auth error", err)
	}
}

func TestSMTPSendConnectFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	sender, err := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: port, From: "noreply@example.com"})
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}
	err = sender.Send(context.Background(), Message{To: []string{"a@example.com"}})
	if err == nil || !strings.Contains(err.Error(), "connect smtp") {
		t.Fatalf("err = %v, want connect error", err)
	}
}

func TestSMTPSendRequiresSTARTTLS(t *testing.T) {
	server := newFakeSMTPServer(t, fakeSMTPSettings{})
	sender := newTestSMTPSender(t, server, SMTPConfig{TLS: true})

	err := sender.Send(context.Background(), Message{To: []string{"a@example.com"}})
	if err == nil || !strings.Contains(err.Error(), "does not support STARTTLS") {
		t.Fatalf("err = %v, want STARTTLS support error", err)
	}
}

func TestSMTPSendSTARTTLSFailure(t *testing.T) {
	server := newFakeSMTPServer(t, fakeSMTPSettings{advertiseStartTLS: true, closeOnStartTLS: true})
	sender := newTestSMTPSender(t, server, SMTPConfig{})

	err := sender.Send(context.Background(), Message{To: []string{"a@example.com"}})
	if err == nil || !strings.Contains(err.Error(), "starttls") {
		t.Fatalf("err = %v, want starttls error", err)
	}
}

func TestSMTPSendGreetingRejected(t *testing.T) {
	server := newFakeSMTPServer(t, fakeSMTPSettings{greeting: "554 go away"})
	sender := newTestSMTPSender(t, server, SMTPConfig{})

	err := sender.Send(context.Background(), Message{To: []string{"a@example.com"}})
	if err == nil || !strings.Contains(err.Error(), "create client") {
		t.Fatalf("err = %v, want create client error", err)
	}
}

func TestSMTPSendCanceledDuringGreeting(t *testing.T) {
	server := newFakeSMTPServer(t, fakeSMTPSettings{greetingDelay: 2 * time.Second})
	sender := newTestSMTPSender(t, server, SMTPConfig{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := sender.Send(ctx, Message{To: []string{"a@example.com"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestSMTPSendStageFailures(t *testing.T) {
	cases := []struct {
		name     string
		settings fakeSMTPSettings
		want     string
	}{
		{name: "mail from rejected", settings: fakeSMTPSettings{mailCode: 550}, want: "mail from"},
		{name: "rcpt rejected", settings: fakeSMTPSettings{rcptCode: 550}, want: "rcpt"},
		{name: "data rejected", settings: fakeSMTPSettings{dataCode: 451}, want: "data"},
		{name: "quit rejected", settings: fakeSMTPSettings{quitCode: 500}, want: "quit"},
		{name: "body write failure", settings: fakeSMTPSettings{closeAfterDataReply: true}, want: "body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newFakeSMTPServer(t, tc.settings)
			sender := newTestSMTPSender(t, server, SMTPConfig{})
			err := sender.Send(context.Background(), Message{To: []string{"a@example.com"}, Body: "x"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestSMTPSendInvalidInput(t *testing.T) {
	server := newFakeSMTPServer(t, fakeSMTPSettings{})
	sender := newTestSMTPSender(t, server, SMTPConfig{})

	if err := sender.Send(context.Background(), Message{To: []string{"not an address"}}); err == nil || !strings.Contains(err.Error(), "invalid recipient") {
		t.Fatalf("err = %v, want invalid recipient error", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sender.Send(ctx, Message{To: []string{"a@example.com"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

type fakeSMSProvider struct {
	err      error
	calls    int
	lastTo   []string
	lastBody string
	lastMeta map[string]string
}

func (p *fakeSMSProvider) Send(_ context.Context, to []string, content string, params map[string]string) error {
	p.calls++
	p.lastTo = to
	p.lastBody = content
	p.lastMeta = params
	return p.err
}

func TestSMSSender(t *testing.T) {
	if err := NewSMS(nil).Send(context.Background(), Message{To: []string{"10086"}}); err == nil {
		t.Fatal("expected nil provider error")
	}

	provider := &fakeSMSProvider{}
	sms := NewSMS(provider)
	if err := sms.Send(context.Background(), Message{Body: "x"}); err == nil || !strings.Contains(err.Error(), "recipients") {
		t.Fatalf("err = %v, want recipients error", err)
	}

	message := Message{To: []string{"10086"}, Body: "code", Metadata: map[string]string{"template": "login"}}
	if err := sms.Send(context.Background(), message); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if provider.calls != 1 || provider.lastBody != "code" || provider.lastMeta["template"] != "login" || len(provider.lastTo) != 1 {
		t.Fatalf("unexpected provider call: %+v", provider)
	}
}

func TestMultiSMSProvider(t *testing.T) {
	ctx := context.Background()

	if err := NewMulti().Send(ctx, []string{"1"}, "x", nil); err == nil || !strings.Contains(err.Error(), "no sms provider") {
		t.Fatalf("err = %v, want no provider error", err)
	}
	if err := NewMulti(nil, nil).Send(ctx, []string{"1"}, "x", nil); err == nil || !strings.Contains(err.Error(), "no sms provider") {
		t.Fatalf("err = %v, want no provider error", err)
	}

	first := &fakeSMSProvider{err: errors.New("first down")}
	second := &fakeSMSProvider{}
	if err := NewMulti(nil, first, second).Send(ctx, []string{"1"}, "x", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("calls = %d/%d, want 1/1", first.calls, second.calls)
	}

	third := &fakeSMSProvider{err: errors.New("third down")}
	err := NewMulti(first, third).Send(ctx, []string{"1"}, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "all sms providers failed") || !strings.Contains(err.Error(), "first down") || !strings.Contains(err.Error(), "third down") {
		t.Fatalf("err = %v, want aggregated provider errors", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := NewMulti(second).Send(canceled, []string{"1"}, "x", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestNewWebhookDefaultsAndTimeout(t *testing.T) {
	headers := map[string]string{"X-Custom": "value"}
	sender, err := NewWebhook(WebhookConfig{URL: "http://example.com/hook", Headers: headers})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	if sender.client.Timeout != webhookDefaultTimeout {
		t.Fatalf("timeout = %v, want %v", sender.client.Timeout, webhookDefaultTimeout)
	}
	headers["X-Custom"] = "mutated"
	if sender.headers["X-Custom"] != "value" {
		t.Fatal("headers should be copied")
	}

	if _, err := NewWebhook(WebhookConfig{URL: "http://[::1"}); err == nil {
		t.Fatal("expected url parse error")
	}
}

func TestWebhookTimeoutAndCancel(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer close(release)

	sender, err := NewWebhook(WebhookConfig{URL: server.URL, Timeout: 30 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	if err := sender.Send(context.Background(), Message{Body: "x"}); err == nil {
		t.Fatal("expected timeout error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sender.Send(ctx, Message{Body: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
