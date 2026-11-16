# Gin 通用 Go 后端框架技术设计文档

**版本：** V1.0  
**技术栈：** Go + Gin + GORM  
**架构模式：** 模块化单体（Modular Monolith）  
**适用范围：** IoT 平台、设备管理平台、企业管理系统、后台管理系统、开放 API 服务。

---

# 1. 项目概述

## 1.1 项目定位

构建一套基于 Go 语言和 Gin 的通用后端开发框架，为不同业务系统提供统一的基础设施、模块管理、认证授权、数据访问、日志、安全及服务运行能力。

框架不绑定具体业务，通过模块化设计，让业务项目只需实现业务模块，即可快速构建完整的后端应用。

## 1.2 设计目标

1. **轻量化：** 基于 Gin、GORM 等成熟组件，不重复实现成熟基础设施。
2. **模块化：** 各功能模块独立，支持按需启用和扩展。
3. **低耦合：** 业务逻辑不依赖 Gin、GORM 等具体技术实现。
4. **标准化：** 统一目录结构、接口规范、错误处理和响应格式。
5. **安全性：** 内置认证、授权、限流、审计、数据保护等安全能力。
6. **可扩展：** 支持 Redis、MySQL、PostgreSQL、对象存储及消息队列等扩展。
7. **易部署：** 支持单文件部署、Docker、Linux 服务及容器化运行。
8. **可测试：** 业务逻辑可独立测试，支持 Mock 和集成测试。

## 1.3 设计原则

- 默认安全，最小权限。
- 显式依赖注入，不依赖隐式全局状态。
- 优先接口抽象，不对所有功能过度封装。
- 模块化单体优先，不提前引入微服务复杂度。
- 业务逻辑与基础设施分离。
- 数据库为业务数据的最终可信来源。
- 配置、密钥和业务数据分离管理。
- 所有后台任务必须支持生命周期管理和优雅退出。

---

# 2. 总体架构

## 2.1 分层架构

整体分为五层：

### Application 层

负责应用程序生命周期和模块装配。

主要组件：
- App
- Bootstrap
- Module Manager
- Dependency Injection
- Lifecycle Manager

### Interface 层

负责对外提供访问入口。

主要组件：
- HTTP Server
- Gin Router
- Middleware
- HTTP Handler
- Request DTO
- Response DTO
- WebSocket（扩展）

### Business 层

负责业务逻辑。

主要组件：
- Service
- Domain
- Repository Interface
- Business Event
- Business Validation

### Infrastructure 层

负责基础设施实现。

主要组件：
- GORM Repository
- Database
- Redis
- Cache
- Storage
- Logger
- Scheduler
- Event Bus
- Cryptography

### Runtime 层

负责应用运行环境。

主要组件：
- Configuration
- TLS / ACME
- Health Check
- Metrics
- Tracing
- Graceful Shutdown

## 2.2 依赖关系

依赖必须遵循单向原则：

- Handler 依赖 Service。
- Service 依赖 Domain 和 Repository 接口。
- Repository 实现依赖 GORM 和 Database。
- Middleware 依赖认证、授权等抽象接口。
- Domain 不依赖 Gin、GORM。
- Service 不依赖 Gin Context。
- Infrastructure 不反向依赖业务 Handler。

业务模块不得直接操作其他模块的内部 Repository，应通过对外暴露的 Service 或模块接口协作。

## 2.3 技术选型

| 功能 | 技术 |
|---|---|
| Web 框架 | Gin |
| ORM | GORM v2 |
| 配置管理 | Viper |
| 日志 | Zap |
| 参数校验 | go-playground/validator |
| JWT | golang-jwt/jwt |
| 密码哈希 | Argon2id |
| Redis | go-redis |
| 定时任务 | robfig/cron |
| TLS/ACME | CertMagic |
| API 文档 | OpenAPI / swaggo |
| Metrics | Prometheus |
| Trace | OpenTelemetry |
| ID | UUID / Snowflake |
| 文件存储 | Local / S3 / MinIO |

框架不强制所有依赖必须启用。Redis、对象存储、消息队列等均采用可选适配器。

---

# 3. 项目目录结构

## 3.1 框架项目

