# goframe 使用说明

本文面向第一次接入 goframe 的业务开发者，按"如何完成一件事"的方式组织。组件级 API 的完整说明见 [README.md](../README.md)，生产部署检查清单见 [deployment.md](deployment.md)。

## 1. 概述

goframe（module `github.com/god-jason/goframe`）是一个面向业务系统的 Go 应用框架库：以 `boot` 生命周期编排为中心，把配置、HTTP 服务、数据库、缓存、任务队列、通知、云服务、安全、可观测等能力封装为可独立启停的组件，业务只需要写 main 骨架、配置文件和业务模块。

### 1.1 组件全景

| 分组 | 组件（配置段） | 说明 |
|---|---|---|
| 核心 | `boot`、`config`（app）、`logger`（logger）、`web`（server/tls/pprof/spa/openapi）、`api` | 生命周期、配置、日志、HTTP 服务与统一响应 |
| 数据与存储 | `database`（database）、`cache`/`redis`（redis）、`storage`（storage）、`upload`（security.upload） | GORM 数据库、Redis 缓存、文件存储、上传 |
| 任务与消息 | `taskqueue`（taskqueue）、`scheduler`（scheduler）、`pool`（pool）、`event`、`event/outbox`（outbox）、`notify`（notify） | 异步任务、定时任务、协程池、进程内事件、事务性发件箱、多渠道通知 |
| 云服务 | `cloud` + `cloud/aliyun`、`cloud/tencent`、`cloud/huawei`、`cloud/onenet`、`cloud/ctyun`（aliyun/tencent/huawei/onenet/ctyun） | 短信/语音/对象存储统一接口与五厂商实现 |
| 安全与认证 | `auth`（security.auth）、`auth/oidc`、`auth/gormstore`、`permission`、`security/ratelimit`（security.rate_limit）、`security/csrf`（security.csrf）、`security/audit`（security.audit）、`security/crypto`（security.encryption）、`secrets`、`license`（license）、`security/captcha`、`i18n` | 认证授权、限流、CSRF、审计、加密、密钥管理、许可、验证码、国际化 |
| 网络通信 | `httpclient`、`lock`、`idempotency`、`mqtt`（mqtt）、`broker`（broker）、`websocket`（websocket） | HTTP 客户端、分布式锁、幂等、MQTT 客户端、内置 MQTT Broker、WebSocket |
| 可观测与诊断 | `trace`（trace）、`sysinfo`（sysinfo）、`web` pprof（pprof） | OpenTelemetry 链路、系统信息接口、性能剖析 |
| 支付与回调 | `payment`（payment）、`notify` webhook 通道、`weixin`（weixin） | 微信/支付宝统一支付与回调，外部 webhook 接收，微信生态 |

### 1.2 适用前提

- Go 1.26 及以上（见 go.mod）。
- 按需的外部依赖：
  - MySQL（`database`、`auth/gormstore`、`outbox`）；
  - Redis（`cache`、`taskqueue`、`lock`、`ratelimit`、`idempotency`、`payment` 回调去重）；
  - SMTP 服务或 HTTP 端点（`notify` 的 smtp/webhook 通道）；
  - S3/MinIO（`storage` 的 s3 驱动，或用 local 驱动落本地盘）；
  - 云厂商账号（`cloud/*`、`payment`、`weixin`，均为可选）。
- 所有组件都是"配置段缺省即用默认值"，不需要的组件不配置、不导入即可完全跳过。

## 2. 安装与项目引入

```bash
go get github.com/god-jason/goframe
```

业务 main 包匿名导入 `bootstrap`，一次性注册全部标准组件的配置段与 boot 任务：

```go
import (
    "context"
    "log"

    "github.com/god-jason/goframe/boot"
    _ "github.com/god-jason/goframe/bootstrap" // 注册全部标准组件
    "github.com/god-jason/goframe/config"
    "github.com/god-jason/goframe/web"
)
```

`bootstrap` 不包含 `cloud/*` 厂商包（避免引入全部云 SDK），需要云能力时按厂商匿名导入，例如 `import _ "github.com/god-jason/goframe/cloud/aliyun"`（见第 6.4 节）。希望精简依赖的项目也可以不导入 `bootstrap`，改为只匿名导入用到的组件包。

### 2.1 最小 main 骨架

```go
func main() {
    // 可选：显式指定配置文件；不指定时自动搜索（见 3.1）
    config.SetFile("configs/config.yaml")

    // serve 在所有基础设施启动完成后执行，最后启动 HTTP 服务并阻塞
    serve := func(ctx context.Context) error {
        // 这里初始化业务模块并挂载路由（见第 5 章）
        return web.Run(ctx) // 阻塞直到 ctx 取消或服务退出
    }

    // boot.Run：监听 SIGINT/SIGTERM → 按依赖拓扑 Startup → 运行 serve → 带超时逆序 Shutdown
    if err := boot.Run(context.Background(), serve); err != nil {
        log.Fatalf("run: %v", err)
    }
}
```

`boot.Run(ctx, serve, opts...)` 的要点：

- 自动监听 SIGINT/SIGTERM，收到信号后取消传给 `serve` 的 ctx；
- `serve` 返回、ctx 取消或收到信号后，用独立的超时 context（默认 30s，可用 `boot.WithShutdownTimeout(d)` 覆盖）逆序释放组件；
- `serve` 传 nil 时只等待退出信号，适合纯后台进程；
- 启动失败时 `Startup` 内部已自动回滚，`Run` 不会重复释放。

## 3. 配置系统

### 3.1 配置文件加载顺序

