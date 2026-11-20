# goframe

基于 **Go + Gin + GORM** 的通用后端开发框架，采用**模块化单体（Modular Monolith）**架构，为 IoT 平台、设备管理平台、企业管理系统、后台管理系统和开放 API 服务提供统一的基础设施。

框架不绑定具体业务：业务项目在 `main` 中启动 boot、初始化自己的模块并注册路由，只关注 Service / Domain / Repository，认证、授权、配置、日志、安全、数据库、缓存、存储、调度等能力由框架统一提供。

## 项目定位

- 一套可复用的后端底座，而不是某个业务系统的脚手架。
- 框架项目与业务项目分开管理，业务通过 Go Module 引用框架。
- 优先模块化单体，不提前引入微服务复杂度；后续可按业务边界拆分服务。
- 默认安全、最小权限、显式依赖注入、接口优先。
- 业务逻辑不依赖 Gin、GORM 等具体实现，便于单元测试和替换。

## 设计原则

- 默认安全，最小权限。
- 显式依赖注入，不依赖隐式全局状态。
- 优先接口抽象，不对所有功能过度封装。
- 业务逻辑与基础设施分离。
- 数据库为业务数据的最终可信来源。
- 配置、密钥和业务数据分离管理。
- 所有后台任务必须支持生命周期管理和优雅退出。
- 初始化失败立即终止启动，不允许带着不完整依赖进入运行状态。

## 核心特性

- **组件生命周期**：各包 `init()` 注册 boot 任务，依赖拓扑启动、逆序自动释放、启动失败回滚、panic 隔离。
- **全局实例**：`logger.Get()`、`database.Get()`、`cache.Get()`、`auth.Get()`、`web.API()` 等，无需集中式 App 容器。
- **配置管理**：配置结构体分散在各包并自动注册，默认值 → 配置文件 → `GOFRAME_*` 环境变量，`${VAR}` 展开，原子热更新与日志等级热更新。
- **HTTP 层**：标准库 `http.Server` + Gin，超时、可信代理、请求体与 Query 限制、统一错误响应、分页与排序白名单。
- **认证与授权**：JWT（HS256 白名单、iss/aud/exp/nbf/iat 校验）、刷新令牌轮换与重放检测、会话管理、Argon2id 密码哈希、RBAC + 租户/资源授权。
- **安全基础设施**：CORS（gin-contrib/cors，默认关闭）、CSRF、限流、审计、AES-256-GCM 加密、输入清洗、密钥提供者。
- **文件与图片**：安全上传（大小、扩展名、MIME/Magic Number、路径隔离）、图片等比缩放/裁剪/缩略图/重新编码。
- **Excel 导入导出**：XLSX 读写、XLS（BIFF8 读取 / SpreadsheetML 导出）、多工作表、表头冻结、筛选、列宽、斑马纹。
- **PDF 生成**：标题、段落、表格、图片、页眉页脚、页码、自动分页与表头重复。
- **压缩包处理**：zip / tar / tar.gz / gz 的创建、解压与列表，路径穿越防护、大小与数量限制、符号链接安全校验。
- **基础设施**：GORM（MySQL/PostgreSQL）、数据库迁移工具、缓存（内存/Redis）、本地文件存储、定时任务、进程内事件总线、健康检查、Prometheus 指标。
- **接入协议**：嵌入式 MQTT Broker（MQTT 3.1/3.1.1、QoS 0/1/2、Retained、Will、持久会话）、MQTT 客户端（发布/订阅/TLS/重连）、WebSocket Hub（握手鉴权、Origin 校验、心跳、广播）。
- **HTTPS**：TLS file 与 ACME 自动证书（autocert，HTTP-01 / TLS-ALPN-01）。
- **组件启动注册表**：任务注册、依赖拓扑启动、逆序自动释放、启动失败自动回滚、任务 panic 隔离。
- **后台协程池**：有界队列与固定 worker、任务 panic 隔离、阻塞/非阻塞提交、优雅排空与统计，避免 goroutine 失控。
- **可测试**：Service、Repository、中间件、安全组件均可独立测试。

## 快速开始

环境要求：Go 1.26+（以 go.mod 为准）。