```text
goframe/
├── app/                    # 应用生命周期
│   ├── app.go
│   ├── module.go
│   ├── lifecycle.go
│   └── options.go
│
├── config/                 # 配置管理
│   ├── config.go
│   ├── loader.go
│   ├── validator.go
│   └── watcher.go
│
├── server/                 # HTTP Server
│   ├── server.go
│   ├── options.go
│   └── tls.go
│
├── router/                 # 路由注册
│   ├── router.go
│   └── group.go
│
├── middleware/             # 通用中间件
│   ├── request_id.go
│   ├── recovery.go
│   ├── access_log.go
│   ├── cors.go
│   ├── security.go
│   ├── auth.go
│   ├── rate_limit.go
│   └── csrf.go
│
├── response/               # 统一响应
│   ├── response.go
│   ├── pagination.go
│   └── writer.go
│
├── errors/                 # 错误码和错误处理
│   ├── error.go
│   └── codes.go
│
├── auth/                   # 身份认证
│   ├── auth.go
│   ├── jwt.go
│   ├── password.go
│   ├── session.go
│   └── identity.go
│
├── permission/             # 权限管理
│   ├── authorizer.go
│   ├── rbac.go
│   ├── policy.go
│   └── resource.go
│
├── security/               # 安全基础设施
│   ├── crypto/
│   ├── csrf/
│   ├── ratelimit/
│   ├── sanitize/
│   ├── audit/
│   └── secrets/
│
├── database/               # 数据库
│   ├── database.go
│   ├── options.go
│   ├── mysql.go
│   ├── postgres.go
│   ├── transaction.go
│   └── health.go
│
├── logger/                 # 日志
│   ├── logger.go
│   └── zap.go
│
├── cache/                  # 缓存
│   ├── cache.go
│   ├── memory.go
│   └── redis.go
│
├── storage/                # 文件存储
│   ├── storage.go
│   ├── local.go
│   └── s3.go
│
├── scheduler/              # 定时任务
│   ├── scheduler.go
│   └── cron.go
│
├── event/                  # 事件总线
│   ├── event.go
│   └── bus.go
│
├── health/                 # 健康检查
│   ├── health.go
│   └── checks.go
│
├── monitor/                # 监控和链路追踪
│   ├── metrics.go
│   └── tracing.go
│
├── id/                     # ID 生成
│   └── id.go
│
├── internal/               # 内部实现
│
├── go.mod
└── README.md
```

## 3.2 业务项目

框架与业务项目分开管理，通过 Go Module 引用。

```text
iot-platform/
├── cmd/
│   └── server/
│       └── main.go
│
├── configs/
│   ├── config.yaml
│   └── config.example.yaml
│
├── internal/
│   ├── app/
│   │   └── modules.go
│   │
│   ├── bootstrap/
│   │   └── bootstrap.go
│   │
│   └── modules/
│       ├── user/
│       ├── tenant/
│       ├── device/
│       ├── auth/
│       └── system/
│
├── migrations/
├── storage/
├── web/
│   └── dist/
├── docs/
├── scripts/
├── go.mod
└── README.md
```

---

# 4. 应用生命周期与模块管理

## 4.1 App 结构

App 是整个应用的运行容器，负责装配和管理所有公共服务。

```go
type App struct {
    Config     *Config
    Logger     Logger
    Server     *Server

    DB         Database
    Cache      Cache
    Storage    Storage

    Auth       Authenticator
    Permission Authorizer

    Scheduler  Scheduler
    Events     EventBus

    Modules    *ModuleManager
}
```

App 不应包含具体业务对象，业务模块通过依赖注入获取需要的基础设施。

## 4.2 Module 接口

```go
type Module interface {
    Name() string
    Init(app *App) error
    RegisterRoutes(router gin.IRouter)
}
```

生命周期接口：

```go
type Lifecycle interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}
```

可选依赖接口：

```go
type Dependency interface {
    Dependencies() []string
}
```

模块管理器负责：

- 注册模块。
- 检查模块名称重复。
- 检查依赖是否存在。
- 检查循环依赖。
- 按依赖顺序初始化。
- 统一注册路由。
- 按顺序启动模块。
- 逆序停止模块。

## 4.3 生命周期顺序

```text
1. 加载配置
2. 配置校验
3. 初始化日志
4. 初始化数据库
5. 初始化缓存
6. 初始化存储
7. 初始化安全组件
8. 创建 App
9. 注册模块
10. 模块依赖检查
11. 初始化模块
12. 注册路由
13. 初始化定时任务
14. 启动后台服务
15. 启动 HTTP Server
16. 接收退出信号
17. 停止 HTTP Server
18. 停止后台服务
19. 关闭数据库和缓存
20. 刷新并关闭日志
```

初始化阶段失败应立即返回错误，不允许带着不完整依赖进入运行状态。

## 4.4 启动接口

```go
func New(opts ...Option) (*App, error)

func (app *App) Register(module Module) error

func (app *App) Init(ctx context.Context) error

func (app *App) Run(ctx context.Context) error

func (app *App) Shutdown(ctx context.Context) error
```

New 只负责构造对象，不直接监听端口或启动不可控的 Goroutine。

---

# 5. HTTP Server 与路由

## 5.1 Server 配置

```go
type ServerConfig struct {
    Host            string
    Port            int
    Mode            string

    ReadTimeout     time.Duration
    ReadHeaderTimeout time.Duration
    WriteTimeout    time.Duration
    IdleTimeout     time.Duration

    MaxHeaderBytes  int
    TrustedProxies  []string

    APIPrefix       string
    APIVersion      string
}
```