`config.Startup`（由 boot 最先执行）按以下优先级确定配置文件：

1. `config.SetFile(path)` 显式指定的路径（文件必须存在，否则启动报错）；
2. 环境变量 `GOFRAME_CONFIG` 指定的路径；
3. 依次在当前工作目录、可执行文件所在目录下查找：`configs/config.yaml`、`configs/config.yml`、`config.yaml`、`config.yml`。

全部找不到时不报错，各组件使用代码内注册的默认值。示例配置见 [configs/config.example.yaml](../configs/config.example.yaml)。

### 3.2 环境变量注入

配置文件内容在解析前做占位符展开，支持两种形式：

```yaml
security:
  auth:
    secret: "${JWT_SECRET}"              # 未设置时展开为空
database:
  password: "${DB_PASSWORD:-changeme}"   # 未设置时使用默认值
```

此外每个配置键都可以用 `GOFRAME_<段>_<键>` 环境变量直接覆盖（点与短横线转下划线并大写），即使该键在文件里不存在：

```bash
export GOFRAME_SERVER_PORT=9090                      # server.port
export GOFRAME_LOGGER_LEVEL=debug                    # logger.level
export GOFRAME_SECURITY_AUTH_ACCESS_TOKEN_TTL=30m    # security.auth.access_token_ttl
```

### 3.3 热更新

配置文件存在时，`config` 组件会监听文件变化并原子替换所有配置段快照（读取方通过 `Get()` 拿到的指针不会撕裂）。业务可以用 `config.OnReload(fn)` 注册回调感知重载；`config.Reload()` 可手动触发。注意：热更新只改变配置值，连接池、监听端口等需要重建资源的行为不会自动生效。

### 3.4 新增配置组件的注册约定

每个组件包在自己的包内声明配置结构体并在 `init` 中注册，遵循统一惯例：

```go
// Config 是本组件配置。所有字段用 yaml tag 对应配置文件的键。
type Config struct {
    Enabled bool          `yaml:"enabled"`
    Timeout time.Duration `yaml:"timeout"` // 支持 "30s" 这类字符串
}

// DefaultConfig 返回默认值；缺失字段加载时保留默认值。
func DefaultConfig() Config {
    return Config{Enabled: false, Timeout: 30 * time.Second}
}

// cfg 在 init 注册，段名即配置文件里的顶层键（支持点分嵌套如 "security.auth"）。
var cfg = config.Register("mycomponent", DefaultConfig())

// GetConfig 返回当前配置快照（只读，不要修改）。
func GetConfig() *Config { return cfg.Get() }

// normalize 在 Validate 前补齐/清洗字段；Validate 返回配置错误则启动失败。
func (c Config) Validate() error {
    if c.Timeout <= 0 {
        return fmt.Errorf("mycomponent: timeout must be positive")
    }
    return nil
}
```

解码约定：结构体字段统一使用 `yaml:"..."` tag；`time.Duration` 可写 `"1m30s"` 字符串；`[]string` 可写 `"a,b,c"` 逗号串；`config.ByteSize` 可写 `"10MB"`。

## 4. 启动与生命周期

### 4.1 boot.Module 与依赖拓扑

组件在 `init` 中向注册表声明自己：

```go
func init() {
    _ = boot.Register("mycomponent", &boot.Module{
        Depends:  []string{"config", "logger"}, // 先启动依赖，后释放依赖
        Startup:  startup,                       // func(ctx context.Context) error
        Shutdown: shutdown,                      // func(ctx context.Context) error，可为 nil
    })
}
```

- `Startup(ctx)`：加载配置、建立连接、注册路由等；返回 error 会中止启动并逆序回滚已启动组件。
- `Shutdown(ctx)`：释放资源；ctx 带超时（默认 30s），实现里应自行响应 ctx 取消。
- 启动顺序 = 注册顺序 + 依赖拓扑；释放顺序与启动顺序相反。

### 4.2 标准启动顺序

导入 `bootstrap` 后，典型启动顺序为：

```text
config → logger → database → cache → storage → upload → trace → crypto → auth
→ ratelimit → csrf → audit → license → pool → event → outbox → taskqueue → notify
→ scheduler → payment → mqtt → broker → websocket → weixin → web
```

`web` 依赖几乎所有组件所以最后启动；`cloud/*` 厂商模块被匿名导入时排在 `notify` 之前启动，以便把短信服务商注册进 notify。

### 4.3 手动编排（不使用 boot.Run）

需要自定义信号处理或分阶段启动时，可以直接调用：

```go
ctx, stop := boot.WithSignal(context.Background()) // 派生监听 SIGINT/SIGTERM 的 ctx
defer stop()

if err := boot.Startup(ctx); err != nil {
    log.Fatal(err)
}
// ... 业务初始化、运行 ...
shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
if err := boot.Shutdown(shutdownCtx); err != nil {
    log.Printf("shutdown: %v", err)
}
```

## 5. 业务模块开发

框架推荐的业务组织方式参考 [example/](../example) 目录：每个业务域一个模块包，包含 `Module`（装配）、`Service`（业务逻辑）、`Handler`（HTTP 处理）。

### 5.1 模块三件套

```go
// module.go —— 装配
type Module struct{ handler *Handler }

func New() *Module { return &Module{} }

// Init 在 boot.Startup 之后手动调用：构造依赖、初始化数据。
func (m *Module) Init() error {
    hasher, err := auth.NewPasswordHasher(auth.GetConfig().Password)
    if err != nil {
        return err
    }
    service := NewService(NewUserRepository(), hasher)
    m.handler = NewHandler(service, auth.Get(), permission.Get())
    return nil
}

// Mount 把模块登记的接口挂到 web 的 API 分组。
func (m *Module) Mount(router gin.IRoutes) error {
    return m.handler.Mount(router)
}
```