```bash
# 启动示例服务（默认 :8080，内存会话 + 内存缓存，不依赖数据库）
go run ./example

# 探活（公开探针）
curl http://localhost:8080/health/live

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

## 目录结构

### 框架项目

```text
goframe/
├── boot/           # 组件启动注册表（依赖拓扑启动、逆序释放）
├── bootstrap/      # 汇总导入全部标准组件
├── config/         # 配置段注册、加载、环境变量覆盖、热更新
├── web/            # HTTP Server、TLS、Gin 引擎装配、全局中间件链、请求限制、健康检查、指标、pprof
├── api/            # 接口注册与挂载、统一响应、分页、路由分组、SPA 托管、认证/租户/权限守卫（原 response、router 包已并入）
├── idempotency/    # 基于 Idempotency-Key 的 HTTP 幂等中间件与记录存储
├── errors/         # 稳定错误码与 AppError
├── auth/           # Identity、JWT、API Key、TOTP、Session/RefreshToken、GORM 仓库、OIDC
├── license/        # 离线签名许可、功能授权、配额、机器绑定与吊销
├── permission/     # RBAC、策略、租户与资源授权
├── security/       # crypto / csrf（含 Gin 中间件） / ratelimit（含 Gin 中间件） / sanitize / audit / secrets / captcha
├── database/       # GORM 封装、事务、健康检查、migrate 迁移工具
├── cache/          # Cache 接口、Memory、Redis
├── storage/        # Storage 接口、本地存储
├── upload/         # 安全文件上传、类型校验、扫描器接入
├── imagex/         # 图片解码、缩放、裁剪、缩略图、重新编码
├── excel/          # XLSX/XLS 导入导出
├── pdf/            # PDF 生成（文本/表格/图片/页眉页脚）
├── archive/        # 压缩包处理（zip/tar/tar.gz/gz，安全解压）
├── scheduler/      # 定时任务接口与 cron 实现
├── pool/           # 有界协程池（后台任务）
├── event/          # 进程内事件总线 / 事务 Outbox
├── mqtt/           # MQTT 客户端（发布/订阅/TLS/重连）
├── broker/         # 嵌入式 MQTT Broker（会话/QoS/Retained/Will/鉴权）
├── websocket/      # WebSocket Hub（鉴权/Origin/心跳/广播）
├── weixin/         # 微信公众号 / 小程序鉴权、登录与消息校验
├── payment/        # 在线支付网关（微信支付 APIv3 / 支付宝：下单、查询、退款、账单、回调）
├── health/         # 存活/就绪/聚合健康检查
├── monitor/        # Prometheus 指标
├── sysinfo/        # CPU/内存/磁盘实时占用分析
├── trace/          # OpenTelemetry 链路追踪
├── openapi/        # swaggo OpenAPI 文档
├── httpclient/     # HTTP 客户端（重试、熔断、JSON）
├── lock/           # 分布式锁（Redis / 内存）
├── taskqueue/      # 分布式任务队列（Asynq/Redis：并发、队列权重、重试、延迟、运维接口）
├── notify/         # 通知（SMTP / Webhook / SMS，可经任务队列异步派发）
├── cloud/          # 云厂商 SDK：aliyun / tencent / huawei（短信、语音通知/通话、对象存储），按需导入
├── i18n/           # 国际化与语言协商
├── logger/         # 日志接口与 Zap 实现
├── id/             # UUIDv7
├── example/        # 示例程序与业务模块（Handler / Service / Repository）
└── configs/        # 示例配置
```

### 业务项目

框架与业务项目分开管理，通过 Go Module 引用：

```text
iot-platform/
├── cmd/
│   └── server/
│       └── main.go
├── configs/
│   ├── config.yaml
│   └── config.example.yaml
├── internal/
│   ├── bootstrap/
│   │   └── bootstrap.go   # 导入框架组件、初始化业务模块、注册路由
│   └── modules/
│       ├── user/
│       ├── tenant/
│       ├── device/
│       ├── auth/
│       └── system/
├── migrations/
├── storage/
├── web/
│   └── dist/
├── docs/
├── scripts/
├── go.mod
└── README.md
```

## 架构设计

### 分层架构

| 层 | 职责 | 当前实现 |
|---|---|---|
| Application | 组件启动注册、依赖拓扑、全局实例、配置段注册 | `boot/`、`config/`、`bootstrap/` |
| Interface | HTTP 引擎装配、路由、中间件、Handler、DTO | `web/`、`api/` |
| Business | Service、Domain、Repository 接口、业务事件、业务校验 | 业务项目 `internal/modules/*` |
| Infrastructure | GORM Repository、数据库、缓存、存储、日志、调度、事件、加密 | `database/`、`cache/`、`storage/`、`logger/`、`scheduler/`、`event/`、`security/` |
| Runtime | 配置、TLS、健康检查、指标、优雅退出 | `config/`、`web/`、`health/`、`monitor/` |

### 依赖规则

依赖必须遵循单向原则：

- Handler 依赖 Service。
- Service 依赖 Domain 和 Repository 接口。
- Repository 实现依赖 GORM 和 Database。
- Middleware 依赖认证、授权等抽象接口。
- Domain 不依赖 Gin、GORM。
- Service 不依赖 Gin Context。
- Infrastructure 不反向依赖业务 Handler。
- 业务模块不得直接操作其他模块的内部 Repository，应通过对外暴露的 Service 或模块接口协作。

### 技术选型

| 功能 | 技术 | 状态 |
|---|---|---|
| Web 框架 | Gin | 已使用 |
| ORM | GORM v2 | 已使用 |
| 配置管理 | yaml.v3 + fsnotify（自研配置段） | 已使用 |
| 日志 | Zap | 已使用 |
| 参数校验 | go-playground/validator | 通过 Gin Binding 使用 |
| JWT | golang-jwt/jwt/v5 | 已使用 |
| 密码哈希 | Argon2id | 已使用 |
| Redis | go-redis | 已使用 |
| 定时任务 | robfig/cron | 已使用 |
| Metrics | Prometheus | 已使用 |
| ID | UUIDv7 | 已使用 |
| 文件存储 | Local / S3 / MinIO | 已实现（minio-go） |
| 文件上传 | 标准库 + Magic Number + ClamAV | 已实现 |
| 图片处理 | image + golang.org/x/image | 已实现 |
| Excel | xuri/excelize + extrame/xls | 已实现 |
| PDF | signintech/gopdf | 已实现（生成） |
| 压缩包 | 标准库 archive/zip、archive/tar、compress/gzip | 已实现 |
| 数据库迁移 | 内置 migrate（SQL/Go 迁移） | 已实现 |
| 组件生命周期 | 内置 boot 注册表 | 已实现 |
| MQTT 客户端 | eclipse/paho.mqtt.golang | 已实现 |
| MQTT Broker | 标准库 net + 自定义协议编解码 | 已实现 |
| WebSocket | gorilla/websocket + topic 协议 | 已实现 |
| TLS/ACME | 标准库 TLS / autocert / lego | HTTP-01、TLS-ALPN-01、DNS-01 已实现 |
| API 文档 | swaggo + gin-swagger | 已实现 |
| Trace | OpenTelemetry OTLP | 已实现 |
| API Key | 内置（哈希存储、作用域） | 已实现 |
| 分布式锁 | Redis SET NX + Lua 解锁 | 已实现 |
| 可靠事件 | GORM Outbox + 进程内 EventBus | 已实现 |
| 限流 | 进程内令牌桶 / Redis 分布式 | 已实现 |

框架不强制所有依赖必须启用。Redis、对象存储、消息队列等均采用可选适配器。

## boot 启动模型

框架不使用集中式 App 容器。每个组件在自己包内完成三件事：

1. 声明并注册配置段；
2. 用 `init()` 向 `boot` 注册启动/释放任务与依赖；
3. 启动成功后把实例保存为包级全局单例，其他包通过 `Get()` 访问。

### 全局实例

| 组件 | 访问方式 |
|---|---|
| 日志 | `logger.Get()` |
| 数据库 | `database.Get()` |
| 缓存 | `cache.Get()` |
| 文件存储 / 上传 | `storage.Get()` / `upload.Get()` |
| 协程池 / 事件总线 | `pool.Get()` / `event.Get()` |
| 定时任务 | `scheduler.Get()` |
| 任务队列 | `taskqueue.Enqueue()` / `taskqueue.Register()` |
| 任务队列运维 | `taskqueue.InspectQueues()` / `ListTasks()` / `CancelTask()` 等 |
| 通知 | `notify.Send()` / `notify.GetSender()` |
| 云服务（按需导入） | `aliyun.GetSms()` / `tencent.GetVoice()` / `huawei.GetStorage()` / `onenet.GetSms()` / `ctyun.GetStorage()` 等，均实现 `cloud.Sms`/`Voice`/`Storage` 统一接口 |
| MQTT 客户端 / Broker | `mqtt.Get()` / `broker.Get()` |
| WebSocket | `websocket.Get()` |
| 认证 / 授权 | `auth.Get()` / `permission.Get()` |
| 限流 / CSRF / 审计 / 加密 | `ratelimit.Get()` / `csrf.Get()` / `audit.Get()` / `crypto.Get()` |
| HTTP 引擎 / 路由 | `web.Engine()` / `web.API()` |

未启用或未启动的组件返回 `nil`；日志返回空实现，审计返回空实现。

### 组件注册示例

```go
// cache/config.go
var cfg = config.Register("redis", DefaultConfig())

func GetConfig() *Config { return cfg.Get() }

// cache/boot.go
func init() {
    _ = boot.Register("cache", &boot.Module{
        Startup:  Startup,
        Shutdown: Shutdown,
        Depends:  []string{"config"},
    })
}
```

`boot.Startup` 按依赖拓扑启动，`boot.Shutdown` 严格逆序释放。启动失败会自动回滚已启动任务；依赖缺失会告警并跳过；循环依赖返回错误；任务 panic 被隔离为错误。

### 启动与关闭顺序

```text
main 设置配置路径/注入项
→ boot.Startup(ctx)
   config → logger → database → cache → storage → upload
   → event → pool → scheduler → taskqueue → notify → cloud-aliyun → cloud-tencent → cloud-huawei → cloud-onenet → cloud-ctyun
   → broker → mqtt → websocket
   → auth → ratelimit → csrf → audit → crypto → web（仅装配引擎）
→ 业务手动初始化模块并注册路由到 web.API()
→ web.Run(ctx) 最后启动 HTTP 服务
→ 收到退出信号
→ boot.Shutdown(ctx) 逆序释放
```

`web` 的 boot 任务只创建 Gin 引擎、全局中间件、健康检查、指标路由与 HTTP Server，不监听端口；因此业务可以先注册路由，再最后启动服务。

### 业务模块示例

```go
type Module struct{ handler *Handler }

func (m *Module) Init() error {
    // 从全局单例获取基础设施
    m.handler = NewHandler(NewService(database.Get()))
    return nil
}

func (m *Module) RegisterRoutes(r gin.IRouter) {
    r.POST("/users", m.handler.Create)
}
```

启动入口：推荐使用 `boot.Run`，自动监听 SIGINT/SIGTERM、按依赖拓扑启动、退出时带超时逆序 Shutdown：

```go
func main() {
    err := boot.Run(context.Background(), func(ctx context.Context) error {
        module := user.New()
        if err := module.Init(); err != nil {
            return err
        }
        module.RegisterRoutes(web.API())
        return web.Run(ctx) // ctx 收到退出信号后由 web.Run 优雅停止
    })
    if err != nil {
        log.Fatal(err)
    }
}
```

`boot.Run` 内部流程：`signal.NotifyContext` → `boot.Startup` → 运行 `serve`（或等待取消）→ 以 `WithShutdownTimeout`（默认 30s）执行 `boot.Shutdown`。需要手工控制时也可以继续直接调用 `boot.Startup` / `boot.Shutdown`。

完整示例见 `example/main.go`。只需要部分组件时，按需导入对应包；需要全套标准组件时导入 `bootstrap`：

```go
import (
    "github.com/god-jason/goframe/boot"
    _ "github.com/god-jason/goframe/bootstrap"
)
```

### 可选注入

必须在 `boot.Startup` 之前完成：

```go
config.SetFile("configs/config.yaml")

websocket.SetAuthenticator(func(c *gin.Context) (string, error) { ... })
websocket.SetOnMessage(func(ctx context.Context, subject string, payload []byte) error { ... })

broker.SetAuthenticator(broker.AuthFunc(func(ctx context.Context, req broker.ConnectRequest) error { ... }))
broker.SetHooks(broker.Hooks{OnConnect: ...})

auth.SetSessionRepository(customRepository)
upload.SetScanner(customScanner)
permission.Set(customAuthorizer)

web.Configure(web.WithFileSystem(webDist)) // SPA embed 模式
```

## 后台任务与协程池

协程池用于承载审计落盘、通知、数据同步等可异步化的任务，避免每个任务直接 `go func` 造成 goroutine 数量失控。

```go
// 阻塞提交：队列满时等待空位，ctx 取消时返回错误
err := pool.Get().Submit(ctx, func(ctx context.Context) error {
    return notify(ctx)
})

// 非阻塞提交：队列满立即返回 pool.ErrQueueFull，适合可丢弃的采集任务
err = pool.Get().TrySubmit(ctx, func(ctx context.Context) error {
    return syncOnce(ctx)
})

stats := pool.Get().Stats() // workers / queued / running / completed / failed / panics
```

能力说明：

- 固定 worker 数量与有界队列。
- 任务 panic 隔离并记录堆栈，worker 不会退出。
- 关闭时先拒绝新任务，再排空队列；超时后取消任务上下文并给收尾窗口。
- 支持错误回调 `OnError`，便于接入监控告警。

配置：

```yaml
pool:
  workers: 8       # 0 表示按 CPU 核数自动计算
  queue_size: 1024
```

## HTTP 服务与路由

### Server

底层使用标准库 `http.Server`，不使用 Gin 的 `Run()` 作为生产环境入口，支持：

- `host` / `port` / `mode`。
- `read_timeout` / `read_header_timeout` / `write_timeout` / `idle_timeout`。
- `shutdown_timeout`、`max_header_bytes`。
- `trusted_proxies`：只有可信代理才能提供真实客户端 IP。
- `api_prefix` / `api_version`。
- `cors`：跨域白名单、方法、请求头、凭证与预检缓存，默认关闭。

### 路由分组

```text
/api/v1/
├── public/      # 公开接口
├── auth/        # 登录认证
├── user/        # 用户接口
├── admin/       # 管理接口
├── tenant/      # 租户接口
└── system/      # 系统接口
```

业务模块只注册自己的路由，不允许随意修改全局中间件。路由分组只用于组织接口和挂载中间件，不代表最终资源授权。

### 统一响应

```go
type Response[T any] struct {
    Code      int    `json:"code"`
    Message   string `json:"message"`
    Data      T      `json:"data"`
    RequestID string `json:"request_id"`
}

type PageData[T any] struct {
    List     []T   `json:"list"`
    Total    int64 `json:"total"`
    Page     int   `json:"page"`
    PageSize int   `json:"page_size"`
}
```

业务成功码统一为 `0`，HTTP 状态码按实际语义使用：

```text
200 成功      201 创建成功    202 异步任务已接收    204 成功无响应体
400 参数错误  401 未认证      403 无权限            404 资源不存在
409 资源冲突  429 请求过多    500 服务器内部错误
```

错误响应不得包含 SQL、堆栈、内部路径、密钥或数据库连接信息。

### 分页与排序

统一参数：

- `page`
- `page_size`
- `sort`
- `order`

最大 `page_size` 由框架限制为 100；排序字段必须使用白名单映射，不允许将用户输入直接拼接进 SQL。

```go
query := api.PageQuery{Page: 1, PageSize: 20, Sort: "created_at", Order: "desc"}
query.Normalize()
column, direction := query.SortClause(map[string]string{
    "created_at": "created_at",
    "name":       "username",
}, "created_at")
```

### 幂等约定

高风险操作建议支持 `Idempotency-Key`：

- 相同 Key、相同请求：返回首次执行结果。
- 相同 Key、不同请求内容：返回 `409`。
- 执行中：返回处理中状态。
- 失败：按业务策略允许安全重试。

设备命令应使用独立的 `command_id`，不能仅依赖 HTTP 幂等键。HTTP 幂等由 `idempotency` 包提供（`idempotency.Middleware(store, ttl)`），业务可按该约定实现设备侧幂等。

### 中间件顺序

全局链路：

```text
gin.Recovery → CORS（可选） → Trace → CSRF → Global Rate Limit → Method Filter → Request Limits
```

- CORS 仅在 `server.cors.enabled` 为 true 时加入链路，并先于限流与方法过滤处理预检请求。
- 框架不内置 Access Log 与 HTTP 请求指标中间件；如需采集请求数、耗时与在途请求，可在业务入口挂载 `web.Metrics().Middleware()`。

认证、租户、权限中间件由业务模块按分组挂载：

```go
protected := api.Group("",
    api.Authenticate(auth.Get()),
    api.RequireAuth(),
    api.Tenant(tenantResolver),
)
```

- 框架不生成 RequestID，`api.RequestID` 读取客户端 `X-Request-ID`；需要服务端生成时由业务中间件调用 `c.Set(api.RequestIDKey, id)`。
- 登录接口应在认证之前单独限流，示例模块已按 IP 配置登录限流。
- CSRF 仅对携带 CSRF Cookie 且未使用 Bearer Token 的非安全方法生效。
- 框架不内置 Access Log、RequestID、Gzip、Timeout、Security Headers 等通用中间件；CORS 使用 `github.com/gin-contrib/cors`，通过 `server.cors` 配置。其他中间件业务可按需自行接入。

## 认证与授权

### 认证、授权、Session 分离

- 认证：当前请求是谁发起的？
- 授权：当前请求是否允许执行该操作？
- Session：当前登录会话是否仍然有效？

### 身份结构

```go
type Identity struct {
    UserID      string
    TenantID    string
    SessionID   string
    Roles       []string
    Permissions []string
    AuthType    string
}
```

身份由认证组件产生，不允许业务请求直接提交并覆盖 `UserID`、`TenantID` 等字段。当前租户必须经过用户租户成员关系验证。

### JWT 与 Refresh Token

Access Token：

- 默认 30 分钟，可配置。
- 签名算法白名单，仅允许 HS256，禁止 `none`。
- 校验 iss、aud、exp、nbf、iat，并设置时钟漂移容忍。
- 不放入密码、密钥等敏感信息。

Refresh Token：

- 使用高强度随机 opaque token，服务端只保存 SHA-256 Hash。
- 默认 30 天，可配置。
- 每次刷新轮换 Token。
- 已轮换 Token 再次使用时，撤销整个 Token Family 并记录安全日志。
- 支持主动撤销、退出全部会话。

Session 记录包含：

```text
id / user_id / tenant_id / roles / permissions
device_name / ip / user_agent
created_at / last_active_at / expires_at / revoked_at
```

> `auth.MemorySessionRepository` 仅用于开发和测试。生产环境必须实现 `auth.SessionRepository` 持久化接口。

### 密码安全

- 使用 Argon2id，参数可配置。
- 初始参数：`memory=65536 KiB`、`iterations=3`、`parallelism=1`、salt 16 字节、输出 32 字节。
- 密码不能明文存储，也不能使用 MD5、SHA1、SHA256 直接保存。
- 参数必须结合服务器性能和并发登录压力压测后再调整。

### 登录防爆破

示例策略：

- 按 IP、账号、IP+账号、全局登录请求多维限流。
- 使用渐进延迟或短时限制，避免永久锁死账号造成拒绝服务。
- 登录失败统一返回“用户名或密码错误”，避免枚举账号。

框架提供通用限流器，示例模块内置了登录接口按 IP 限流；更复杂的渐进延迟策略可由业务实现。

### 授权模型

采用 RBAC + 资源授权：

- RBAC：用户、角色、权限、用户角色关系、角色权限关系。
- 资源授权：用户是否拥有资源、是否属于资源所属租户、是否通过分享关系获得权限、当前权限是否允许操作该资源。
- 权限格式：`user:read`、`device:control`、`tenant:manage`、`system:config`，支持 `user:*`、`*` 通配。
- 超级管理员角色默认放行，可通过 `permission.Policy` 配置。
- 租户不一致默认拒绝；访问他人资源需要 `动作:any` 宽权限。
- **授权必须在 Service 层执行，不能只依赖 Gin Middleware。**

## 数据库与 ORM

### Database 接口

```go
type Database interface {
    DB() *gorm.DB
    Ping(ctx context.Context) error
    Transaction(ctx context.Context, fn func(tx *gorm.DB) error) error
    Stats() sql.DBStats
    Close() error
}
```

默认支持 MySQL 和 PostgreSQL。业务模块通过 Repository 接口访问数据，Service 不直接依赖 GORM。

### Repository 与事务

```go
type UserRepository interface {
    GetByID(ctx context.Context, id int64) (*User, error)
    GetByUsername(ctx context.Context, username string) (*User, error)
    Create(ctx context.Context, user *User) error
    Update(ctx context.Context, user *User) error
    Delete(ctx context.Context, id int64) error
}
```

事务由 Service 控制业务边界；跨多个 Repository 的事务必须共享同一个事务对象：

```go
err := database.Get().Transaction(ctx, func(tx *gorm.DB) error {
    txCtx := database.WithTx(ctx, tx)
    if err := userRepo.Create(txCtx, user); err != nil {
        return err
    }
    return auditRepo.Create(txCtx, record)
})
```

Repository 内统一通过 `database.Conn(ctx, baseDB)` 获取连接，自动复用当前事务。

### 数据模型规范

- ID：`BIGINT` 或 UUID，跨 MySQL/PostgreSQL 优先使用 Go 侧 `int64`。
- 状态：`TINYINT` / `SMALLINT`。
- 金额：`DECIMAL`。
- 时间：`DATETIME(3)`，统一 UTC。
- JSON：仅用于非核心结构化扩展字段。
- 表名：`snake_case`；索引：`idx_`、`uk_`。
- 删除：按业务选择软删除或物理删除；审计日志、设备日志通常不软删除。

### 迁移

框架提供 [database/migrate](database/migrate) 迁移工具，不依赖 GORM `AutoMigrate` 修改核心表：

- `Register` 注册迁移，版本号必须唯一。
- `Up` 按版本升序执行；每个迁移在独立事务中执行，并写入 `schema_migrations`。
- `Down` 逆序回滚，支持指定步数。
- `Status` 返回每个迁移的应用状态。
- 支持 Go 函数迁移和 SQL 文件迁移（`0001_name.up.sql` / `.down.sql`）。

```go
runner, _ := migrate.New(database.Get().DB(), migrate.WithLogger(logger.Get()))
_ = runner.Register(
    migrate.Migration{
        Version: 1,
        Name:    "create users",
        Up: func(ctx context.Context, tx *gorm.DB) error {
            return tx.Exec("CREATE TABLE users (id BIGINT PRIMARY KEY)").Error
        },
        Down: func(ctx context.Context, tx *gorm.DB) error {
            return tx.Exec("DROP TABLE users").Error
        },
    },
)
_, _ = runner.Up(ctx)
```

```go
migrations, err := migrate.LoadSQL(os.DirFS("migrations"), ".")
_ = runner.Register(migrations...)
```

## 安全体系

安全能力作为框架基础设施内置，不依赖业务项目自行拼装。

| 模块 | 主要职责 |
|---|---|
| HTTP Security | CORS、CSRF、请求限制 |
| Authentication | JWT、Session、Refresh Token |
| Authorization | RBAC、资源授权 |
| Input Security | 参数校验、SQL 注入、XSS |
| Network Security | TLS、可信代理 |
| Rate Limiting | 限流、并发控制、爆破防护 |
| Data Security | 加密、脱敏、密钥管理 |
| File Security | 文件上传、下载和路径安全 |
| Audit Security | 安全审计、操作审计 |
| Runtime Security | 配置、依赖和进程安全 |

### HTTP 安全

CORS 由 `server.cors` 配置，默认关闭，启用后使用 `github.com/gin-contrib/cors`：

- 必须显式列出 `allowed_origins`，支持精确 Origin 或 `"*"`。
- `allow_credentials: true` 时不允许 `"*"`。
- 预检请求由 CORS 中间件在限流与业务路由之前处理。
- CORS 不作为身份认证和资源授权机制。

框架不内置安全响应头（`X-Content-Type-Options`、`X-Frame-Options`、CSP、HSTS 等），推荐在反向代理/网关注入，或由业务自行挂载中间件。

- 使用 `http.MaxBytesReader` 限制 Body，限制 Query 长度和 HTTP 方法。
- JSON API 可要求 `Content-Type: application/json`。

### CSRF

- 采用签名 Double Submit Cookie。
- 同时使用 `Secure`、`HttpOnly`、`SameSite` Cookie 属性。
- SameSite 不能完全替代 CSRF Token。
- 纯 Bearer Token 接口按认证模式跳过 CSRF 校验。
- 未签发 CSRF Cookie 的请求不携带 Cookie 身份，直接放行。

### 输入校验

所有外部输入必须校验必填、长度、格式、数值范围、枚举、数组大小和嵌套层级。

SQL：

- 使用参数化查询。
- 禁止直接拼接用户输入。
- 动态表名、排序字段必须使用白名单。
- Raw SQL 必须经过审查。

XSS：

- 输出时按上下文编码。
- 富文本使用 HTML Sanitizer（`security/sanitize`）。
- 不做粗暴的全局字符串转义。

SSRF：

- 只允许必要协议。
- 禁止访问 localhost、私网、link-local 和云元数据地址。
- DNS 解析后校验 IPv4/IPv6，连接时使用已校验 IP，防止 DNS rebinding。
- 每次重定向重新校验，限制连接时间和响应体大小。

### 文件安全

- 上传服务 `upload.Service` 检查大小、扩展名、MIME、Magic Number、文件名和路径。
- 不信任客户端文件名，存储 Key 由服务端生成，格式为 `{tenant}/{category}/{yyyy}/{mm}/{uuid}{ext}`。
- 文件存放在 Web Root 外部，本地存储会校验路径穿越并原子写入。
- JPEG/PNG/WebP 图片可自动等比缩放并重新编码，丢弃 EXIF 等元数据。
- HTML、SVG 等主动内容默认不在白名单内，必要时作为附件下载。
- 下载文件必须再次检查资源授权；私有文件优先使用短期签名 URL。
- `scan_enabled=true` 时必须注入 `upload.Scanner`（`upload.SetScanner`），否则上传服务拒绝启动。
- 当前本地存储已实现，S3/MinIO 为后续扩展。

### 网络与 TLS

- 最低 TLS 1.2，优先 TLS 1.3。
- 私钥文件限制权限，证书到期告警。
- 支持 `none`（反向代理终止 TLS）、`file`（加载证书）和 `acme`（autocert 自动申请/续期）。
- ACME 支持 HTTP-01 与 TLS-ALPN-01；DNS-01 需要 DNS Provider 集成，暂未实现。
- 严格配置 `trusted_proxies`，不能直接信任任意 `X-Forwarded-For`。

WebSocket Hub 已内置 Origin 校验、握手鉴权、消息大小限制、读写超时和心跳；长连接中的敏感操作仍应重新检查授权。

### 限流与并发控制

- 限流维度：IP、User、Tenant、API、Global。
- 算法：Token Bucket（当前实现）、Sliding Window、Fixed Window、Semaphore。
- 默认中间件按客户端 IP 分桶，避免单个客户端打满全站配额；`security.rate_limit` 中的速率与桶容量按每个 key 生效。
- 限流器内置 key 数量上限（`max_buckets`，默认 10 万）与分片锁，防止高基数 key 占用过多内存。
- 多实例部署应使用 Redis 或网关共享限流状态。
- 高风险接口在限流组件不可用时应失败关闭；普通查询可按明确策略降级。
- 后台任务并发通过协程池和定时任务并发策略控制。

### 数据加密

- 密码使用 Argon2id，不使用可逆加密。
- 敏感字段使用 AES-256-GCM，密文采用版本化 Envelope：版本、KeyID、nonce、密文。
- AES-GCM nonce 必须保证同一密钥下不重复。
- 密钥来源支持环境变量，后续可接入 Vault、KMS、Secret Manager。
- 支持密钥轮换规划。
- 日志禁止记录密码、JWT、Refresh Token、API Secret、设备完整密钥、数据库连接密码。

### API Key

设计约定（后续实现）：

- Hash 存储，不保存明文。
- Scope 权限、有效期、主动撤销、密钥轮换。
- 记录最后使用时间，限制调用来源。

### 审计

重点审计登录、登出、登录失败、用户/角色/权限变更、租户变更、设备绑定与分享、设备远程控制、系统配置修改、文件上传删除、管理员操作。

```text
actor_type / actor_id / user_id / tenant_id
action / resource_type / resource_id
result / reason_code
source_ip / user_agent / request_id / trace_id
detail / created_at
```

- `actor_id` 可记录用户、设备、API Key 或系统任务。
- 审计事件不可由普通用户修改或删除。
- `detail` 限制大小并过滤敏感字段。
- 当前提供日志审计实现；高价值操作建议使用事务 Outbox 保证业务数据与审计事件一致。

### IoT 设备安全（规划）

设备认证：

- 独立 Device Secret、HMAC 签名、mTLS、短期设备 Token。
- 签名至少绑定：`device_id`、`key_id`、`timestamp`、`nonce`、`method`、`path`、`query`、`body_hash`。
- 服务端校验身份、密钥、签名、时间窗口、nonce 唯一性、设备状态和权限。
- nonce 使用 Redis 原子 `SET NX + TTL`。
- 设备命令包含 `command_id`、`device_id`、`tenant_id`、`action`、`payload`、`created_at`、`expires_at`，服务端和设备端均持久化处理状态，避免重启后重复执行。
- MQTT 每台设备独立身份和 ACL，Broker 限制 Topic，启用 TLS，设备禁用后立即撤销连接和权限。

### Runtime 安全

- 生产环境关闭 Debug，禁止默认 JWT Secret。
- 配置文件不提交真实 Secret。
- 服务使用非 root 运行；容器限制 CPU、内存和文件系统权限。
- pprof 默认关闭；排障时临时开启并限制访问来源，用完立即关闭。
- 后台 goroutine 必须受 Context 管理，并具备 panic 隔离。
- Recovery 记录堆栈，但不向客户端返回堆栈。
- 使用 `govulncheck` 检查依赖漏洞，生成 SBOM 并固定依赖版本。

## 配置管理

### 配置声明方式

配置结构体不再集中声明。每个组件在自己的包里定义 `Config`，并在 `init()` 中注册配置段：

```go
// logger/config.go
type Config struct {
    Level  string `yaml:"level"`
    Output string `yaml:"output"`
}

func DefaultConfig() Config { return Config{Level: "info", Output: "logs/app.log"} }

// logger/boot.go
var cfg = config.Register("logger", DefaultConfig())

func GetConfig() *Config { return cfg.Get() }
```

加载时，框架把 YAML 中同名节点解码到每个包的独立副本，再原子替换当前快照；读取方始终拿到不可变配置。

### 加载顺序

```text
各包注册的默认值
  → 配置文件
  → GOFRAME_<SECTION>_<KEY> 环境变量
```

- 配置文件：`config.SetFile(path)` 或环境变量 `GOFRAME_CONFIG`；未指定时搜索 `configs/config.yaml`、`config.yaml` 等常见路径。
- 支持 `${VAR}`、`${VAR:-default}` 占位符展开。
- 环境变量示例：`GOFRAME_SERVER_PORT`、`GOFRAME_LOGGER_LEVEL`、`GOFRAME_DATABASE_DSN`、`GOFRAME_SECURITY_AUTH_SECRET`、`GOFRAME_REDIS_ADDR`。
- 每个组件在自己的 `Startup` 中校验配置，非法配置导致 boot 启动失败并自动回滚。
- 支持热更新：文件变化后重新加载所有段并触发回调；日志等级会即时生效，其他需要重建连接的配置需重启。

### 配置结构

完整示例见 [configs/config.example.yaml](configs/config.example.yaml)，主要配置段：

```yaml
app:        # 名称、版本、环境
server:     # 监听地址、超时、可信代理、API 前缀与版本、请求限制（request 子段）、CORS（cors 子段）
tls:        # none / file / acme
database:   # driver、DSN、连接池
redis:      # 缓存开关与连接
logger:     # level / format / output / rotation（默认滚动写入文件）
storage:    # local / s3 / minio
scheduler:  # 开关、任务超时、时区
taskqueue:  # Asynq 任务队列：Redis 连接（缺省复用 redis 段）、并发、队列权重、重试
notify:     # 通知通道 smtp/webhook/sms，队列启用时经 taskqueue 异步派发
aliyun:     # 阿里云 SDK：短信 dysmsapi / 语音 dyvmsapi / 对象存储 OSS（按需导入 cloud/aliyun）
tencent:    # 腾讯云 SDK：短信 SMS / 语音 VMS / 对象存储 COS（按需导入 cloud/tencent）
huawei:     # 华为云 SDK：短信 SMS / 语音 VCP / 对象存储 OBS（按需导入 cloud/huawei）
onenet:     # OneNET 短信能力平台：模板签名短信（按需导入 cloud/onenet）
ctyun:      # 天翼云：云通信短信（EOP 签名）/ 对象存储 ZOS（S3 兼容）（按需导入 cloud/ctyun）
pool:       # 后台协程池 worker 与队列
mqtt:       # 客户端 Broker 列表、认证、TLS、重连
broker:     # 嵌入式 MQTT Broker、会话、限额、TLS、重传间隔
websocket:  # 路径、Origin、消息大小、超时、上下行队列、匿名开关
spa:        # embed / directory

security.auth:        # JWT、Session 与 password（auth 包）
security.rate_limit:  # 限流（ratelimit 包，含 Gin 中间件）
security.csrf:        # CSRF（csrf 包，含 Gin 中间件）
security.upload:      # 上传（upload 包）
security.audit:       # 审计（audit 包）
security.encryption:  # 数据加密（crypto 包）
```

上传配置还包含：`allowed_extensions`、`allowed_mime_types`、`image_max_width`、`image_max_height`、`image_max_pixels`、`image_quality`（位于 `security.upload`）。

说明：

- `${DB_DSN}` 等 Secret 必须由运行环境注入。
- `scan_enabled: false` 时，不允许未经检查的高风险文件进入业务流程。
- HSTS 仅在正确部署 HTTPS 后启用。
- CSP 不应直接使用通用示例代替实际 SPA 策略。

### 热更新

当前支持热更新：

- 日志等级。

通常需要重启或专门重建：

- 监听地址。
- 数据库连接。
- TLS 模式。
- ACME 域名。
- 存储后端。

## 日志系统

采用 Zap 抽象，业务只依赖 `logger.Logger` 接口：

- 级别：debug / info / warn / error。
- 格式：json / console。
- 输出：stdout / stderr / 文件；默认写入 `logs/app.log`。
- 文件输出默认开启滚动：单文件 100MB、保留 30 天与 10 个备份、压缩旧日志。
- 支持 `With` 附加固定字段和运行时调整日志等级。
- 生产环境可输出 JSON 到文件由系统采集，或改为 stdout 交给容器采集。
- 支持 `Close` 刷新并释放文件句柄。

配置示例：

```yaml
logger:
  level: info
  format: json
  output: logs/app.log
  rotation:
    enabled: true
    max_size: 100MB
    max_age: 30
    max_backups: 10
    compress: true
    local_time: true
```

`output` 为 `stdout` / `stderr` 时不启用滚动；需要关闭文件滚动时设置 `rotation.enabled: false`。

统一字段建议：

```text
timestamp / level / message
request_id / trace_id / user_id / tenant_id / module
```

要求：

- 敏感信息脱敏。
- 错误日志包含错误上下文。
- 请求日志包含状态码、耗时和请求 ID。
- 不记录密码、Token、Secret。
- 避免记录完整文件内容和大型请求 Body。

## 缓存

```go
type Cache interface {
    Get(ctx context.Context, key string, value any) error
    Set(ctx context.Context, key string, value any, ttl time.Duration) error
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
    Close() error
}
```

实现：

- `MemoryCache`：进程内，支持 TTL、过期清理、并发安全。
- `RedisCache`：基于 go-redis，支持 Key 前缀。

缓存只用于提升性能，不作为核心业务数据的唯一来源。需要明确 TTL、一致性、穿透、击穿、雪崩和多租户 Key 隔离。

## 文件存储

```go
type Storage interface {
    Put(ctx context.Context, key string, r io.Reader) error
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
    GetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}
```

当前实现 `LocalStorage`：

- 文件存放在配置目录，默认 `data/storage`，位于 Web Root 之外。
- 校验 Key，拒绝路径穿越、反斜杠和空 Key。
- 使用临时文件 + Rename 原子写入。
- 支持 BaseURL 生成访问地址。

S3/MinIO 为后续扩展。文件 Key 建议：

```text
{tenant_id}/{category}/{yyyy}/{mm}/{random_id}
```

文件元数据应独立存储：文件名、大小、MIME、存储 Key、上传者、租户、创建时间。

## 文件上传与图片处理

`upload.Service` 复用 `Storage`，完成校验、图片处理与落盘：

```go
result, err := upload.Get().Save(ctx, tenantID, "avatar", filename, file)
// Result: Key / Name / Size / MIME / URL / Width / Height
```

也可以直接挂载 HTTP 处理器（建议放在受保护分组）：

```go
api.POST("/upload", upload.Get().Handler(upload.HTTPOptions{
    Field:           "file",
    DefaultCategory: "attachment",
}))
```

处理器默认从认证身份读取 `TenantID`（为空回退 `UserID`），分类可来自表单 `category`。

安全约束：

- 大小上限、扩展名白名单、MIME 白名单、Magic Number 校验。
- 服务端生成随机 Key，不信任客户端文件名。
- 租户/分类只允许 `[A-Za-z0-9_-]`，防止路径穿越。
- 可选 `upload.Scanner`；开启扫描但未注入时服务拒绝启动。
- 图片会重新编码，自动丢弃 EXIF 等元数据。

`imagex` 提供独立图片处理能力：

```go
out, info, err := imagex.Process(file, imagex.Options{
    MaxWidth:  1024,
    MaxHeight: 1024,
    MaxPixels: 32 << 20,
    Format:    "jpeg",
    Quality:   85,
})

resized := imagex.Resize(img, 300, 200)
filled := imagex.Fill(img, 200, 200)
thumb := imagex.Thumbnail(img, 128)
```

支持 JPEG、PNG、GIF（首帧）、WebP 解码；JPEG、PNG 编码。

## Excel 导入导出

`excel` 提供多工作表读写：

```go
tables := []excel.Table{
    {
        Sheet:   "用户",
        Headers: []string{"ID", "姓名", "状态"},
        Rows: [][]string{
            {"1", "张三", "启用"},
            {"2", "李四", "禁用"},
        },
    },
}

// 导出 XLSX
err := excel.Export(file, "users.xlsx", tables,
    excel.WithFreezeHeader(true),
    excel.WithAutoFilter(true),
    excel.WithZebra(true),
    excel.WithColumnWidths(10, 20, 12),
)

// 读取（自动识别 XLSX / XLS / SpreadsheetML）
tables, err := excel.Read(file, "users.xlsx",
    excel.WithHeaderRow(1),
    excel.WithMaxRows(10000),
    excel.WithMaxCols(50),
)
```

说明：

- XLSX 读写基于 excelize；XLS 读取 BIFF8 基于 extrame/xls。
- XLS 导出为 Excel 2003 SpreadsheetML（`.xls` 扩展名），Excel/WPS 可直接打开，但不是 BIFF8 二进制。
- 导入按文件魔数识别格式，不依赖扩展名；支持单表/全部工作表、表头行、最大行/列限制。
- 单文件默认限制 64MB；导入内容为字符串，业务层负责类型转换与校验。

## PDF 生成

```go
doc, err := pdf.New(
    pdf.WithHeader("月度报表"),
    pdf.WithFooter("goframe"),
    pdf.WithPageNumbers(true),
)
if err != nil {
    return err
}

_ = doc.Title("设备运行报告")
_ = doc.Heading("概览")
_ = doc.Paragraph("本报告汇总本月设备在线率、告警与能耗数据。")

_ = doc.Table(pdf.Table{
    Headers: []string{"设备", "在线率", "状态"},
    Rows: [][]string{
        {"D-001", "99.2%", "正常"},
        {"D-002", "97.8%", "告警"},
    },
    Widths: []float64{2, 1, 1},
    Align:  []string{"left", "right", "center"},
    Zebra:  true,
})

data, err := doc.Bytes()
// 或 doc.Save("report.pdf")
```

- 表格支持自动换行、自动分页，并在新页重复表头。
- 支持图片插入、页眉页脚、页码与 PDF 元数据。
- 内置 Go Regular 字体，仅覆盖拉丁字符；中文必须注入包含中文字形的 TTF：

```go
doc, _ := pdf.New(
    pdf.WithFontFile("cjk", "/path/NotoSansSC-Regular.ttf"),
    pdf.WithDefaultFont("cjk"),
)
```

PDF 模块当前只负责生成，不包含 PDF 文本抽取或页面合并。

## 压缩包处理

`archive` 支持 zip、tar、tar.gz、gz 的创建、解压与列表：

```go
// 创建：sources 可以是文件或目录，目录会递归打包
err := archive.Compress("backup.zip", "data", "configs/config.yaml")
err = archive.Compress("backup.tar.gz", "data")

// 解压
err = archive.Extract("backup.zip", "data/backup")

// 带安全选项解压
err = archive.Extract("upload.tar.gz", "data/upload",
    archive.WithMaxFileSize(10<<20),  // 单文件上限
    archive.WithMaxTotalSize(100<<20), // 总大小上限
    archive.WithMaxFiles(1000),        // 文件数量上限
    archive.WithOverwrite(),           // 允许覆盖
    archive.WithPreserveMode(),        // 保留权限
    archive.WithPreserveTime(),        // 保留修改时间
)

// 列出条目，不落盘
entries, err := archive.List("backup.zip")

// 处理上传流；zip 需要随机访问，默认限制最多读取 512MB
err = archive.ExtractTo(reader, archive.Zip, "data/upload",
    archive.WithMaxArchiveSize(100<<20),
)
```

- 默认拒绝绝对路径、盘符路径与 `../` 路径穿越（Zip Slip）。
- 默认拒绝符号链接、硬链接、设备文件与 FIFO；开启 `WithAllowSymlinks` 后链接目标仍必须位于解压目录内。
- 默认限制单文件 100MB、总大小 1GB、文件数量 10000，可用选项调整或关闭。
- 默认不覆盖已存在文件，返回 `archive.ErrAlreadyExists`。
- 格式优先按扩展名识别，后缀缺失时读取文件头；`.gz` 内部为 tar 时自动按 tar.gz 处理。
- `ExtractTo` 处理 zip 时需要完整读入内存，默认限制 512MB，可通过 `WithMaxArchiveSize` 调整。
- 创建压缩包先写同目录临时文件，成功后再原子替换，失败不会破坏已有归档。

## 定时任务

基于 robfig/cron：

```go
type Scheduler interface {
    Register(name string, spec string, fn func(ctx context.Context) error) error
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Jobs() []Job
}
```

能力：

- 标准 5 段 cron 表达式，支持可选秒字段和 `@every`。
- 任务执行超时控制。
- 同一任务上一轮未结束时跳过本轮，避免堆积。
- 任务 panic 隔离并记录堆栈。
- 启动、停止、任务状态查询。
- 多实例分布式锁为规划能力，多实例部署必须防止同一任务重复执行。

## 事件总线

第一版使用进程内事件总线：

```go
type EventBus interface {
    Publish(ctx context.Context, event Event) error
    Subscribe(name string, handler EventHandler) error
    Unsubscribe(name string) error
}
```

行为：

- 订阅者按注册顺序同步执行。
- 单个订阅者失败或 panic 不影响其他订阅者，错误聚合返回。
- 支持记录事件名、载荷、发生时间和元数据。

进程内事件总线不能当作可靠消息队列。涉及资金、设备命令、审计等关键流程时，应使用数据库事务 Outbox 或可靠消息机制。后续可扩展 NATS、Kafka、Redis Streams。

## MQTT

框架提供 MQTT 客户端，配置启用后由 boot 任务自动连接、重连与关闭：

```go
client := mqtt.Get()
client.Publish(ctx, "devices/1/down", 0, false, payload)
client.PublishJSON(ctx, "devices/1/down", 0, false, command)

err := client.Subscribe(ctx, "devices/+/up", 0, func(topic string, payload []byte) {
    handleUplink(topic, payload)
})
```

- 支持 Broker 列表、认证、KeepAlive、自动重连、CleanSession。
- 支持 MQTT over TLS（CA、客户端证书、ServerName）。
- 发布 Topic 禁止通配符；订阅过滤器校验 `+` / `#` 位置。
- 消息处理 panic 被隔离并记录堆栈。
- 配置项：`mqtt.enabled`、`brokers`、`client_id`、`username`、`password`、`keep_alive`、`connect_timeout`、`reconnect_interval`、`tls`。

## MQTT Broker

框架内置轻量 MQTT Broker，为设备提供标准 MQTT 3.1/3.1.1 接入，默认关闭：

```yaml
broker:
  enabled: true
  addr: ":1883"
  allow_anonymous: false
```

关闭匿名连接时必须注入鉴权器，在 CONNECT 阶段完成身份校验：

```go
// 必须在 boot.Startup 之前注入
broker.SetAuthenticator(broker.AuthFunc(func(ctx context.Context, req broker.ConnectRequest) error {
    // 可查询数据库、校验 HMAC 或调用设备认证服务
    if req.Username == "" || req.Password == "" {
        return errors.New("missing credentials")
    }
    if !deviceAuth.Verify(req.ClientID, req.Username, req.Password) {
        return errors.New("invalid credentials")
    }
    return nil
}))
broker.SetHooks(broker.Hooks{
    OnConnect: func(s *broker.Session) {
        logger.Get().Info("device connected", logger.String("client_id", s.ClientID()))
    },
    OnPublish: func(s *broker.Session, msg broker.Message) {
        handleUplink(msg.Topic, msg.Payload)
    },
})

boot.Startup(ctx)
```

- 支持 CONNECT/CONNACK、PUBLISH、SUBSCRIBE/UNSUBSCRIBE、PINGREQ、DISCONNECT。
- QoS 0/1/2 完整握手：PUBACK、PUBREC/PUBREL/PUBCOMP，重复包按消息 ID 去重。
- 支持 Retained 消息、Will 消息、`+` / `#` 通配符订阅。
- 同一 ClientID 再次连接会接管旧会话并断开旧连接。
- `CleanSession=false` 时保留订阅、离线消息与未确认的 QoS 1/2 消息；重连后按 DUP 重发，并按 `broker.retransmit_interval` 定期重传。
- 路由使用订阅索引，只遍历实际存在的过滤器，避免按“会话数 × 订阅数”扫描。
- 单连接发送队列有界，慢消费者不会阻塞路由 goroutine；队列满时计入 `messages_dropped`。
- 监听 Accept 遇到 EMFILE 等临时错误时退避重试，不会永久停止接入。
- 可选 TLS 监听（`broker.tls.enabled`，通常使用 8883 端口）。
- Hook 在连接 goroutine 中执行并隔离 panic，不应执行长时间阻塞操作。
- 业务侧可通过 `broker.Get().Publish(topic, payload, qos, retain)` 向设备下发消息。
- 运行状态：`broker.Get().Stats()`、`broker.Get().Sessions()`、`broker.Get().Addr()`。
- 配置项：`broker.enabled`、`addr`、`allow_anonymous`、`max_packet_size`、`max_clients`、`max_subscriptions`、`max_queued_messages`、`session_expiry`、`write_timeout`、`connect_timeout`、`retransmit_interval`、`send_queue`、`tls`。

## WebSocket

框架提供 WebSocket Hub，默认关闭；启用后注册 `websocket.path`（默认 `/ws`）：

```go
hub := websocket.Get()
hub.Broadcast(payload)
hub.Send(subject, payload)
infos := hub.Clients()
stats := hub.Stats()
```

- 握手前鉴权：`websocket.SetAuthenticator`；未配置且未开启 `allow_anonymous` 时返回 401。
- Origin 默认只允许同源；`allowed_origins` 为精确白名单，`*` 不建议生产使用。
- 每个连接独立读写 goroutine，支持 Ping/Pong、消息大小限制、慢消费者关闭。
- 上行消息进入有界队列后由独立 goroutine 处理，慢业务不会阻塞读循环；队列满时关闭连接形成背压。
- 上行消息通过 `websocket.SetOnMessage` 处理，panic 被隔离。
- 应用关闭时先停止接收新连接，再关闭并等待已有连接退出。
- 配置项：`websocket.enabled`、`path`、`allowed_origins`、`max_message_size`、`read_timeout`、`write_timeout`、`ping_interval`、`send_queue`、`inbound_queue`、`allow_anonymous`。

## 健康检查与监控

提供：

```text
GET /health/live     # 公开
GET /health/ready    # 公开
GET /health          # 管理员
GET /metrics         # 管理员
GET /system/...      # 管理员（sysinfo 启用时）
GET /license         # 管理员（license 启用时）
```

- 管理员指已登录且通过 `permission` 的 `admin` 授权；未启用 `security.auth` 时这些端点返回 401（默认拒绝）。
- `live`：进程是否存活。
- `ready`：服务是否可以正常接收请求，未就绪或关键依赖异常返回 503。
- `health`：聚合检查所有依赖，关键依赖失败返回 503。
- 检查项并发执行，单项 panic 被隔离并记录为失败。
- 默认检查数据库和缓存。

Prometheus 指标：

- `goframe_http_requests_total`
- `goframe_http_request_duration_seconds`
- `goframe_http_requests_in_flight`
- Go runtime 与进程指标

其中 HTTP 请求指标由 `monitor.Metrics.Middleware()` 采集，框架不会自动挂载；`/metrics` 默认只输出 Go runtime 与进程指标。需要请求指标时在业务入口注册：

```go
web.Engine().Use(web.Metrics().Middleware())
```

`/metrics` 与聚合健康检查默认要求管理员身份，Prometheus 抓取时需携带认证信息；如仅在内网暴露，应在网关或网络策略层再做一次限制。

### 性能分析（pprof）

Web 引擎内置 [gin-contrib/pprof](https://github.com/gin-contrib/pprof)，默认关闭；启用后在业务端口上注册分析路由：

```yaml
pprof:
  enabled: true
  path: /debug/pprof
```

```text
GET /debug/pprof/            # 分析索引
GET /debug/pprof/cmdline     # 启动参数
GET /debug/pprof/profile     # CPU profile，默认 30s
GET /debug/pprof/trace       # 执行追踪
GET /debug/pprof/symbol      # 符号解析
GET /debug/pprof/allocs      # 内存分配
GET /debug/pprof/block       # 阻塞
GET /debug/pprof/goroutine   # goroutine
GET /debug/pprof/heap        # 堆内存
GET /debug/pprof/mutex       # 锁竞争
GET /debug/pprof/threadcreate
```

- 采集方式：`go tool pprof http://localhost:8080/debug/pprof/profile`。
- pprof 路由与业务共用端口，会经过全局中间件（日志、限流、CSRF 等）；生产环境只在排障期间临时开启，并通过网关或访问控制限制来源。
- 通过 `web.PProf()` 获取当前配置。

### 系统资源实时分析（sysinfo）

基于 `shirou/gopsutil` 提供 CPU、内存、磁盘占用的实时查询，默认关闭；数据仅在请求时采集，不落盘、不保留历史：

```yaml
sysinfo:
  enabled: true
  path: /system
  sample_interval: 300ms
  timeout: 3s
```

```text
GET /system           # 完整快照（主机、CPU、内存、磁盘，管理员）
GET /system/cpu       # CPU 总使用率、每核使用率、核心数、负载（管理员）
GET /system/memory    # 物理内存与 Swap（管理员）
GET /system/disk      # 各分区容量、使用率与 inode（管理员）
```

- 返回结构与统一响应一致，数据在 `data` 字段中。
- 单次 CPU 采集会阻塞 `sample_interval`，超时由 `sysinfo.timeout` 控制。
- 接口会暴露主机信息，生产环境建议通过网关、内网或认证中间件限制访问来源。

### 在线支付（payment）

基于官方 SDK（`wechatpay-go` / `smartwalle/alipay`）的支付网关封装，默认关闭。只做协议网关、不落库：订单与退款状态持久化由业务在回调 handler 中完成；金额统一使用分（`int64`），微信/支付宝的元分换算在适配器内处理。

```yaml
payment:
  enabled: true
  notify_path: /payment/notify   # 自动登记 POST /payment/notify/wechat、/payment/notify/alipay
  wechat:
    enabled: true
    app_id: wx...
    mch_id: "190000..."
    serial_no: ...               # 商户 API 证书序列号
    private_key: /etc/apiclient_key.pem  # PEM 内容或文件路径
    apiv3_key: ...
    notify_url: https://example.com/payment/notify/wechat
  alipay:
    enabled: true
    app_id: "2021..."
    private_key: ...             # 应用私钥
    public_key: ...              # 普通模式；三个 *_cert_file 任一非空即证书模式
    production: true
    notify_url: https://example.com/payment/notify/alipay
```

```go
// 下单（渠道未启用时返回 nil）
result, err := payment.Get().Wechat().Prepay(ctx, payment.Order{
	OutTradeNo: "ORD20261119001", Amount: 1, Subject: "1 分钱测试", Scene: payment.SceneNative,
})
// result.CodeURL 即 Native 二维码串；jsapi 场景返回 PayParams 调起五参数。

// 回调：验签、幂等去重（Redis 可用时为分布式）后分发业务 handler
payment.OnNotify(payment.ChannelWechat, func(ctx context.Context, n *payment.NotifyResult) error {
	if n.State == payment.StateSuccess {
		// 业务落库：标记订单已支付（按 n.OutTradeNo 幂等更新）
	}
	return nil
})
```

- 支持场景：微信 `native`/`jsapi`/`h5`/`app`，支付宝 `native`/`page`/`h5`/`app`；均含查询、关单、退款、退款查询与账单下载（`Query`/`Close`/`Refund`/`QueryRefund`/`DownloadBill`）。
- 回调处理失败或同一回调正在处理时按渠道约定应答失败，由渠道自动重试；成功处理结果按 `IdempotencyKey` 去重，`done_ttl` 内重复通知直接确认。
- 未启用 Redis 时幂等锁回退为进程内实现，仅保证单实例语义。

### 企业许可（license）

基于 Ed25519 的离线签名许可，运行时只需公钥即可校验，无需联网激活：

- **有效期**：`not_before` / `expires_at`，支持到期后的宽限期。
- **功能授权**：`features` 列表，支持 `*` 全量授权。
- **配额**：`limits` 键值对，`-1` 表示不限制。
- **机器绑定**：`machines` 存放机器指纹；默认按 `uuid > hostid > disk > cpuid > mac` 优先级取第一个可用来源，MAC 仅作兜底并过滤虚拟网卡与随机地址。
- **指纹诊断**：`go run ./cmd/license fingerprint` 查看各来源与最终指纹。
- **吊销**：离线吊销列表，由同一私钥签发。
- **防回拨**：`state_file` 记录最后校验时间，检测系统时钟回拨。
- **严格模式**：`strict: true` 时无效许可阻断启动。

```yaml
license:
  enabled: true
  file: data/license/license.json
  public_key: keys/public.pem
  revocation_file: data/license/revocation.json
  status_path: /license
  strict: true
  state_file: data/license/state.json
  fingerprint: ""
  fingerprint_sources: []   # 可选：uuid / hostid / disk / cpuid / mac
  clock_tolerance: 5m
```

签发与检查：

```bash
go run ./cmd/license keygen -dir keys
go run ./cmd/license issue -key keys/private.pem -id L-001 -product goframe \
  -customer ACME -edition enterprise -features sso,audit \
  -limits users=100,devices=50 -expires 2027-12-31T23:59:59Z -grace 720h \
  -machines <fingerprint> -out data/license/license.json
go run ./cmd/license inspect -public keys/public.pem -license data/license/license.json
go run ./cmd/license fingerprint
go run ./cmd/license revoke -key keys/private.pem -ids L-001 -out data/license/revocation.json
```

业务路由授权：

```go
api.GET("/sso/config", license.RequireFeature("sso"), handler)
api.GET("/billing", license.RequireValid(), handler)

if license.Get().WithinLimit("users", currentUsers) {
    // 允许新增用户
}
```

```text
GET /license   # 状态、有效期、功能与配额快照（管理员）
```

许可状态查询接口会暴露客户与授权信息，框架默认按管理员接口挂载（登录 + `admin` 授权），未启用认证时返回 401。

### 微信鉴权（weixin）

支持多公众号/多小程序，AccessToken 与 JSAPI Ticket 自动缓存、提前刷新，并在令牌失效时自动重试：

```yaml
weixin:
  enabled: true
  timeout: 10s
  apps:
    - name: official
      type: official
      app_id: ${WECHAT_APP_ID}
      app_secret: ${WECHAT_APP_SECRET}
      token: ${WECHAT_TOKEN}
      encoding_aes_key: ${WECHAT_AES_KEY}
      oauth_redirect: https://example.com/oauth/callback
      default: true
    - name: mini
      type: mini
      app_id: ${WECHAT_MINI_APP_ID}
      app_secret: ${WECHAT_MINI_APP_SECRET}
```

公众号：

```go
official := weixin.Get("official")

authorizeURL := official.AuthorizeURL(redirectURI, "snsapi_userinfo", state)
token, err := official.ExchangeCode(ctx, code)          // 网页授权
user, err := official.OAuthUserInfo(ctx, token.AccessToken, token.OpenID)
config, err := official.JSSDKConfig(ctx, pageURL)       // JS-SDK 签名

// 服务端回调校验
if !official.VerifySignature(signature, timestamp, nonce) { ... }
if !official.VerifyMessageSignature(msgSignature, timestamp, nonce, encrypt) { ... }
plain, err := official.DecryptMessage(encrypt)          // AES 消息解密
```

小程序：

```go
mini := weixin.Get("mini")

session, err := mini.Code2Session(ctx, code)            // openid + session_key
phone, err := mini.PhoneNumber(ctx, phoneCode)          // 新版手机号获取
phone, err := mini.DecryptPhone(session.SessionKey, encryptedData, iv)
user, err := mini.DecryptUserInfo(session.SessionKey, encryptedData, iv)

accessToken, err := mini.AccessToken(ctx)               // 业务接口调用
```

- 令牌缓存优先使用本地内存，启用 Redis 缓存时自动多实例共享。
- `weixin.Apps()` 返回已加载应用，`weixin.Default()` 返回默认应用。
- 同名应用、缺失凭证、非法 AES Key 会在启动阶段报错。
- 未引入第三方 SDK，基于标准库实现，避免额外依赖。

### 新增基础设施能力

**链路追踪（OpenTelemetry）**

```yaml
trace:
  enabled: true
  endpoint: localhost:4318
  protocol: http
  sample_ratio: 1.0
```

- `trace.Middleware()` 已内置到全局中间件，自动解析/生成 traceparent 并回写 `X-Trace-ID`。
- 出站请求使用 `httpclient` 或 `trace.HTTPTransport` 即可传递链路。

**OpenAPI 文档（swaggo）**

```bash
swag init -g cmd/server/main.go -o docs
```

业务 `main` 匿名导入生成的 `docs` 包，并开启：

```yaml
openapi:
  enabled: true
  path: /swagger
```

Swagger UI 以公开根路径挂载；生产环境应保持关闭，或仅在管理网络内开启，并通过网关/访问控制限制访问。

**对象存储与文件扫描**

- `storage.driver` 支持 `local`、`s3`、`minio`，配置 `endpoint/bucket/region/access_key/secret_key/use_ssl`。
- `upload.scan_enabled: true` 时使用 `upload.clamav` 配置的 clamd 地址进行 INSTREAM 扫描；也可通过 `upload.SetScanner` 注入自定义实现。

**会话持久化与 API Key**

```go
repo, _ := gormstore.New(database.Get().DB())
auth.SetSessionRepository(repo)

keys := auth.NewMemoryAPIKeyRepository()
auth.SetAPIKeyRepository(keys)
```

- API Key 明文只在创建时返回，服务端保存 sha256 哈希；支持 `X-API-Key` 与 `Authorization: Bearer gf_xxx`。

**分布式锁、限流与 Outbox**

- `lock.NewRedis(cache.Get().(*cache.RedisCache).Client(), lock.RedisOptions{...})`。
- 启用 Redis 缓存时，全局限流自动切换为 Redis 分布式令牌桶，否则保持进程内实现。
- Outbox：业务事务内调用 `outbox.Record(ctx, tx, ev)`；`outbox.enabled: true` 时自动迁移并启动 relay 投递到 EventBus。

**WebSocket 业务协议**

未设置 `SetOnMessage` 时启用内置 JSON 协议：

```json
{"type":"subscribe","topic":"device/1"}
{"type":"publish","topic":"device/1","data":{"online":true}}
```

支持 `subscribe / unsubscribe / publish / ping`，单连接订阅上限由 `websocket.max_topics` 控制。

**任务队列与通知**

`taskqueue` 基于 Asynq（Redis）提供分布式异步任务队列，并发量、队列权重、重试等由 `taskqueue` 配置段控制；处理器在 boot 启动前注册：

```go
// 业务 init 中注册处理器
taskqueue.Register("report:build", func(ctx context.Context, payload []byte) error {
    return buildReport(ctx, payload)
})

// 投递任务（可选 Queue / MaxRetry / Timeout / Delay / Unique）
err := taskqueue.Enqueue(ctx, "report:build", data,
    taskqueue.Queue("default"), taskqueue.Delay(time.Minute))
```

`notify` 在配置段声明通道（smtp / webhook / sms）后即可用 `notify.Send` 派发：任务队列启用时入队异步发送并自动重试，未启用时回退为同步直发；也可 `notify.SetSender` 注入自定义发送器。

- `notify.NewSMTP`、`notify.NewWebhook`、`notify.NewSMS` 仍可独立使用。
- `sms` 通道由已导入并启用的云厂商自动注册（`notify.AddSMSProvider`），多家启用时按序故障转移；未注册任何服务商时发送明确报错。

**任务队列运维接口**

基于 Asynq Inspector，队列启用后可供管理后台直接调用：

```go
stats, _ := taskqueue.InspectQueues()               // 全部队列统计
tasks, _ := taskqueue.ListTasks("default", "retry", 1, 30) // 按状态分页列任务（含 archived 死信）
_ = taskqueue.CancelTask("default", id)             // 取消：非运行态转归档，运行态发取消信号
_ = taskqueue.RunTaskNow("default", id)             // 立即执行 scheduled/retry/archived 任务
_ = taskqueue.DeleteTask("default", id)             // 彻底删除
```

**云服务 SDK（短信 / 语音 / 对象存储）**

`cloud` 包定义与厂商无关的统一能力接口（`cloud.Sms` / `cloud.Voice` / `cloud.Storage`），五个厂商包实现之：`cloud/aliyun`、`cloud/tencent`、`cloud/huawei` 封装官方 SDK，`cloud/onenet`（中国移动 OneNET 短信）、`cloud/ctyun`（天翼云短信 + ZOS 对象存储）基于 REST API。各包提供 `sms`（短信）、`voice`（语音通知/通话，OneNET 无）、`storage`（对象存储，OneNET 无）子服务。云包不在 `bootstrap` 中，按需匿名导入以注册 boot 模块：

```go
import _ "github.com/god-jason/goframe/cloud/aliyun"
```

启动后通过全局实例使用（未启用的子服务返回 `nil`）：

```go
aliyun.GetSms().Send(ctx, []string{"13800000000"}, "示例签名", "SMS_001", map[string]string{"code": "1234"})
aliyun.GetVoice().SingleCallByTts(ctx, "13800000000", "TTS_001", nil)
aliyun.GetStorage().Put(ctx, "a/b.txt", strings.NewReader("hi"))
url, _ := aliyun.GetStorage().SignURL(ctx, "a/b.txt", time.Minute)
```

需要跨厂商可切换时，依赖统一接口而非具体类型：

```go
var sms cloud.Sms = aliyun.GetSms() // 或 onenet.GetSms() / ctyun.GetSms()
_ = sms.SendSms(ctx, &cloud.SmsRequest{
    PhoneNumbers: []string{"13800000000"},
    TemplateID:   "SMS_001",
    Params:       map[string]string{"code": "1234"},
})
```

短信走 `notify` 通道时各厂商需要的 `Message.Metadata` 参数：aliyun/ctyun 需 `template_code`（其余键作为模板变量 JSON）；tencent 需 `template_id` 与按序逗号分隔的 `template_params`；huawei 需 `template_id`（其余键按序组成 JSON 数组）；onenet 需 `template_id`（可选 `sign_id`，其余键为具名模板参数）。华为云语音基于 VCP 1.0 `executeCall`，短信/语音仅支持 `cn-north-4` / `cn-south-1` 且必填 `project_id`；天翼云短信使用 EOP AK/SK 签名、单号码逐条下发，ZOS 通过 S3 兼容协议访问。

**i18n、验证码、TOTP、OIDC**

- `i18n.New("zh-CN")` + `bundle.Middleware()`。
- `captcha.New(cfg, store)` 生成图片验证码。
- `auth.GenerateTOTPSecret/VerifyTOTP` 提供 TOTP。
- `auth/oidc.New(ctx, cfg)` 完成 OIDC 授权码登录与 ID Token 校验。

**HTTP 客户端与中间件**

- `httpclient.New(httpclient.Options{...})` 内置超时、重试、熔断与 JSON 辅助。
- `idempotency.Middleware(store, ttl)` 提供 HTTP 幂等；gzip、超时、RequestID、安全 Header 等通用中间件需业务自行接入 gin-contrib 对应实现，CORS 由框架内置的 `server.cors` 提供。

**DNS-01 与密钥管理**

- DNS-01 使用 Let's Encrypt：实现 `web.DNSProvider` 并在启动前 `web.SetACMEDNSProvider(provider)`，将 `tls.acme.challenge` 设为 `dns-01`。
- `secrets.New("vault")` 或 `secrets.NewVault(cfg)` 读取 Vault KV v2；`secrets.NewRotating` 支持新旧密钥宽限期轮换。

## 错误处理

统一错误结构：

```go
type AppError struct {
    Code    int
    Message string
    Status  int
    Cause   error
}
```

错误分类：

- 参数错误、认证错误、授权错误。
- 资源不存在、业务冲突。
- 数据库错误、外部服务错误、内部错误。

要求：

- 底层错误保留 `Cause`，用于日志排查。
- 对外响应使用安全错误信息，不返回 SQL、堆栈、内部路径。
- 未知错误统一转换为内部错误。
- 业务错误码稳定，发布后不随意调整。
- 错误码约定为 `HTTP 状态码 * 100 + 序号`，例如 `40001`、`40100`、`50000`。

## SPA 前端集成

支持两种方式：

1. **Embed**：前端构建文件嵌入 Go 二进制。
2. **Directory**：前端文件由外部目录提供。

```go
//go:embed all:dist
var dist embed.FS

fsys, _ := fs.Sub(dist, "dist")
web.Configure(web.WithFileSystem(fsys))
```

路由规则：

- API 未知路由返回 JSON 404。
- 存在的静态文件返回文件。
- 不存在的前端路由回退到 `index.html`。
- `index.html` 使用 `no-cache`。
- 带 Hash 的静态资源可使用一年 `immutable` 缓存。
- 上传文件与 SPA 静态资源分离。
- 未启用 SPA 时，未知路由统一返回 JSON 404。

## TLS

```text
none：由可信反向代理终止 TLS
file：加载已有证书和私钥
acme：autocert 自动申请与续期
```

- 最低 TLS 1.2。
- ACME 支持 HTTP-01 与 TLS-ALPN-01；DNS-01 暂未实现。
- HTTP-01 需要独立监听 80 端口（`tls.acme.http_challenge_addr`）；TLS-ALPN-01 需要 443 端口可达。
- 证书与 ACME 账户缓存默认在 `data/acme`，目录权限为 0700。
- 建议先用 Let's Encrypt Staging Directory 验证，再切换生产；多实例部署应共享证书缓存或由负载均衡统一签发。
- 私钥和 ACME 账户信息必须妥善保护。

## 部署

上线前逐项核对 [生产部署检查清单](docs/deployment.md)（配置与密钥、外部依赖、TLS、多实例、任务队列与通知、云 SDK 凭据、发布验证）。

### Linux 裸机

- 单个 Go 可执行文件 + 外部配置文件 + 外部数据目录。
- systemd 管理，stdout 日志采集。
- SIGTERM 触发优雅退出。

### Docker

- 多阶段构建。
- 非 root 用户运行。
- 配置通过环境变量或挂载文件注入。
- 数据目录使用 Volume。
- 限制 CPU 和内存，配置健康检查。

### Nginx 反向代理

Nginx 可负责 TLS 终止、静态资源、访问限流、反向代理和请求体大小限制。应用必须严格配置可信代理，不能直接信任任意 `X-Forwarded-For`。

### 进程退出

`boot.Run` 已自动监听 SIGINT/SIGTERM 并执行以下流程（手动模式需自行实现）。收到 SIGTERM 后：

1. 停止接收新请求。
2. 等待现有请求完成。
3. 逆序停止模块。
4. 停止定时任务。
5. 排空并关闭协程池。
6. 关闭缓存。
7. 关闭数据库。
8. 刷新并关闭日志。

退出流程必须有超时限制。

## 测试与代码质量

```bash
go build ./...
go test ./...
go test -race ./...
go vet ./...
golangci-lint run
govulncheck ./...
```

测试覆盖方向：

- 单元测试、Service 测试、Repository 测试。
- HTTP 接口、中间件、安全组件测试。
- JWT 过期、错误签名、错误算法、Refresh Token 重放。
- 越权访问、跨租户资源访问、CSRF、SQL 注入、XSS、文件路径穿越、限流。
- 并发测试与优雅退出测试。

## 版本规划与状态

| 阶段 | 内容 | 状态 |
|---|---|---|
| V1.0 基础框架 | boot 生命周期、按包配置注册、全局实例、Gin Server、Router、Middleware、统一响应、错误处理、GORM、Zap、健康检查、优雅退出 | 已实现 |
| V1.1 认证与安全 | JWT、Refresh Token、Session、Argon2id、RBAC、资源授权、限流、CSRF、安全 Header、审计、AES 加密、API Key、TOTP、OIDC、验证码 | 已实现 |
| V1.2 基础设施 | Redis、Cache、Storage、Scheduler、EventBus、TLS/ACME、SPA Embed、Prometheus、协程池、安全上传、图片处理、Excel 导入导出、PDF 生成、数据库迁移、S3/MinIO、OpenTelemetry、OpenAPI、分布式锁、Outbox | 已实现 |
| V1.3 IoT 扩展 | MQTT Broker、MQTT 客户端、WebSocket Hub、设备鉴权、HMAC、MQTT ACL、设备命令、命令幂等、设备审计 | MQTT Broker/客户端/WebSocket 已实现；设备鉴权与命令幂等规划 |
| V1.4 平台能力 | API Key、通知、i18n、ClamAV、HTTP 客户端、gzip/timeout/幂等中间件、WebSocket topic 协议、DNS-01、Vault 与密钥轮换、sysinfo 实时资源分析、企业离线许可、微信鉴权、在线支付（微信/支付宝） | 已实现 |

后续扩展点：

- IoT 设备认证、MQTT ACL、设备命令幂等与设备审计。
- 分布式定时任务锁（`lock` 包已提供，调度器尚未内置集成）。
- KMS 与云 Secret Manager（Vault 已支持）。
- 可靠消息队列（NATS、Kafka、Redis Streams）。
- 搜索、特性开关、远程配置中心等可选扩展。

## 核心接口

```go
// boot 生命周期
func Register(name string, task *Module) error
func Startup(ctx context.Context) error
func Shutdown(ctx context.Context) error

type Module struct {
    Depends  []string
    Startup  func(ctx context.Context) error
    Shutdown func(ctx context.Context) error
}

// 配置注册
func Register[T any](name string, defaults T) *Section[T]
func (s *Section[T]) Get() *T

// 全局实例
database.Get() / cache.Get() / storage.Get() / upload.Get()
auth.Get() / permission.Get() / ratelimit.Get() / csrf.Get()
mqtt.Get() / broker.Get() / websocket.Get()
pool.Get() / scheduler.Get() / event.Get() / logger.Get()

// Web
web.Configure(opts ...WebOption)
web.Startup(ctx context.Context) error
web.Run(ctx context.Context) error
web.API() *gin.RouterGroup
web.Engine() *gin.Engine

// Database
type Database interface {
    DB() *gorm.DB
    Ping(ctx context.Context) error
    Transaction(ctx context.Context, fn func(tx *gorm.DB) error) error
    Stats() sql.DBStats
    Close() error
}

// Authenticator
type Authenticator interface {
    Authenticate(ctx context.Context, token string) (*Identity, error)
}

// Authorizer
type Authorizer interface {
    Check(ctx context.Context, subject Subject, action string, resource Resource) error
}

// PasswordHasher
type PasswordHasher interface {
    Hash(password string) (string, error)
    Verify(password, hash string) (bool, error)
}

// Encryptor
type Encryptor interface {
    Encrypt(plaintext []byte) ([]byte, error)
    Decrypt(ciphertext []byte) ([]byte, error)
}

// RateLimiter
type RateLimiter interface {
    Allow(ctx context.Context, key string) (bool, error)
}

// Auditor
type Auditor interface {
    Record(ctx context.Context, event Event) error
}

// Cache
type Cache interface {
    Get(ctx context.Context, key string, value any) error
    Set(ctx context.Context, key string, value any, ttl time.Duration) error
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
    Close() error
}

// Storage
type Storage interface {
    Put(ctx context.Context, key string, r io.Reader) error
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
    GetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Scheduler
type Scheduler interface {
    Register(name string, spec string, fn func(ctx context.Context) error) error
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Jobs() []Job
}

// EventBus
type EventBus interface {
    Publish(ctx context.Context, event Event) error
    Subscribe(name string, handler EventHandler) error
    Unsubscribe(name string) error
}
```

新增能力的常用入口：

```go
// 上传与图片
upload.Get().Save(ctx, tenantID, category, filename, reader)
upload.Get().Handler(upload.HTTPOptions{})
imagex.Process(reader, imagex.Options{MaxWidth: 1024, MaxHeight: 1024})

// 数据库迁移
runner, _ := migrate.New(db, migrate.WithLogger(logger))
runner.Register(migration)
runner.Up(ctx)
runner.Down(ctx, 1)
runner.Status(ctx)

// MQTT 客户端
mqtt.Get().Publish(ctx, topic, qos, retained, payload)
mqtt.Get().Subscribe(ctx, filter, qos, handler)

// MQTT Broker
broker.Get().Publish(topic, payload, qos, retained)
broker.Get().Stats()
broker.Get().Sessions()

// WebSocket
websocket.Get().Broadcast(payload)
websocket.Get().Send(subject, payload)
websocket.Get().Clients()
```

以上为框架公共接口。实现时需遵守统一的错误约定、资源关闭语义和并发安全要求。