底层使用标准库 `http.Server`，不使用 Gin 的 `Run()` 作为生产运行入口。

## 5.2 路由分组

```text
/api/v1/
├── public/             # 公开接口
├── auth/               # 登录认证
├── user/               # 用户接口
├── admin/              # 管理接口
├── tenant/             # 租户接口
└── system/             # 系统接口
```

路由分组只用于组织接口和挂载中间件，不代表最终资源授权。

## 5.3 路由注册接口

```go
type Router interface {
    RegisterRoutes(router gin.IRouter)
}
```

业务模块只注册自己的路由，不允许随意修改全局中间件。

## 5.4 统一响应格式

```go
type Response[T any] struct {
    Code      int    `json:"code"`
    Message   string `json:"message"`
    Data      T      `json:"data"`
    RequestID string `json:"request_id"`
}
```

分页：

```go
type PageData[T any] struct {
    List     []T   `json:"list"`
    Total    int64 `json:"total"`
    Page     int   `json:"page"`
    PageSize int   `json:"page_size"`
}
```

业务成功码统一为 0，HTTP 状态码按实际语义使用。

- 200：成功。
- 201：创建成功。
- 202：异步任务已接收。
- 204：成功，无响应体。
- 400：请求参数错误。
- 401：未认证。
- 403：无权限。
- 404：资源不存在。
- 409：资源冲突。
- 429：请求过多。
- 500：服务器内部错误。

错误响应不得包含 SQL、堆栈、内部路径、密钥或数据库连接信息。

## 5.5 分页和排序

统一使用：

- page
- page_size
- sort
- order

限制最大 page_size，排序字段必须使用白名单映射，不允许将用户输入直接拼接进 SQL。

## 5.6 API 幂等

高风险操作支持 `Idempotency-Key`。

同一个 Key：

- 相同请求：返回首次执行结果。
- 不同请求内容：返回 409。
- 执行中：返回处理中状态。
- 失败：按照业务策略允许安全重试。

设备命令额外使用独立 `command_id`，不能仅依赖 HTTP 幂等键。

---

# 6. 认证与授权

## 6.1 认证和授权分离

认证解决：

> 当前请求是谁发起的？

授权解决：

> 当前请求是否允许执行该操作？

Session 解决：

> 当前登录会话是否仍然有效？

三者独立设计。

## 6.2 身份结构

```go
type Identity struct {
    UserID    string
    TenantID  string
    SessionID string

    Roles       []string
    Permissions []string

    AuthType string
}
```

身份由认证组件产生，不允许业务请求直接提交并覆盖 UserID、TenantID 等身份字段。

当前租户必须经过用户租户成员关系验证。

## 6.3 JWT 设计

Access Token：

- 有效期建议 15～30 分钟。
- 使用签名算法白名单。
- 校验 iss、aud、exp、nbf、iat、kid。
- 不允许 none 算法。
- 不放入密码、密钥等敏感信息。

Refresh Token：

- 使用高强度随机 opaque token。
- 服务端只保存 Token Hash。
- 有效期建议 7～30 天。
- 每次刷新轮换 Token。
- 检测旧 Token 重放。
- 支持主动撤销和退出全部会话。

Refresh Token 记录至少包含：

```text
id
user_id
session_id
family_id
token_hash
parent_token_id
created_at
expires_at
rotated_at
revoked_at
```

使用唯一索引保证 Token Hash 不重复。检测到已轮换 Token 再次使用时，应撤销对应 Token Family，并触发安全审计。

## 6.4 Session 管理

Session 字段：

```text
id
user_id
tenant_id
token_hash
device_name
ip
user_agent
created_at
last_active_at
expires_at
revoked_at
```

提供：

- 登录。
- 登出。
- 刷新 Token。
- 查看当前 Session。
- 查看全部 Session。
- 撤销指定 Session。
- 退出全部设备。
- 修改密码后撤销其他 Session。

## 6.5 密码安全

优先采用 Argon2id。

初始参数可参考：

```text
memory      = 65536 KiB
iterations  = 3
parallelism = 1
salt        = 16 bytes
output      = 32 bytes
```

该参数是初始配置，必须结合服务器性能和并发登录压力进行压测。

密码不能明文存储，也不能使用普通 MD5、SHA1 或 SHA256 直接保存。

## 6.6 登录防爆破

采用多维度限制：

- IP。
- 账号。
- IP + 账号。
- 全局登录请求。

采用渐进延迟、短时限制等措施，避免永久锁死账号造成拒绝服务。

登录失败统一返回信息，避免通过错误提示枚举账号。

## 6.7 授权模型

采用 RBAC + ABAC + Resource Authorization。