```go
// handler.go —— 接口登记与处理
func (h *Handler) RegisterAPIs() {
    // 公开接口（无需认证）
    api.RegisterPublic("GET", "/public/ping", h.ping)
    // 需登录接口
    api.Register("GET", "/user/me", h.me)
    // 需登录 + 权限
    api.Register("GET", "/system/info",
        api.RequirePermission(h.permission, "system:read", nil), h.systemInfo)
}

func (h *Handler) Mount(router gin.IRoutes) error {
    return api.Mount(router) // 把注册表里的接口统一挂到 /api/v1 分组
}

func (h *Handler) ping(c *gin.Context) {
    api.Success(c, gin.H{"pong": true}) // 统一响应 {code,message,data,request_id}
}
```

要点：

- `api.Register*` 只是登记，真正挂载发生在 `api.Mount(web.API())`，因此路由前缀、认证守卫、审计、CSRF 等全局中间件由框架统一注入；
- `web.API()` 返回 `/{server.api_prefix}/{server.api_version}` 分组（默认 `/api/v1`），组内接口默认需要认证，公开接口用 `RegisterPublic`；
- 响应统一走 `api.Success/Created/Accepted/NoContent/Fail`，错误用 `errors` 包构造的业务错误（如 `apperrors.NotFound("user")`）由 `Fail` 映射为合适的 HTTP 状态码；
- 分页参数用 `api.PageQuery`（`page/page_size/sort/order`），返回用 `api.NewPage(list, total, page, pageSize)`。

### 5.2 main 中的装配顺序

```go
serve := func(ctx context.Context) error {
    demo := demo.New()
    if err := demo.Init(); err != nil {
        return fmt.Errorf("initialize demo: %w", err)
    }
    if err := demo.Mount(web.API()); err != nil {
        return fmt.Errorf("mount demo: %w", err)
    }
    return web.Run(ctx)
}
return boot.Run(context.Background(), serve)
```

## 6. 各组件使用

以下按分组给出每个组件的配置段、启用方式与核心 API。所有段名以源码 `config.Register` 为准；未写出的字段见 configs/config.example.yaml 注释。

### 6.1 核心

#### web（HTTP 服务）

- 配置段：`server`（host/port/mode/read_timeout/write_timeout/shutdown_timeout/trusted_proxies/api_prefix/api_version/request.*）、`tls`（证书与 ACME）、`pprof`、`spa`、`openapi`。
- 启用：匿名导入即注册；`web.Run(ctx)` 在 serve 中阻塞运行 HTTP 服务。

```go
// 获取引擎与 API 分组
engine := web.Engine() // *gin.Engine，可挂全局中间件
group := web.API()     // /api/v1 分组
addr := web.Addr()     // 实际监听地址

// 健康检查与指标注册（供探针抓取）
web.Health().Add(health.Check{Name: "db", Critical: true, Fn: db.Ping})
web.Health().Add(health.Check{Name: "redis", Fn: func(ctx context.Context) error {
    return rdb.Ping(ctx).Err()
}})
```

- 注意：`server.mode` 只接受 `debug/release/test`；`api_prefix` 必须以 `/` 开头；生产环境务必配置 `trusted_proxies` 以便取真实客户端 IP。

#### logger

- 配置段：`logger`（level/format/output/rotation.*）。默认 `info`、`json`、`logs/app.log`、开启滚动。
- 使用：

```go
log := logger.Get() // logger.Logger 接口，boot 启动后可用；启动前返回 Nop 或默认实现
log.Info("order created",
    logger.String("order_no", orderNo),
    logger.Int64("amount", amount),
    logger.Error(err), // 常用字段构造器：String/Int/Int64/Bool/Duration/Time/Any/Error/Stack...
)
sub := log.With(logger.String("component", "payment")) // 派生带公共字段的 logger
```

- 注意：业务代码只依赖 `logger.Logger` 接口，不要直接 import zap；`logger.New(logger.Options{...})` 可创建独立实例（如审计专用日志）。

#### config

见第 3 章。核心 API：`config.SetFile/File/Reload/OnReload/App()` 与泛型句柄 `config.Register[T](name, defaults)`、`Section.Get()`。

### 6.2 数据与存储

#### database

- 配置段：`database`（driver: mysql|postgres、dsn、max_open_conns/max_idle_conns/conn_max_lifetime、slow_threshold、log_level）。
- 启用：匿名导入；`driver` 为空时组件跳过初始化，不连接数据库。

```go
db := database.Get() // database.Database 接口；未配置时为 nil
if db != nil {
    err := db.Transaction(ctx, func(tx *gorm.DB) error {
        // 闭包内操作共用同一事务，出错自动回滚、正常返回自动提交
        return tx.Create(&order).Error
    })
    if err != nil {
        return err
    }
}

// Repository 实现统一用 Conn 取连接：ctx 中已携带事务
// （database.WithTx(ctx, tx)）时自动复用同一事务，否则用基础连接
gdb := database.Conn(ctx, db.DB()) // db.DB() 返回底层 *gorm.DB
```

- 注意：框架不做自动迁移，表结构变更用迁移脚本管理（见 deployment.md）。

#### cache / redis

- 配置段：`redis`（enabled/addr/username/password/db/pool_size/key_prefix）。
- 提供两层能力：原生 `redis.Client` 与框架 `cache.Cache` 抽象。

