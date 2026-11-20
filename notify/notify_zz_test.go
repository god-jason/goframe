package notify

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/god-jason/goframe/config"
)

// zzSetNotifyConfig 写入临时配置文件并加载 notify 配置段。
func zzSetNotifyConfig(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notify.yaml")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config.SetFile(path)
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	t.Cleanup(func() {
		config.SetFile("")
		if err := config.Reload(); err != nil {
			t.Fatalf("reset config: %v", err)
		}
		SetSender(nil)
	})
}

// TestStartupDisabled 覆盖未启用时 Startup 直接返回。
func TestStartupDisabled(t *testing.T) {
	zzSetNotifyConfig(t, "notify:\n  enabled: false\n")
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if GetSender() != nil {
		t.Fatal("sender should stay nil when disabled")
	}
}

// TestStartupBuildSenderError 覆盖未知通道导致 buildSender 失败的分支。
func TestStartupBuildSenderError(t *testing.T) {
	zzSetNotifyConfig(t, "notify:\n  enabled: true\n  channel: bogus\n")
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected build sender error")
	}
}

// TestStartupShutdownLifecycle 覆盖启用后构建发送器与 Shutdown 清空。
func TestStartupShutdownLifecycle(t *testing.T) {
	zzSetNotifyConfig(t, "notify:\n  enabled: true\n  channel: webhook\n  webhook:\n    url: http://127.0.0.1:9/hook\n")
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if GetSender() == nil {
		t.Fatal("sender should be set after startup")
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if GetSender() != nil {
		t.Fatal("sender should be cleared after shutdown")
	}
}

// TestSMTPSendUsesContextDeadline 覆盖 ctx 截止时间早于默认超时时采用 ctx 截止时间的分支。
func TestSMTPSendUsesContextDeadline(t *testing.T) {
	server := newFakeSMTPServer(t, fakeSMTPSettings{})
	sender := newTestSMTPSender(t, server, SMTPConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sender.Send(ctx, Message{To: []string{"a@example.com"}, Body: "x"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

// TestSMTPSendWriteBodyFailure 覆盖写入正文阶段连接被对端重置的失败分支。
func TestSMTPSendWriteBodyFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		reply := func(line string) {
			_, _ = writer.WriteString(line + "\r\n")
			_ = writer.Flush()
		}
		reply("220 fake smtp")
		for {
			raw, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line := strings.ToUpper(strings.TrimRight(raw, "\r\n"))
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				reply("250 fake")
			case strings.HasPrefix(line, "MAIL FROM"):
				reply("250 ok")
			case strings.HasPrefix(line, "RCPT TO"):
				reply("250 ok")
			case line == "DATA":
				reply("354 go ahead")
				// 立即以 RST 关闭，使客户端写正文时报错。
				if tcp, ok := conn.(*net.TCPConn); ok {
					_ = tcp.SetLinger(0)
				}
				return
			case line == "QUIT":
				reply("221 bye")
				return
			default:
				reply("250 ok")
			}
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	sender, err := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: port, From: "noreply@example.com"})
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}
	body := strings.Repeat("body line for smtp write failure\n", 40000)
	err = sender.Send(context.Background(), Message{To: []string{"a@example.com"}, Body: body})
	if err == nil || !strings.Contains(err.Error(), "body") {
		t.Fatalf("err = %v, want write body error", err)
	}
}

// TestWebhookSendCreateRequestError 覆盖请求构造失败分支。
// 该分支在公开 API 下不可达（NewWebhook 已校验 URL），这里通过同包直接构造非法 URL 的发送器触发。
func TestWebhookSendCreateRequestError(t *testing.T) {
	sender := &WebhookSender{url: "http://exa mple.com/hook"}
	err := sender.Send(context.Background(), Message{To: []string{"a@example.com"}})
	if err == nil || !strings.Contains(err.Error(), "create webhook request") {
		t.Fatalf("err = %v, want create request error", err)
	}
}