RBAC：
- 用户。
- 角色。
- 权限。
- 用户角色关系。
- 角色权限关系。

ABAC：
- 用户属性。
- 租户属性。
- 资源属性。
- 时间和环境属性。

Resource Authorization：
- 用户是否拥有资源。
- 用户是否属于资源所属租户。
- 用户是否通过分享关系获得权限。
- 当前权限是否允许操作该资源。

权限格式：

```text
user:read
user:create
user:update
user:delete

device:read
device:control
device:config
device:share

tenant:manage
system:config
```

授权必须在 Service 层执行，不能只依赖 Gin Middleware。

---

# 7. 数据库与 ORM

## 7.1 ORM 选择

默认采用 GORM v2。

设计原则：

- 普通业务使用 GORM。
- 复杂查询允许原生 SQL。
- 批量操作使用批量 SQL。
- 大数据量报表使用专门查询优化。
- 不为 ORM 再封装一层复杂 DSL。

## 7.2 Database 接口

```go
type Database interface {
    DB() *gorm.DB
    Ping(ctx context.Context) error
    Transaction(ctx context.Context, fn func(tx *gorm.DB) error) error
    Close() error
}
```

业务模块通过 Repository 接口访问数据。

## 7.3 Repository 规范

```go
type UserRepository interface {
    GetByID(ctx context.Context, id int64) (*User, error)
    GetByUsername(ctx context.Context, username string) (*User, error)
    Create(ctx context.Context, user *User) error
    Update(ctx context.Context, user *User) error
    Delete(ctx context.Context, id int64) error
}
```

Repository 实现：

```go
type GormUserRepository struct {
    db *gorm.DB
}
```

Service 不直接依赖 GORM。

## 7.4 事务

事务由 Service 控制业务边界。

跨多个 Repository 的事务必须共享同一个事务对象，不能每个 Repository 自己开启事务。

## 7.5 数据模型规范

建议：

- ID：BIGINT 或 UUID。
- 状态：TINYINT / SMALLINT。
- 金额：DECIMAL。
- 时间：DATETIME(3)，统一 UTC。
- JSON：仅用于非核心结构化扩展字段。
- 表名：snake_case。
- 索引：idx_、uk_。
- 删除：按业务需要选择软删除或物理删除。

跨 MySQL/PostgreSQL 时，优先使用 `int64` 作为 Go 侧通用整数 ID，避免依赖数据库不通用的 unsigned 类型。

## 7.6 迁移

生产环境不允许依赖 GORM AutoMigrate 自动修改核心表结构。

建议使用独立 migration 工具，支持：

- 创建表。
- 修改字段。
- 创建索引。
- 数据迁移。
- 回滚。
- 迁移版本记录。

审计日志、设备日志等通常不使用普通软删除。

---

# 8. 安全体系

安全能力作为框架的基础设施，不应依赖业务项目自行拼装。

## 8.1 安全模块划分

| 模块 | 主要职责 |
|---|---|
| HTTP Security | Header、CORS、CSRF、请求限制 |
| Authentication | JWT、Session、Token |
| Authorization | RBAC、ABAC、资源授权 |
| Input Security | 参数校验、SQL 注入、XSS |
| Network Security | HTTPS、TLS、代理信任 |
| Rate Limiting | 限流、并发控制、爆破防护 |
| Data Security | 加密、脱敏、密钥管理 |
| File Security | 文件上传、下载和路径安全 |
| Audit Security | 安全审计、操作审计 |
| Runtime Security | 配置、依赖和进程安全 |

安全原则：

1. 默认拒绝。
2. 最小权限。
3. 分层防御。
4. 敏感数据不明文保存。
5. 关键操作必须审计。
6. 安全组件异常时按业务风险决定是否失败关闭。

## 8.2 HTTP 安全

响应头：

```text
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: strict-origin-when-cross-origin
Permissions-Policy: camera=(), microphone=(), geolocation=()
```

HTTPS 部署后，根据域名部署情况配置 HSTS。

CSP 应根据实际 SPA 的 CDN、API、字体和脚本来源配置，建议先使用 Report-Only 模式观察，再正式启用。

CORS：

- 生产环境禁止任意 Origin。
- Cookie credentials 模式不允许通配 Origin。
- 只开放必要方法和 Header。
- CORS 不作为身份认证和资源授权机制。

请求限制：

- 使用 `http.MaxBytesReader` 限制 Body。
- 限制 Header 大小。
- 限制 Query 长度。
- 限制 JSON 深度和数组大小。
- JSON API 可按接口要求校验 Content-Type。
- 禁止不必要的 TRACE、CONNECT 方法。

## 8.3 CSRF

对于使用 Cookie 自动携带认证信息的接口，必须实现 CSRF 防护。

可采用：

- Synchronizer Token。
- Double Submit Cookie。

