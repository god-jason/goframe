# goframe

基于 Gin + GORM 的通用 Go 后端开发框架：模块化单体架构，统一应用生命周期、配置、日志、错误处理、响应格式、认证授权与安全基础设施。

框架不绑定具体业务：业务项目通过实现 `Module` 接口接入，只关注 Service / Domain / Repository。

## 快速开始

```bash
# 启动示例服务（默认 :8080，内存会话 + 内存缓存，不依赖数据库）
go run ./example

# 探活
curl http://localhost:8080/health

# 公开接口
curl http://localhost:8080/api/v1/public/ping

# 登录（示例账号 admin / admin123456）
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin123456"}'

# 使用 access_token 访问受保护接口
curl -s http://localhost:8080/api/v1/user/me -H "Authorization: Bearer <access_token>"

# 权限接口（需要 system:read）
curl -s http://localhost:8080/api/v1/system/info -H "Authorization: Bearer <access_token>"
```

示例配置见 [configs/config.example.yaml](configs/config.example.yaml)，复制为 `configs/config.yaml` 后自动加载。Secret 通过环境变量注入，例如 `JWT_SECRET`、`DB_DSN`。

## 核心能力

- **应用生命周期**：`New` / `Register` / `Init` / `Run` / `Shutdown`，信号触发优雅退出，退出顺序固定且带超时。
- **模块管理**：模块注册、名称与依赖检查、循环依赖检测、拓扑排序初始化、逆序停止。
- **配置管理**：Viper 加载，默认值 → 配置文件 → 环境变量，`${VAR}` 展开，启动强校验，支持日志等级热更新。
- **HTTP 层**：标准库 `http.Server` + Gin，超时、可信代理、请求体与 Query 限制、统一错误响应、分页与排序白名单。
- **认证与授权**：JWT（HS256 白名单、iss/aud/exp/nbf/iat 校验）、刷新令牌轮换与重放检测、会话管理、Argon2id 密码哈希、RBAC + 租户/资源授权。
- **安全基础设施**：安全响应头、CORS、CSRF、限流、审计日志、AES-256-GCM 加密、输入清洗、密钥提供者。
- **基础设施**：GORM（MySQL/PostgreSQL）、缓存（内存/Redis）、本地文件存储、定时任务、进程内事件总线、健康检查、Prometheus 指标。
- **后台协程池**：有界队列与固定 worker、任务 panic 隔离、阻塞/非阻塞提交、优雅排空与统计，避免 goroutine 失控。
- **可测试**：业务逻辑不依赖 Gin/GORM，Repository、Service 与中间件均可独立测试。

## 目录结构

```text
goframe/
├── app/            # 应用生命周期、模块管理、依赖装配
├── config/         # 配置结构、加载、校验、热更新
├── server/         # HTTP Server 与 TLS
├── router/         # API 路由分组、SPA 托管
├── middleware/     # RequestID、Recovery、日志、CORS、安全、认证、限流、CSRF
├── response/       # 统一响应、分页、写入工具
├── errors/         # 稳定错误码与 AppError
├── auth/           # Identity、JWT、密码哈希、Session/RefreshToken
├── permission/     # RBAC、策略、租户与资源授权
├── security/       # crypto / csrf / ratelimit / sanitize / audit / secrets
├── database/       # GORM 封装、事务、健康检查
├── cache/          # Cache 接口、Memory、Redis
├── storage/        # Storage 接口、本地存储
├── scheduler/      # 定时任务接口与 cron 实现
├── pool/           # 有界协程池（后台任务）
├── event/          # 进程内事件总线
├── health/         # 存活/就绪/聚合健康检查
├── monitor/        # Prometheus 指标
├── logger/         # 日志接口与 Zap 实现
├── id/             # UUIDv7
├── example/        # 示例业务模块（Module / Handler / Service / Repository）
└── configs/        # 示例配置
```

## 开发业务模块

```go
type Module struct{ handler *Handler }

func (m *Module) Name() string { return "user" }

func (m *Module) Init(application *app.App) error {
    // 通过 application 获取 DB / Cache / Auth / Logger 等基础设施
    m.handler = NewHandler(NewService(NewRepository(application.DB)))
    return nil
}

func (m *Module) RegisterRoutes(router gin.IRouter) {
    // router 是 /api/v1 分组，只注册本模块路由
    router.POST("/users", m.handler.Create)
}
```

模块可选实现：

```go
func (m *Module) Dependencies() []string          // 声明依赖的模块
func (m *Module) Start(ctx context.Context) error // 后台服务启动
func (m *Module) Stop(ctx context.Context) error  // 后台服务停止
```

## 后台任务与协程池

```go
// 阻塞提交：队列满时等待空位，ctx 取消时返回错误
err := application.Submit(ctx, func(ctx context.Context) error {
    return notify(ctx)
})

// 非阻塞提交：队列满立即返回 pool.ErrQueueFull，适合可丢弃的采集任务
err = application.Pool.TrySubmit(ctx, func(ctx context.Context) error {
    return syncOnce(ctx)
})

stats := application.Pool.Stats() // workers / queued / running / completed / failed / panics
```

任务 panic 会被隔离并记录堆栈；应用关闭时会先停止接收新任务，再排空队列。
协程池参数通过配置 `pool.workers`、`pool.queue_size` 调整。

## 中间件顺序

```text
RequestID → Recovery → Security Headers → Method Filter → CORS → Global Rate Limit
→ Request Limits → CSRF → Access Log → Metrics → 业务路由
认证 / 租户 / 权限中间件由业务模块按分组挂载。
```

## 配置与安全约定

- 生产环境禁止 `server.mode=debug`、禁止默认 JWT Secret。
- 数据库迁移不依赖 `AutoMigrate`，生产使用独立 migration 工具。
- 私有文件通过短期签名 URL 或受控下载接口访问；上传文件不使用客户端原始文件名。
- `/metrics` 与健康检查端点不得无保护暴露到公网。
- `auth.MemorySessionRepository` 仅用于开发/测试，生产必须实现 `auth.SessionRepository` 持久化。
- 审计事件只追加、不修改；`detail` 有大小限制并需过滤敏感字段。

## 常用命令

```bash
go build ./...
go test ./...
go test -race ./...
go vet ./...
golangci-lint run
govulncheck ./...
```

## 版本状态

- 已实现：App 生命周期、模块管理、配置、Gin Server、路由、中间件、统一响应、错误处理、GORM、Zap、健康检查、优雅退出。
- 已实现（安全）：JWT、Refresh Token 轮换与重放检测、Session、Argon2id、RBAC/资源授权、限流、CSRF、安全 Header、审计、AES-GCM、API 输入清洗。
- 已实现（基础设施）：Redis 缓存、文件存储（本地）、Scheduler、EventBus、Prometheus、SPA 托管。
- 后续扩展点：ACME 自动证书、S3/MinIO 存储、OpenTelemetry 链路追踪、GORM SessionRepository、数据库迁移工具、分布式定时任务锁、OpenAPI 文档、MQTT/WebSocket 设备接入。