```go
// 读取时未命中返回 cache.ErrMiss，业务回源数据库
func loadUser(ctx context.Context, id string) (User, error) {
    c := cache.Get() // cache.Cache 接口；redis 未启用时自动退化为进程内内存缓存
    var u User
    if err := c.Get(ctx, "user:"+id, &u); err == nil {
        return u, nil // 命中，值按 JSON 序列化存储
    } else if !errors.Is(err, cache.ErrMiss) {
        return u, err
    }
    u = queryUserFromDB(ctx, id)
    _ = c.Set(ctx, "user:"+id, u, 5*time.Minute) // 缓存只用于提速，不作业务数据唯一来源
    return u, nil
}

// 原生 go-redis 客户端（启用 Redis 后可用）
if rc, ok := cache.Get().(*cache.RedisCache); ok {
    rdb := rc.Client() // *redis.Client
    rdb.Set(ctx, "k", "v", time.Minute)
}
```

#### storage

- 配置段：`storage`（driver: local|s3|minio + 对应参数）。
- 统一接口 `storage.Storage`：

```go
s := storage.Get() // storage.Storage；未启用时为 nil
err := s.Put(ctx, key, reader)                    // key 由服务端生成，勿用客户端原始文件名
rc, err := s.Get(ctx, key)                        // io.ReadCloser
url, err := s.GetURL(ctx, key, 10*time.Minute)    // 预签名/临时访问 URL
exists, err := s.Exists(ctx, key)
err = s.Delete(ctx, key)
```

#### upload

- 配置段：`security.upload`（max_size/allowed_extensions/allowed_mime_types/scan_enabled/clamav/image_* 等）。依赖 `storage`；`upload.Get()` 在 boot 启动后返回服务，接口由业务自行挂载：

```go
// 方式一：直接挂载上传接口（应挂在受保护分组，按需加权限中间件）
api.Register("POST", "/upload", upload.Get().Handler(upload.HTTPOptions{
    Field:           "file",    // multipart 字段名，默认 "file"
    DefaultCategory: "avatar",  // 未传 category 时的分类
}))

// 方式二：编程式保存（自行处理 multipart.FileHeader）
result, err := upload.Get().Save(ctx, tenantID, "avatar", header.Filename, file)
if err != nil {
    api.Fail(c, err) // 校验大小/类型/图片处理，写入 storage
    return
}
api.Success(c, result) // Result{Key,Name,Size,MIME,URL,Width,Height}
```

- 注意：上传落盘位置由 `storage.driver` 决定；`allowed_extensions` 与 `allowed_mime_types` 双重校验；`scan_enabled=true` 时经 ClamAV 扫描，自定义扫描器用 `upload.SetScanner(...)` 在 boot.Startup 前注入。

### 6.3 任务与消息

#### taskqueue（异步任务，Asynq/Redis）

- 配置段：`taskqueue`（enabled/addr/concurrency/queues/max_retry/task_timeout/shutdown_timeout 等，addr 为 Redis 地址）。依赖 `cache`（Redis）。
- 定义与注册任务处理器：

```go
// 注册处理器（在模块 Init 或包 init 中）；payload 为原始字节，业务自行解码
taskqueue.Register("email:send", func(ctx context.Context, payload []byte) error {
    var p EmailPayload
    if err := json.Unmarshal(payload, &p); err != nil {
        return err
    }
    return sendEmail(ctx, p)
})

// 投递任务（payload 为 []byte）
err := taskqueue.Enqueue(ctx, "email:send", payload,
    taskqueue.Queue("default"),
    taskqueue.Delay(30*time.Second),
    taskqueue.MaxRetry(3),
    taskqueue.Unique(time.Minute), // TTL 内相同任务去重
)
```

运维接口（排查积压、重放任务）：

```go
queues, err := taskqueue.Queues()                 // []string 队列名列表
stats, err := taskqueue.InspectQueues()            // []QueueStat：size/pending/active/retry 等
one, err := taskqueue.InspectQueue("default")      // *QueueStat 单队列详情
tasks, err := taskqueue.ListTasks("default", "retry", 1, 20)
// state 取值：pending / active / scheduled / retry / archived / completed
err = taskqueue.CancelTask("default", taskID)      // 取消任务
err = taskqueue.DeleteTask("default", taskID)      // 删除任务
err = taskqueue.RunTaskNow("default", taskID)      // 立即执行一次
```

- 注意：任务处理器必须幂等（Asynq 至少一次投递）；`taskqueue.enabled=false` 时 `Enqueue` 返回错误。

#### scheduler（进程内定时任务）

- 配置段：`scheduler`（enabled/job_timeout/location）。

```go
s := scheduler.Get() // scheduler.Scheduler 接口
err := s.Register("clean-token", "0 */5 * * * *", func(ctx context.Context) error {
    return cleanExpiredTokens(ctx) // spec：标准 5 段 cron，可带可选秒字段
})
jobs := s.Jobs() // 已注册任务快照：Name/Spec/Next/Running
```

- 注意：`Register` 必须在 scheduler 启动前调用（boot.Run 的 Startup 阶段即会启动）；启动后注册会返回错误。业务通常在包 `init` 或 `boot.Run` 之前完成注册。**没有分布式锁**：多实例部署时每个实例都会触发任务，需业务自行去重（如配合 `lock`），或只在一个实例上启用。

#### pool（协程池）

- 配置段：`pool`（workers/queue_size）。