同时使用 Secure、HttpOnly、SameSite Cookie 属性。

SameSite 不能完全替代 CSRF Token。

纯 Bearer Token 接口应按认证模式区分处理。

## 8.4 输入校验

所有外部输入都必须校验：

- 必填字段。
- 长度。
- 格式。
- 数值范围。
- 枚举值。
- 数组大小。
- 嵌套层级。

SQL：

- 使用参数化查询。
- 禁止直接拼接用户输入。
- 动态表名、排序字段必须使用白名单。
- Raw SQL 必须审查。

XSS：

- 输出时进行上下文编码。
- 富文本使用 HTML Sanitizer。
- 不进行粗暴的全局字符串转义。

SSRF：

- 只允许必要协议。
- 禁止访问 localhost、私网、link-local 和云元数据地址。
- DNS 解析后校验 IPv4 和 IPv6 地址。
- 实际连接时必须使用已校验 IP，避免 DNS rebinding。
- 每次重定向都重新校验。
- 限制连接时间和响应体大小。
- 不允许用户控制代理配置。

## 8.5 文件安全

上传文件必须检查：

- 文件大小。
- 扩展名。
- MIME 类型。
- Magic Number。
- 文件内容。
- 文件名和路径。
- 压缩包解压大小、文件数量和压缩比。

不信任客户端文件名。

文件存储在 Web Root 外部，使用随机 Key，不直接使用原始文件名。

HTML、SVG 等主动内容需要谨慎处理，必要时作为附件下载。

下载文件必须再次检查资源授权。私有文件优先使用短期签名 URL。

## 8.6 网络安全

TLS：

- 最低 TLS 1.2。
- 优先 TLS 1.3。
- 私钥文件必须限制权限。
- 证书到期需要告警。
- 支持 CertMagic 自动续期。
- ACME 生产环境启用前先使用 Staging 测试。

反向代理：

- 严格配置 TrustedProxies。
- 不信任任意客户端提交的 X-Forwarded-For。
- 只有可信代理可以提供真实客户端 IP。

WebSocket：

- 校验 Origin。
- 校验握手身份。
- 限制消息大小。
- 限制连接数量。
- 设置读写超时和心跳。
- 对长连接中的敏感操作重新检查授权。

## 8.7 限流和并发控制

限流维度：

- IP。
- User。
- Tenant。
- API。
- Global。

并发控制：

- 请求并发。
- 数据库操作并发。
- 文件处理并发。
- 后台任务并发。

算法：

- Token Bucket。
- Sliding Window。
- Fixed Window。
- Semaphore。

多实例部署时使用 Redis 或网关共享限流状态。

高风险接口在限流服务不可用时应失败关闭；普通查询接口可按明确策略降级。

## 8.8 数据加密

密码使用 Argon2id，不使用可逆加密。

敏感字段使用：

- AES-256-GCM。
- ChaCha20-Poly1305。

加密数据应采用版本化 Envelope 格式，包含 Key ID、版本和 nonce。

AES-GCM nonce 必须保证同一密钥下不重复。

密钥来源：

- 环境变量。
- Secret Manager。
- KMS。
- Vault。

支持密钥轮换。

日志禁止记录：

- 密码。
- JWT。
- Refresh Token。
- API Secret。
- 设备完整密钥。
- 数据库连接密码。

## 8.9 API Key

API Key 必须支持：

- Hash 存储。
- Scope 权限。
- 有效期。
- 主动撤销。
- 密钥轮换。
- 最后使用时间。
- 调用来源限制。

不允许数据库保存 API Key 明文。

## 8.10 审计日志

重点审计：

- 登录、登出。
- 登录失败。
- 用户和角色变更。
- 权限变更。
- 租户变更。
- 设备绑定和分享。
- 设备远程控制。
- 系统配置修改。
- 文件上传和删除。
- 管理员操作。

建议字段：

```text
id
actor_type
actor_id
user_id
tenant_id
action
resource_type
resource_id
result
reason_code
source_ip
user_agent
request_id
trace_id
detail
created_at
```

actor_id 可用于记录用户、设备、API Key 或系统任务。

审计事件不可由普通用户修改或删除。detail 必须限制大小并过滤敏感字段。

高价值操作可使用事务 Outbox 保证业务数据和审计事件一致，避免异步日志丢失。

## 8.11 IoT 设备安全

设备认证：

- 独立 Device Secret。
- HMAC 签名。
- mTLS。
- 短期设备 Token。

设备签名至少绑定：

```text
device_id
key_id
timestamp
nonce
method
path
query
body_hash
```

服务端验证：

1. 设备身份。
2. 密钥有效性。
3. 签名正确性。
4. 时间窗口。
5. nonce 唯一性。
6. 设备状态。
7. 设备权限。

nonce 使用 Redis 原子 SET NX + TTL 或其他具备原子唯一性保障的存储。

