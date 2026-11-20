package web

// 本文件补充 web 包分支覆盖：
//   - ACME 钩子默认实现（acme_dns.go 71/74/77/80）
//   - server.go 挑战/主服务异常路径（124/135/145/186）
//   - web.go MountRoot 失败（178）与数据库健康检查分支（153，假 PG 握手）
//
// 说明：web.go:138、server.go:116 与 acme_dns.go:183/212/244/263 属于
// 逻辑不可达的防御分支：gin 代理校验与 Config.Validate 等价；obtain 成功
// 必产生证书；X509KeyPair/LoadX509KeyPair 内部已解析叶子证书；
// certcrypto.GeneratePrivateKey 无随机数注入点；server.go 的 89/112 两次
// s.challenge 读取会被编译器合并（s 未逃逸），竞态实测 800 次 0 命中。

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"

	"github.com/god-jason/goframe/api"
	"github.com/god-jason/goframe/database"
	"github.com/god-jason/goframe/logger"
)

// captureLogger 记录日志消息，便于等待异步 goroutine 的告警。
type captureLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (l *captureLogger) record(msg string) {
	l.mu.Lock()
	l.msgs = append(l.msgs, msg)
	l.mu.Unlock()
}

func (l *captureLogger) contains(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, msg := range l.msgs {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}

func (l *captureLogger) Debug(msg string, _ ...logger.Field) { l.record(msg) }
func (l *captureLogger) Info(msg string, _ ...logger.Field)  { l.record(msg) }
func (l *captureLogger) Warn(msg string, _ ...logger.Field)  { l.record(msg) }
func (l *captureLogger) Error(msg string, _ ...logger.Field) { l.record(msg) }
func (l *captureLogger) With(_ ...logger.Field) logger.Logger {
	return l
}
func (l *captureLogger) Sync() error { return nil }

// TestACMEDNSHookDefaults 直接调用四个 ACME 钩子的默认实现体，
// CA 目录指向返回 404 的 httptest 服务，注册与申请必然失败但不触网。
func TestACMEDNSHookDefaults(t *testing.T) {
	originalNew, originalSet := newLegoClient, setACMEDNSProvider
	originalRegister, originalObtain := registerACMEAccount, obtainACMECertificate
	t.Cleanup(func() {
		newLegoClient, setACMEDNSProvider = originalNew, originalSet
		registerACMEAccount, obtainACMECertificate = originalRegister, originalObtain
	})

	// lego 强制 HTTPS 且 NewClient 会立即拉取目录，
	// 目录路径返回合法 JSON，其余请求返回 404 使注册/申请失败。
	var ca *httptest.Server
	ca = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Replay-Nonce", "fake-nonce")
		if r.URL.Path == "/directory" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"newNonce":%q,"newAccount":%q,"newOrder":%q}`,
				ca.URL+"/nonce", ca.URL+"/account", ca.URL+"/order")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(ca.Close)

	key, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	user := &legoUser{email: "admin@example.com", key: key}

	legoConfig := lego.NewConfig(user)
	legoConfig.HTTPClient = ca.Client()
	legoConfig.CADirURL = ca.URL + "/directory"
	legoConfig.Certificate.KeyType = certcrypto.EC256

	client, err := newLegoClient(legoConfig)
	if err != nil {
		t.Fatalf("newLegoClient: %v", err)
	}
	if err := setACMEDNSProvider(client, &fakeDNSProvider{}); err != nil {
		t.Fatalf("setACMEDNSProvider: %v", err)
	}
	if _, err := registerACMEAccount(client, user); err == nil {
		t.Fatal("registerACMEAccount should fail against 404 CA")
	}
	if _, err := obtainACMECertificate(client, certificate.ObtainRequest{
		Domains: []string{"example.com"},
		Bundle:  true,
	}); err == nil {
		t.Fatal("obtainACMECertificate should fail against 404 CA")
	}
}

// TestServerChallengeServeStoppedWarning 关闭挑战监听器，
// 触发 challenge.Serve 返回非 ErrServerClosed 错误的告警分支。
func TestServerChallengeServeStoppedWarning(t *testing.T) {
	log := &captureLogger{}
	srv, err := New(Options{
		Config:  testServerConfig(),
		TLS:     acmeTestConfig(t, "http-01", "127.0.0.1:0"),
		Handler: newHandlerEngine(),
		Logger:  log,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if srv.challengeListener == nil {
		t.Fatal("challenge listener missing")
	}
	if err := srv.challengeListener.Close(); err != nil {
		t.Fatalf("close challenge listener: %v", err)
	}
	// 等待 Serve 返回并记录告警，避免与随后的 Shutdown 竞争。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !log.contains("acme http challenge server stopped") {
		time.Sleep(10 * time.Microsecond)
	}
	if !log.contains("acme http challenge server stopped") {
		t.Fatal("challenge serve stop warning not recorded")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := srv.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

// TestServerServePanicConvertedToError 将 http.Server 置空后启动，
// Serve 的 nil 指针 panic 应被 recover 并转换为 Wait 返回的错误。
func TestServerServePanicConvertedToError(t *testing.T) {
	srv, err := New(Options{
		Config:  testServerConfig(),
		Handler: newHandlerEngine(),
		Logger:  logger.NewNop(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.http = nil
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// http 字段为 nil，Shutdown 同样会 panic，只能直接关闭监听器收尾。
	t.Cleanup(func() { _ = srv.listener.Close() })

	if waitErr := srv.Wait(); waitErr == nil || !strings.Contains(waitErr.Error(), "serve panic") {
		t.Fatalf("Wait err = %v, want serve panic", waitErr)
	}
}

// TestServerServeUnexpectedError 外部关闭主监听器，
// Serve 返回非 ErrServerClosed 错误并经 errCh 传给 Wait。
func TestServerServeUnexpectedError(t *testing.T) {
	srv, err := New(Options{
		Config:  testServerConfig(),
		Handler: newHandlerEngine(),
		Logger:  logger.NewNop(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := srv.listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	if waitErr := srv.Wait(); waitErr == nil {
		t.Fatal("Wait should return the serve error")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// TestServerChallengeShutdownCanceled 用挂起 handler 的挑战服务器制造
// 活动连接，取消 ctx 的 Shutdown 应命中 challenge shutdown failed 分支。
func TestServerChallengeShutdownCanceled(t *testing.T) {
	var once sync.Once
	started := make(chan struct{})
	release := make(chan struct{})

	srv, err := New(Options{
		Config:  testServerConfig(),
		Handler: newHandlerEngine(),
		Logger:  logger.NewNop(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.challenge = &http.Server{
		ReadHeaderTimeout: 2 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			once.Do(func() { close(started) })
			<-release
			w.WriteHeader(http.StatusOK)
		}),
	}
	srv.challengeAddr = "127.0.0.1:0"
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := http.Get("http://" + srv.challengeListener.Addr().String() + "/")
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	<-started

	canceled, cancelCanceled := context.WithCancel(context.Background())
	cancelCanceled()
	_ = srv.Shutdown(canceled) // challenge.Shutdown 返回 context.Canceled -> 覆盖 186

	close(release)
	<-requestDone

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := srv.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

// TestStartupMountRootError 登记非法根路径后 Startup 应在 MountRoot 处失败。
func TestStartupMountRootError(t *testing.T) {
	applyConfig(t, "server:\n  port: 8080\n")
	t.Cleanup(api.Reset)
	api.RegisterRoot(http.MethodGet, "bad-path", func(c *gin.Context) {
		c.String(http.StatusOK, "unused")
	})

	err := Startup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "mount framework routes") {
		t.Fatalf("err = %v, want mount framework routes", err)
	}
}

// pgMessage 组装一条 PG 前端/后端协议消息：类型字节 + 含自身的 int32 长度 + 载荷。
func pgMessage(msgType byte, payload ...[]byte) []byte {
	length := 4
	for _, part := range payload {
		length += len(part)
	}
	buf := make([]byte, 0, 1+length)
	buf = append(buf, msgType)
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(length))
	buf = append(buf, header[:]...)
	for _, part := range payload {
		buf = append(buf, part...)
	}
	return buf
}

func pgInt32(value int32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(value))
	return buf[:]
}

func pgCString(text string) []byte { return append([]byte(text), 0) }

// startFakePostgres 启动仅完成握手的最小假 PostgreSQL 服务，
// 使 gorm postgres 驱动（懒连接）在 Ping 时成功建立连接。
func startFakePostgres(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake postgres: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakePostgresConn(conn)
		}
	}()
	return ln.Addr().String()
}

func serveFakePostgresConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))

	// 处理 StartupMessage（含可能的 SSLRequest 探测）。
	for {
		header := make([]byte, 4)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		length := int(binary.BigEndian.Uint32(header))
		if length < 8 || length > 1<<20 {
			return
		}
		body := make([]byte, length-4)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		version := int32(binary.BigEndian.Uint32(body[:4]))
		if version == 80877103 { // SSLRequest：拒绝后等待真正的 StartupMessage
			if _, err := conn.Write([]byte{'N'}); err != nil {
				return
			}
			continue
		}
		if version != 196608 {
			return
		}
		break
	}

	responses := [][]byte{
		pgMessage('R', pgInt32(0)), // AuthenticationOk
		pgMessage('S', pgCString("server_version"), pgCString("16.0")),
		pgMessage('S', pgCString("client_encoding"), pgCString("UTF8")),
		pgMessage('K', pgInt32(1), pgInt32(2)), // BackendKeyData
		pgMessage('Z', []byte{'I'}),            // ReadyForQuery
	}
	for _, msg := range responses {
		if _, err := conn.Write(msg); err != nil {
			return
		}
	}

	for {
		head := make([]byte, 5)
		if _, err := io.ReadFull(conn, head); err != nil {
			return
		}
		length := int(binary.BigEndian.Uint32(head[1:5]))
		if length < 4 || length > 1<<20 {
			return
		}
		body := make([]byte, length-4)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		switch head[0] {
		case 'X': // Terminate
			return
		case 'Q': // 简单查询
			_, _ = conn.Write(pgMessage('C', pgCString("SELECT 1")))
			_, _ = conn.Write(pgMessage('Z', []byte{'I'}))
		case 'P': // Parse
			_, _ = conn.Write(pgMessage('1'))
		case 'B': // Bind
			_, _ = conn.Write(pgMessage('2'))
		case 'D': // Describe
			_, _ = conn.Write(pgMessage('C', pgCString("SELECT 1")))
		case 'S': // Sync
			_, _ = conn.Write(pgMessage('Z', []byte{'I'}))
		}
	}
}

// TestStartupWithDatabaseHealthCheck 借助假 PG 握手让 database.Get() 非空，
// 覆盖 web.Startup 中追加数据库健康检查的分支。
func TestStartupWithDatabaseHealthCheck(t *testing.T) {
	addr := startFakePostgres(t)
	dsn := fmt.Sprintf("postgres://goframe:pwd@%s/goframe?sslmode=disable", addr)
	applyConfig(t, "server:\n  port: 8080\ndatabase:\n  driver: postgres\n  dsn: \""+dsn+"\"\n")

	if err := database.Startup(context.Background()); err != nil {
		t.Fatalf("database startup: %v", err)
	}
	defer func() { _ = database.Shutdown(context.Background()) }()
	if database.Get() == nil {
		t.Fatal("database.Get() must not be nil after startup")
	}

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}