```go
p := pool.Get() // *pool.Pool，全局池
err := p.Submit(ctx, func(ctx context.Context) error { // 队列满时阻塞等待空位
    return doSomething(ctx)
})
err = p.TrySubmit(ctx, task) // 队列满时直接返回错误，不阻塞
st := p.Stats()              // busy/idle/queued 等
```

#### event（进程内事件总线）

- 无配置段，匿名导入 `event` 即注册（用于关闭时排空）。

```go
bus := event.Get() // event.EventBus 接口
err := bus.Subscribe("order.paid", func(ctx context.Context, e event.Event) error {
    log.Printf("order paid: %v", e.Payload)
    return nil
}) // 处理器同步执行；进程内总线不保证可靠投递，关键流程用 outbox 或 taskqueue
err = bus.Publish(ctx, event.Event{Name: "order.paid", Payload: order})
err = bus.Unsubscribe("order.paid") // 取消订阅该事件的全部处理器
```

#### outbox（事务性发件箱）

- 配置段：`outbox`（enabled/batch_size/poll_interval 等）。依赖 `database` + `event`。
- 用途：在数据库事务里落一条事件记录，后台 Relay 轮询投递到 event 总线，保证"业务写库"与"消息发出"最终一致。

```go
db := database.Get()
err := db.Transaction(ctx, func(tx *gorm.DB) error {
    if err := tx.Create(&order).Error; err != nil {
        return err
    }
    // 与业务写入同一事务，提交后由 Relay 异步投递
    return outbox.Record(ctx, tx, event.Event{Name: "order.paid", Payload: order.ID})
})
```

#### notify（多渠道通知）

- 配置段：`notify`（enabled/channel/queue/max_retry + smtp/webhook 子段）。
- 通道由 `notify.channel` 单选：`smtp`（邮件）、`webhook`（HTTP 回调）、`sms`（短信，服务商由各云厂商包启动时自动注册，多家启用时按序故障转移）。

```go
// 发送：taskqueue 启用时自动入队异步发送（队列 notify.queue、重试 max_retry），
// 队列未启用时回退为全局发送器同步直发
err := notify.Send(ctx, notify.Message{
    To:      []string{"user@example.com"}, // 收件人：邮箱/手机号，随通道语义
    Subject: "订单已发货",                   // 邮件主题
    Body:    "您的订单已发货",                // 正文/短信内容
    HTML:    false,
    Metadata: map[string]string{"code": "1234"}, // 短信通道模板参数
})

// 自定义短信服务商（不依赖 cloud 包时）
notify.AddSMSProvider(myProvider) // notify.Provider: Send(ctx, to []string, content string, params map[string]string) error

// 直接拿发送器（跳过队列同步发送）
sender := notify.GetSender() // notify.Sender 接口：Send(ctx, Message) error
```

- 注意：`notify.enabled=false` 时 `Send` 报错；sms 通道没有任何已注册 provider 时报错；全局发送器可用 `notify.SetSender(...)` 覆盖。

### 6.4 云服务（cloud）

`cloud` 包定义统一接口，五个厂商包各自实现并在 `init` 中注册 boot 模块与配置段。**厂商包不在 bootstrap 里**，按需匿名导入：

```go
import (
    "github.com/god-jason/goframe/cloud"
    _ "github.com/god-jason/goframe/cloud/aliyun" // 注册 boot 模块 "cloud-aliyun" 与配置段 "aliyun"
)
```

统一接口（`cloud/cloud.go`）：

```go
type Sms interface {
    SendSms(ctx context.Context, req *SmsRequest) error
}

// SmsRequest 是统一短信发送请求。Sign 在不同厂商分别对应签名名称或签名 ID；
// 模板变量二选一：Params（键值对，键为 "0"、"1"… 时按数值序展开）或
// TemplateParams（显式有序列表，非空时优先，适合腾讯云/华为云等按序取参的厂商）。
type SmsRequest struct {
    PhoneNumbers   []string          // 手机号列表
    Sign           string            // 签名（名称或 ID，视厂商）
    TemplateID     string            // 模板 CODE / ID
    Params         map[string]string // 模板变量（键值对）
    TemplateParams []string          // 显式有序模板变量
}

type Voice interface {
    // PlayVoice 返回厂商侧通话任务 ID 或请求 ID
    PlayVoice(ctx context.Context, req *VoiceRequest) (callID string, err error)
}

type Storage interface {
    Put(ctx context.Context, key string, body io.Reader) error
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, key string) error
    SignURL(ctx context.Context, key string, expiry time.Duration) (string, error)
}
```

各厂商访问器与配置段（返回具体类型，均实现对应 cloud 接口；未启用/未导入时返回 nil）：

| 包 | 配置段 | boot 模块 | 访问器 |
|---|---|---|---|
| cloud/aliyun | `aliyun` | cloud-aliyun | `aliyun.GetSms() *Sms`、`GetVoice() *Voice`、`GetStorage() *Storage` |
| cloud/tencent | `tencent` | cloud-tencent | `tencent.GetSms()`、`GetVoice()`、`GetStorage()` |
| cloud/huawei | `huawei` | cloud-huawei | `huawei.GetSms()`、`GetVoice()`、`GetStorage()` |
| cloud/onenet | `onenet` | cloud-onenet | `onenet.GetSms()` |
| cloud/ctyun | `ctyun` | cloud-ctyun | `ctyun.GetSms()`、`ctyun.GetStorage()` |

```go
// 厂商无关写法：业务变量声明为接口类型
var sms cloud.Sms = aliyun.GetSms() // 未启用时返回 nil，调用前判空
if sms == nil {
    return errors.New("aliyun sms not enabled")
}
err := sms.SendSms(ctx, &cloud.SmsRequest{
    PhoneNumbers: []string{"13800000000"},
    TemplateID:   "SMS_001",
    Params:       map[string]string{"code": "1234"},
})
```