设备命令必须包含：

```text
command_id
device_id
tenant_id
action
payload
created_at
expires_at
```

服务端和设备端均应持久化命令处理状态，避免重启后重复执行。

MQTT：

- 每台设备独立身份。
- 每台设备独立 ACL。
- Broker 强制限制 Topic。
- 启用 TLS。
- 支持设备密钥轮换。
- 设备禁用后立即撤销连接和权限。

建议 Topic：

```text
iot/{product_id}/{device_id}/up
iot/{product_id}/{device_id}/down
iot/{product_id}/{device_id}/event
iot/{product_id}/{device_id}/command
iot/{product_id}/{device_id}/reply
```

## 8.12 安全中间件顺序

建议逻辑顺序：

```text
RequestID
    ↓
Recovery
    ↓
Security Headers
    ↓
Trusted Proxy / Client IP
    ↓
Method Filter
    ↓
CORS / OPTIONS
    ↓
Global Rate Limit
    ↓
Request Body Limit
    ↓
Authentication
    ↓
CSRF（Cookie Auth 的非安全方法）
    ↓
Tenant Context
    ↓
Permission
    ↓
Handler
```

Access Log 在请求完成后记录。

Audit 由 Service 在业务操作后产生。

登录接口必须在认证之前执行限流。OPTIONS 预检通常应在认证前短路。

客户端提供的 X-Request-ID 必须校验格式和长度，不能将其当作身份凭证。

## 8.13 Runtime 安全

- 生产环境关闭 Debug。
- 生产环境禁止默认 JWT Key。
- 配置文件不提交真实 Secret。
- 服务使用非 root 运行。
- 容器限制 CPU、内存和文件系统权限。
- 关闭不必要的 pprof。
- 后台 Goroutine 必须受 Context 管理。
- Recovery 记录堆栈，但不向客户端返回堆栈。
- 使用 govulncheck 检查依赖漏洞。
- 生成 SBOM，固定依赖版本。

---

# 9. 配置管理

## 9.1 配置加载顺序

```text
Default
  ↓
Config File
  ↓
Environment Variables
  ↓
Command Line Arguments
```

采用 Viper。

## 9.2 配置结构

```yaml
app:
  name: iot-platform
  version: 1.0.0
  environment: production

server:
  host: 0.0.0.0
  port: 8080
  mode: release
  read_timeout: 30s
  write_timeout: 30s
  idle_timeout: 60s
  max_header_bytes: 1048576
  trusted_proxies: []

database:
  driver: mysql
  dsn: ${DB_DSN}
  max_open_conns: 100
  max_idle_conns: 10
  conn_max_lifetime: 30m

redis:
  enabled: true
  addr: ${REDIS_ADDR}
  password: ${REDIS_PASSWORD}
  db: 0

security:
  enabled: true

  headers:
    enabled: true
    hsts: true

  cors:
    enabled: true
    allowed_origins:
      - https://admin.example.com
    allow_credentials: true

  request:
    max_body_size: 10MB
    max_header_bytes: 1MB
    max_query_length: 4096

  auth:
    enabled: true
    access_token_ttl: 30m
    refresh_token_ttl: 720h

  password:
    algorithm: argon2id
    memory: 65536
    iterations: 3
    parallelism: 1

  rate_limit:
    enabled: true
    global_rate: 1000
    global_burst: 2000

  csrf:
    enabled: true

  upload:
    max_size: 50MB
    scan_enabled: false

  audit:
    enabled: true

  encryption:
    algorithm: aes-256-gcm
    key_provider: env

tls:
  mode: file
  cert_file: ""
  key_file: ""

spa:
  enabled: true
  mode: embed
  path: web/dist

storage:
  driver: local
  path: storage

logger:
  level: info
  format: json
  output: stdout

scheduler:
  enabled: true
```

说明：

- `${DB_DSN}` 等 Secret 必须由运行环境注入。
- `scan_enabled: false` 时，不允许未经检查的高风险文件进入业务流程。
- HSTS 仅在正确部署 HTTPS 后启用。
- CSP 不应直接使用通用示例代替实际 SPA 策略。

## 9.3 配置热更新

可以热更新：

- 日志等级。
- 部分动态业务参数。
- 明确支持动态调整的限流规则。

通常需要重启或专门重建的配置：

- 监听地址。
- 数据库连接。
- TLS 模式。
- ACME 域名。
- 存储后端。

---

# 10. 日志系统

采用 Zap。

日志分类：

```text
app.log
access.log
audit.log
error.log
```

推荐生产环境输出 JSON 到 stdout，由系统采集。

统一字段：

```text
timestamp
level
message
request_id
trace_id
user_id
tenant_id
module
```

要求：

