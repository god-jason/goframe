package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const (
	smtpDefaultPort    = 587
	smtpDefaultTimeout = 30 * time.Second
)

// Config 是 SMTP 发送配置。
// TLS 为 true 时要求服务器支持 STARTTLS；为 false 时服务器支持则仍会升级。
type Config struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	From     string `mapstructure:"from"`
	TLS      bool   `mapstructure:"tls"`
}

// SMTPSender 是基于 net/smtp 的邮件发送器。
type SMTPSender struct {
	cfg          Config
	fromHeader   string
	envelopeFrom string
}

var _ Sender = (*SMTPSender)(nil)

// NewSMTP 创建 SMTP 发送器，只校验配置，不建立连接。
func NewSMTP(cfg Config) (*SMTPSender, error) {
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.From = strings.TrimSpace(cfg.From)
	if cfg.Host == "" {
		return nil, fmt.Errorf("notify: smtp host is required")
	}
	if cfg.From == "" {
		return nil, fmt.Errorf("notify: smtp from is required")
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("notify: invalid smtp port %d", cfg.Port)
	}
	if cfg.Port == 0 {
		cfg.Port = smtpDefaultPort
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("notify: invalid smtp from: %w", err)
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, fmt.Errorf("notify: smtp username and password must be set together")
	}
	return &SMTPSender{
		cfg:          cfg,
		fromHeader:   from.String(),
		envelopeFrom: from.Address,
	}, nil
}

// Send 发送邮件，服务器支持 STARTTLS 时会自动升级连接。
func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	recipients, err := parseRecipients(msg.To)
	if err != nil {
		return err
	}
	payload := s.buildMessage(msg, recipients)

	dialer := net.Dialer{Timeout: smtpDefaultTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port)))
	if err != nil {
		return fmt.Errorf("notify: connect smtp %s: %w", s.cfg.Host, err)
	}
	defer conn.Close()

	// 上下文取消时关闭连接，解除阻塞中的网络读写。
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	deadline := time.Now().Add(smtpDefaultTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return smtpError(ctx, "create client", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return smtpError(ctx, "starttls", err)
		}
	} else if s.cfg.TLS {
		return fmt.Errorf("notify: smtp server does not support STARTTLS")
	}

	if s.cfg.Username != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return smtpError(ctx, "auth", err)
		}
	}

	if err := client.Mail(s.envelopeFrom); err != nil {
		return smtpError(ctx, "mail from", err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return smtpError(ctx, "rcpt", err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return smtpError(ctx, "data", err)
	}
	if _, err := writer.Write(payload); err != nil {
		_ = writer.Close()
		return smtpError(ctx, "write body", err)
	}
	if err := writer.Close(); err != nil {
		return smtpError(ctx, "close body", err)
	}
	if err := client.Quit(); err != nil {
		return smtpError(ctx, "quit", err)
	}
	return nil
}

// buildMessage 构造符合 RFC 5322 的邮件内容。
func (s *SMTPSender) buildMessage(msg Message, recipients []string) []byte {
	contentType := "text/plain"
	if msg.HTML {
		contentType = "text/html"
	}
	var buf bytes.Buffer
	buf.WriteString("From: " + sanitizeHeader(s.fromHeader) + "\r\n")
	buf.WriteString("To: " + sanitizeHeader(strings.Join(recipients, ", ")) + "\r\n")
	buf.WriteString("Subject: " + sanitizeHeader(msg.Subject) + "\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: " + contentType + "; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(normalizeNewlines(msg.Body))
	return buf.Bytes()
}

func parseRecipients(list []string) ([]string, error) {
	if len(list) == 0 {
		return nil, fmt.Errorf("notify: message has no recipients")
	}
	recipients := make([]string, 0, len(list))
	for _, raw := range list {
		address, err := mail.ParseAddress(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("notify: invalid recipient %q: %w", raw, err)
		}
		recipients = append(recipients, address.Address)
	}
	return recipients, nil
}

// sanitizeHeader 移除换行，避免邮件头注入。
func sanitizeHeader(value string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(value)
}

func normalizeNewlines(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	return strings.ReplaceAll(body, "\n", "\r\n")
}

// smtpError 在上下文已取消时优先返回取消错误。
func smtpError(ctx context.Context, action string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("notify: smtp %s: %w", action, err)
}