- 与 notify 的接线：厂商 boot 模块 Startup 时调用 `notify.AddSMSProvider(...)` 注册短信服务商适配器（实现 `notify.Provider`），因此 `notify.channel=sms` 且任一厂商启用后，`notify.Send` 自动走短信通道，多家启用时按注册顺序故障转移。
- 参数契约：`SmsRequest.Params`（键值对）适合按参数名取值的厂商（阿里云、OneNET、天翼云）；按序取参的厂商（腾讯云、华为云）用 `TemplateParams` 显式有序列表，或把 `Params` 的键写成 `"0"`、`"1"`… 由 `cloud.OrderedParams` 按数值序展开。跨厂商切换时模板变量顺序/名称需在各厂商控制台保持一致。
- 注意：厂商包被导入但未配置凭据时，其 boot 模块跳过初始化（`GetXxx()` 返回 nil），不影响启动。

### 6.5 安全与认证

#### auth（认证）

- 配置段：`security.auth`（enabled/issuer/audience/secret/access_token_ttl/refresh_token_ttl/leeway/password.* 哈希参数）。
- 核心 API：

```go
m := auth.Get() // auth.Manager 接口，未启用返回 nil

// 登录签发令牌：身份由业务根据用户库构造，不得来自客户端提交
tokens, identity, err := m.Login(ctx, &auth.Identity{
    UserID: "1", TenantID: "t1", Roles: []string{"admin"},
}, auth.SessionMeta{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()})
// tokens: AccessToken / RefreshToken / TokenType / ExpiresIn

// 刷新与登出
tokens, identity, err = m.Refresh(ctx, refreshToken, meta)
err = m.Logout(ctx, sessionID)
err = m.RevokeAllSessions(ctx, userID, exceptSessionID) // 踢掉其他设备
```

```go
// 路由守卫：从 Authorization: Bearer 解析身份放入 gin.Context
r.Use(api.Authenticate(m))           // 必须登录
r.Use(api.RequireAuth())             // 等价守卫（基于全局 manager）
identity, ok := auth.FromGin(c)      // Handler 内取当前身份
```

```go
// 密码哈希（注册/改密时）
hasher, err := auth.NewPasswordHasher(auth.GetConfig().Password)
hash, err := hasher.Hash(password)
ok, err := hasher.Verify(password, hash)
```

- 会话存储：默认内存（`auth.NewMemorySessionRepository()`）。持久化：在 `boot.Run` 前调用 `auth.SetSessionRepository(repo)` 注入实现 `auth.SessionRepository` 的仓库，例如 `gormstore.New(db)`（`*gorm.DB` → `*gormstore.Repository`）。
- OIDC：`oidc.New(ctx, oidc.Config{Issuer, ClientID, ClientSecret, RedirectURL, Scopes})` 返回 `*Client`；`c.AuthCodeURL(state)` 生成授权跳转链接；`token, claims, err := c.Exchange(ctx, code)` 用授权码换取令牌与声明；`c.VerifyIDToken(ctx, rawIDToken)` 单独校验 ID Token。

#### permission（授权）

```go
rbac := permission.NewRBAC(permission.DefaultPolicy())
rbac.Grant("admin", "system:read", "user:write") // 给角色授予权限（支持 user:* 通配）
rbac.AddSuperRole("superadmin")                  // 超管角色绕过资源检查
permission.Set(rbac)          // 设置全局 Authorizer（模块 Init 中调用）

authorizer := permission.Get() // permission.Authorizer 接口
err := authorizer.Check(ctx, permission.SubjectFromIdentity(identity), "system:read", resource)
// 或在 Service 层用便捷函数：permission.Authorize(ctx, authorizer, identity, action, resource)
// 路由级：api.RequirePermission(authorizer, "system:read", nil)
```

- 注意：`RequirePermission` 只是第一道防线，Service 层仍必须执行授权检查。

#### ratelimit / csrf / audit

```go
// 限流：中间件签名固定为 (limiter, keyFunc, failOpen)
limiter := ratelimit.NewKeyedLimiter(5, 10) // 每 key 5r/s、突发 10
api.RegisterPublic("POST", "/auth/login",
    ratelimit.Middleware(limiter, ratelimit.ClientIPKey, false), h.login)
// 内置 KeyFunc：ClientIPKey / UserKey / TenantKey
// 全局限流器由 security.rate_limit 配置构建：ratelimit.Get()（未启用为 nil）
// failOpen=true 时限流器异常（如 Redis 故障）放行请求

// CSRF：双提交 Cookie 模式，前端先取 token 再带 header 提交
r.Use(csrf.Middleware(csrf.Get()))
token, err := csrf.Get().Token(c) // 供 GET /public/csrf-token 下发
err = csrf.Get().Validate(c)      // 中间件内部使用，业务一般无需手动调用

// 审计：业务显式记录关键操作
audit.Get().Record(ctx, audit.Event{
    ActorID:      identity.UserID,
    Action:       "order.create",
    ResourceType: "order",
    ResourceID:   orderNo,
    Result:       "success",
})
```

- 配置段：`security.rate_limit`、`security.csrf`、`security.audit`；未启用时中间件为直通。

#### crypto / secrets / license / captcha / i18n