- 敏感信息脱敏。
- 错误日志包含错误上下文。
- 请求日志包含状态码、耗时和请求 ID。
- 不记录密码、Token、Secret。
- 避免记录完整文件内容和大型请求 Body。

---

# 11. 缓存系统

统一 Cache 接口：

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

- MemoryCache。
- RedisCache。

缓存只用于提升性能，不作为核心业务数据的唯一来源。

需要明确：

- TTL。
- 缓存一致性。
- 缓存穿透。
- 缓存击穿。
- 缓存雪崩。
- 多租户 Key 隔离。

---

# 12. 文件存储

统一 Storage 接口：

```go
type Storage interface {
    Put(ctx context.Context, key string, r io.Reader) error
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
    GetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}
```

实现：

- LocalStorage。
- S3Storage。
- MinIOStorage。

文件 Key 建议：

```text
{tenant_id}/{category}/{yyyy}/{mm}/{random_id}
```

文件元数据独立存储，包括文件名、大小、MIME、存储 Key、上传者、租户和创建时间。

---

# 13. 定时任务与事件系统

## 13.1 Scheduler

采用 robfig/cron。

支持：

- 定时任务注册。
- 任务启动和停止。
- 任务状态。
- 错误记录。
- 执行超时。
- 多实例分布式锁。

多实例环境必须防止同一任务重复执行。

## 13.2 EventBus

第一版使用进程内事件总线。

接口：

```go
type EventHandler func(ctx context.Context, event Event) error

type EventBus interface {
    Publish(ctx context.Context, event Event) error
    Subscribe(name string, handler EventHandler) error
    Unsubscribe(name string) error
}
```

后续可扩展：

- MQTT。
- NATS。
- Kafka。
- Redis Streams。

进程内事件总线不能被当作可靠消息队列。涉及资金、设备命令、审计等关键流程时，使用数据库事务 Outbox 或可靠消息机制。

---

# 14. SPA 前端集成

支持两种方式：

1. Embed：前端构建文件嵌入 Go 二进制。
2. Directory：前端文件由外部目录提供。

Embed 示例：

```go
//go:embed all:dist
var dist embed.FS
```

使用 `fs.Sub` 获取实际静态资源目录。

路由规则：

- API 未知路由返回 JSON 404。
- 存在的静态文件返回文件。
- 不存在的前端路由返回 index.html。
- index.html 使用 no-cache。
- 带 Hash 的静态资源可以使用一年 immutable 缓存。
- 支持 ETag、Last-Modified 和 HEAD。
- 上传文件和 SPA 静态资源分离。

建议独立 SPA Handler，不要把静态资源处理全部塞进通用 Middleware。

---

# 15. TLS 与 ACME

支持：

```text
none
file
acme
```

none：不由应用提供 TLS，适用于可信反向代理终止 TLS 的环境。

file：加载已有证书和私钥。

acme：通过 CertMagic 自动申请、续期证书。

ACME 需要：

- Email。
- Directory URL。
- 域名列表。
- Challenge 类型。
- 证书缓存目录。
- 多实例协调机制。

HTTP-01 要求外部 80 端口可访问；DNS-01 需要对应 DNS Provider。

私钥和 ACME 账户信息必须妥善保护。

---

# 16. 健康检查与监控

提供：

```text
GET /health
GET /health/live
GET /health/ready
GET /metrics
```

说明：

- live：进程是否存活。
- ready：服务是否可以正常接收请求。
- health：聚合依赖检查。
- metrics：Prometheus 指标。

监控：

- HTTP 请求数量。
- HTTP 请求耗时。
- HTTP 错误率。
- 数据库连接池。
- Redis 状态。
- Goroutine 数量。
- 内存使用量。
- 定时任务状态。
- 后台队列长度。

Metrics 和调试接口不得无保护地暴露到公网。

---

# 17. 错误处理规范

统一错误结构：

```go
type AppError struct {
    Code    int
    Message string
    Cause   error
}
```

错误分类：

- 参数错误。
- 认证错误。
- 授权错误。
- 资源不存在。
- 业务冲突。
- 数据库错误。
- 外部服务错误。
- 内部错误。

要求：

- 底层错误保留 Cause。
- 对外响应使用安全错误信息。
- 日志保留错误上下文。
- 业务错误码稳定。
- 不将数据库错误直接返回客户端。

---

# 18. 部署设计

支持：

## 18.1 Linux 裸机

- 单个 Go 可执行文件。
- 外部配置文件。
- 外部数据目录。
- systemd 管理。
- stdout 日志采集。
- SIGTERM 优雅退出。

## 18.2 Docker

- 多阶段构建。
- 非 root 用户运行。
- 配置通过环境变量或挂载文件注入。
- 数据目录使用 Volume。
- 设置 CPU 和内存限制。
- 配置健康检查。

## 18.3 Nginx 反向代理

Nginx 可负责：

- TLS 终止。
- 静态资源。
- 访问限流。
- 反向代理。
- 请求体大小限制。

Gin 必须严格配置可信代理，不能直接信任任意 X-Forwarded-For。

## 18.4 进程退出

接收到 SIGTERM 后：

1. 停止接收新请求。
2. 等待现有请求完成。
3. 停止定时任务。
4. 停止后台任务。
5. 关闭数据库。
6. 关闭 Redis。
7. Flush 日志。

退出流程必须有超时限制。

---

# 19. 测试与代码质量

必须支持：

- 单元测试。
- Service 测试。
- Repository 测试。
- HTTP 接口测试。
- 中间件测试。
- 安全测试。
- 数据库集成测试。
- 并发测试。

推荐工具：

```text
go test ./...
go test -race ./...
go vet ./...
golangci-lint run
govulncheck ./...
```

关键安全测试：

1. JWT 过期、错误签名、错误算法。
2. Refresh Token 重放。
3. 登录爆破。
4. 越权访问其他用户资源。
5. 跨租户资源访问。
6. CSRF。
7. SQL 注入。
8. XSS。
9. SSRF。
10. 文件路径穿越。
11. 文件类型伪造。
12. 限流绕过。
13. MQTT Topic 越权。
14. 设备命令重复执行。
15. 审计事件丢失。

---

# 20. 框架核心接口汇总

第一版需要优先稳定以下公共接口：

```go
// Application
type App interface {
    Init(ctx context.Context) error
    Run(ctx context.Context) error
    Shutdown(ctx context.Context) error
}

// Module
type Module interface {
    Name() string
    Init(app *App) error
    RegisterRoutes(router gin.IRouter)
}

// Lifecycle
type Lifecycle interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}

// Database
type Database interface {
    DB() *gorm.DB
    Ping(ctx context.Context) error
    Transaction(ctx context.Context, fn func(tx *gorm.DB) error) error
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
    Record(ctx context.Context, event AuditEvent) error
}

// Cache
type Cache interface {
    Get(ctx context.Context, key string, value any) error
    Set(ctx context.Context, key string, value any, ttl time.Duration) error
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
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
}

// EventBus
type EventBus interface {
    Publish(ctx context.Context, event Event) error
    Subscribe(name string, handler EventHandler) error
    Unsubscribe(name string) error
}
```

以上为核心接口示意，最终代码需要统一错误约定、资源关闭语义和并发安全要求。

---

# 21. 版本规划与开发优先级

## V1.0：基础框架

优先完成：

1. App 生命周期。
2. Module 管理。
3. 配置管理。
4. Gin Server。
5. Router。
6. Middleware。
7. 统一响应。
8. 错误处理。
9. GORM 数据库。
10. Zap 日志。
11. Health Check。
12. 优雅退出。

目标：能够运行一个完整、可部署的业务 API 服务。

## V1.1：认证与安全

完成：

1. JWT。
2. Refresh Token。
3. Session。
4. Password Hash。
5. RBAC。
6. 资源授权。
7. 请求限流。
8. CSRF。
9. 安全 Header。
10. 审计日志。
11. 数据加密。
12. API Key。

目标：能够安全运行具有用户、角色和多租户体系的业务系统。

## V1.2：基础设施扩展

完成：

1. Redis。
2. Cache。
3. Storage。
4. Scheduler。
5. EventBus。
6. TLS / ACME。
7. SPA Embed。
8. Prometheus。
9. OpenTelemetry。

目标：支持复杂业务系统的运行需求。

## V1.3：IoT 扩展

完成：

1. Device Auth。
2. Device HMAC。
3. MQTT ACL 对接。
4. Device Command。
5. Command Idempotency。
6. Tenant Resource Authorization。
7. WebSocket。
8. 设备审计。

目标：能够直接支撑 IoT 平台、设备管理和远程控制系统。

---

# 22. 最终设计结论

本框架采用 **Gin + GORM + 模块化单体 + 分层架构**。

Gin 负责 HTTP 接入，GORM 负责数据库访问，业务模块通过 Service 和 Repository 接口解耦，App 统一管理应用生命周期和基础设施。

核心设计重点：

1. 框架与业务项目独立，业务通过 Module 扩展。
2. 认证、授权、Session 分离，支持 RBAC、ABAC 和资源授权。
3. 安全作为基础设施内置，覆盖 HTTP、网络、数据、文件、运行时和 IoT 安全。
4. 基础设施通过接口抽象，支持不同实现按需替换。
5. 优先模块化单体，后续可以按业务边界拆分服务。
6. 以稳定接口、明确生命周期和统一规范为核心，避免框架过度设计。

第一阶段不建议同时实现所有功能。应先完成 App、Module、Config、Server、Database、Logger、Response、Middleware 八个核心模块，再逐步补充认证、安全和基础设施扩展。