```go
// 字段加密（security.encryption）：AES-GCM
enc := crypto.Get() // crypto.Encryptor 接口（Encrypt/Decrypt []byte）；未启用为 nil
ct, err := enc.Encrypt([]byte(plain))
pt, err := enc.Decrypt(ct)
key, err := crypto.NewKeyFromBase64("...") // 从配置生成 32 字节密钥

// 密钥管理：从环境变量/文件/Vault 读取，支持轮换宽限期
p, err := secrets.New("env")           // secrets.Provider 接口：Get(ctx, name)
rot := secrets.NewRotating(10 * time.Minute)
rot.Set("jwt-secret", v1)
v2, err := rot.Rotate(ctx, "jwt-secret", generate) // 旧值在宽限期内继续有效
ok := rot.Verify("jwt-secret", candidate)          // 新旧值依次匹配
// Vault 后端：secrets.NewVault(secrets.VaultConfig{...})

// 许可证（license 段）：启动时校验，路由守卫按功能位放行
api.Register("GET", "/export", license.RequireFeature("export"), h.export)
r.Use(license.RequireValid())            // 全局要求许可证有效
ok := license.Get().HasFeature("export") // 编程式检查
within := license.Get().WithinLimit("max_users", current)

// 验证码
store := captcha.NewMemoryStore()
mgr, err := captcha.New(captcha.DefaultConfig(), store)
ch, err := mgr.Generate(ctx) // ch.ID / ch.Image(PNG) / ch.ExpiresIn
err = mgr.Verify(ctx, ch.ID, userInput) // 一次性消费，成功/失败均删除

// 国际化
bundle := i18n.New("zh-CN") // 默认语言
bundle.Add("en", map[string]string{"hello": "Hello"})
r.Use(bundle.Middleware()) // 从 Accept-Language/query 识别请求语言
msg := bundle.Translate(i18n.Locale(ctx), "hello")
```

- 注意：`license` 未配置时 `Get()` 返回 nil，守卫跳过放行；`captcha`/`i18n`/`secrets`/`httpclient`/`lock`/`idempotency` 无配置段，纯编程使用。

### 6.6 网络通信

#### httpclient

```go
br := httpclient.NewBreaker(httpclient.BreakerOptions{}) // 零值使用默认阈值
cli := httpclient.New(httpclient.Options{
    Timeout:    10 * time.Second,
    MaxRetries: 2, // 仅对幂等方法与可重试状态码重试，指数退避
    Breaker:    br,
    Transport:  trace.HTTPTransport(nil), // 出站请求自动注入 traceparent
})
var out struct{ Name string `json:"name"` }
err := cli.GetJSON(ctx, "https://api.example.com/u/1", &out)
err = cli.PostJSON(ctx, "https://api.example.com/orders", in, &out)

// 手动使用熔断器
if !br.Allow() {
    return errors.New("breaker open")
}
resp, err := cli.Do(ctx, req)
if err != nil {
    br.Failure()
} else {
    br.Success()
}
```

#### lock / idempotency

```go
// 分布式锁（Redis，自动续期）
l := lock.NewRedis(rdb, lock.DefaultRedisOptions()) // rdb 为 *redis.Client
release, err := l.TryLock(ctx, "order:lock:"+orderNo, 10*time.Second) // 非阻塞
if err != nil {
    return err // 未获得锁或异常
}
defer func() { _ = release(ctx) }()
// 阻塞等待版本：l.Lock(ctx, key, ttl)

// 内存锁（单实例）：lock.NewMemory()

// 幂等：按 Idempotency-Key 头去重，结果缓存
r.Use(idempotency.Middleware(idempotency.NewMemoryStore(), time.Hour))
```

#### mqtt / broker / websocket

```go
// mqtt 客户端（mqtt 段）：组件启动时自动连接，mqtt.Get() 返回客户端
c := mqtt.Get() // *mqtt.Client
err := c.Publish(ctx, "topic/dev", 1, false, payload)
err = c.PublishJSON(ctx, "topic/dev", 1, false, value)
err = c.Subscribe(ctx, "topic/dev", 1, func(topic string, payload []byte) {
    log.Printf("recv: %s", payload)
})

// broker：框架内置 MQTT broker（broker 段），与 mqtt 客户端组成完整消息链路
srv := broker.Get() // *broker.Server，未启用为 nil
broker.SetAuthenticator(myAuth) // 连接鉴权（需在启动前注入）
err = srv.Publish("topic/dev", payload, 1, false) // 服务端直投
st := srv.Stats()                                 // 在线会话与消息计数

// websocket（websocket 段）：Hub 按 subject 管理连接
websocket.SetAuthenticator(func(c *gin.Context) (string, error) {
    id, ok := auth.FromGin(c)
    if !ok {
        return "", errors.New("unauthorized")
    }
    return id.UserID, nil
})
hub := websocket.Get() // *websocket.Hub
n := hub.Send("user:1", payload)      // 向指定 subject 定向推送，返回收到的人数
n = hub.Broadcast(payload)            // 全量广播
n = hub.Publish("topic/dev", payload) // 按 topic 发布（配合 Subscribe 映射）
```

### 6.7 可观测与诊断

#### trace

- 配置段：`trace`（enabled/service_name/endpoint/protocol/insecure/sample_ratio/headers/timeout/batch_timeout）。未启用时全部 API 安全降级。

```go
r.Use(trace.Middleware()) // 解析/生成 traceparent，注入 ctx 并回写响应头
ctx, span := trace.Tracer().Start(ctx, "order.create") // 未启用时返回 noop tracer
defer span.End()
id := trace.TraceID(ctx) // 当前 trace id（可写入日志字段）
tp := trace.Get()        // *sdktrace.TracerProvider，未启用为 nil
```

#### sysinfo / pprof

```go
// sysinfo 段：在业务 Init 中调用，启用时注册只读诊断接口（受认证保护）
sysinfo.Register()

// pprof 段：enabled=true 时在 server 引擎挂 /debug/pprof 前缀路由
```

- 注意：sysinfo 与 pprof 生产环境默认关闭，开启时必须加认证守卫（见 deployment.md）。

### 6.8 支付与回调

#### payment（微信/支付宝统一支付）

- 配置段：`payment`（wechat/alipay 子段凭据、notify_url 默认值等）。依赖 `cache`（回调去重）与 `lock`。

```go
m := payment.Get() // *payment.Manager，未启用返回 nil
ch := m.Wechat()   // payment.Channel 接口；也可 m.Alipay()、m.Channel(payment.ChannelAlipay)

// 统一下单（金额单位：分）
res, err := ch.Prepay(ctx, payment.Order{
    OutTradeNo: orderNo, Amount: 1, Subject: "测试订单",
    Scene: payment.SceneNative, // native/jsapi/h5/app/page
})
// res.CodeURL 为扫码串；res.PayParams 为 JSAPI 调起参数

// 查询 / 关单 / 退款
trade, err := ch.Query(ctx, orderNo)
err = ch.Close(ctx, orderNo)
refund, err := ch.Refund(ctx, payment.RefundRequest{
    OutTradeNo: orderNo, OutRefundNo: refundNo,
    RefundAmount: 1, TotalAmount: 1, Reason: "test",
})
```

回调处理（验签 + 幂等去重 + 分发）：

```go
// 业务注册处理器（可在任意时机，早于/晚于启动均可）
payment.OnNotify(payment.ChannelWechat, func(ctx context.Context, n *payment.NotifyResult) error {
    if n.State == payment.StateSuccess {
        return markPaid(ctx, n.OutTradeNo, n.TransactionID)
    }
    return nil
})
// 回调路由由 payment 组件自动注册；n.IdempotencyKey 已用于去重，
// 处理器返回 error 时渠道将收到失败应答并重试。
```

#### webhook 接收

外部系统回调统一走 `notify` 的 webhook 通道出站；入站 webhook（接收第三方推送）建议用 `api.RegisterPublic` 登记路由 + `idempotency`/`crypto` 做去重与验签，参考 payment 的回调实现模式。

## 7. 常见任务配方

### 7.1 接入一家云短信并让 notify 走 sms

1. 匿名导入厂商包：`import _ "github.com/god-jason/goframe/cloud/aliyun"`；
2. 配置文件加 `aliyun` 段（access_key/secret/sign/template_id，密钥用 `${VAR}` 注入），并设置 `notify.enabled: true`、`notify.channel: sms`；
3. 发送：

```go
err := notify.Send(ctx, notify.Message{
    To:       []string{"13800000000"}, // 短信通道下 To 为手机号列表
    Body:     "您的验证码是 1234",        // 模板固定文案时 Body 仅作占位
    Metadata: map[string]string{"code": "1234"}, // 作为模板变量传给厂商
})
```

厂商 boot 模块启动时已自动 `notify.AddSMSProvider`，多家厂商同时启用时自动按注册顺序故障转移。

### 7.2 异步发送通知（taskqueue + notify）

```go
// 请求内调用 Send 即可：taskqueue 启用时自动入队异步发送，worker 进程真正投递
err := notify.Send(ctx, msg)
```

前提：`taskqueue.enabled=true` 且 Redis 可用；worker 与 API 进程可以是同一进程，也可以分开部署（分开时两边都要导入 bootstrap 并注册相同任务处理器）。

### 7.3 用统一接口写厂商无关的上传代码

```go
func Upload(ctx context.Context, store cloud.Storage, key string, r io.Reader) (string, error) {
    if err := store.Put(ctx, key, r); err != nil {
        return "", err
    }
    return store.SignURL(ctx, key, 24*time.Hour) // 预签名临时访问 URL
}

// 部署时按配置选择厂商，业务代码不变
var store cloud.Storage
if s := aliyun.GetStorage(); s != nil {
    store = s
} else if s := tencent.GetStorage(); s != nil {
    store = s
}
```

框架内部文件存储 `storage.Storage`（Put/Get/Delete/Exists/GetURL）与 `cloud.Storage`（Put/Get/Delete/SignURL）接口同构但方法名略有差异，跨厂商场景建议统一依赖 `cloud.Storage`。

### 7.4 排查任务积压

```go
stats, err := taskqueue.InspectQueues() // 看各队列 size/pending/active/retry 等
// 若 retry 持续增长：ListTasks 取任务详情，检查处理器错误
tasks, err := taskqueue.ListTasks("default", "retry", 1, 20)
// 单个卡住任务：先 RunTaskNow 手动重放验证，仍失败则 CancelTask/DeleteTask
err = taskqueue.RunTaskNow("default", taskID)
```

同时检查：Redis 连通性（`cache.Get().(*cache.RedisCache)` 取 `Client()` 后 `Ping(ctx)`）、worker 进程数与 `concurrency` 配置、处理器是否抛非重试错误（Asynq `SkipRetry`）。

## 8. 进阶与参考

- 全量 API 与配置说明：[README.md](../README.md)（含目录结构、全局实例表、各组件完整参数）。
- 配置模板：[configs/config.example.yaml](../configs/config.example.yaml)。
- 生产部署检查清单（TLS、密钥注入、探针、资源限制、迁移）：[docs/deployment.md](deployment.md)。
- 可运行示例：[example/](../example)（`go run ./example` 即可起完整演示服务）。
