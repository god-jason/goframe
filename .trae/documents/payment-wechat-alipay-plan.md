# payment/ 在线支付组件实现计划（微信支付 + 支付宝）

## Context

用户要求为 goframe 框架库添加在线支付功能（微信支付、支付宝）。已与用户确认的决策：

- **SDK 选型**：官方 SDK 组合 — `github.com/wechatpay-apiv3/wechatpay-go` + `github.com/smartwalle/alipay/v3`
- **场景范围**：全量 — Native 扫码、JSAPI/小程序、H5/APP、查询、关单、退款、对账账单下载、异步回调
- **订单模型**：纯网关封装，不落库（订单持久化由业务方负责，符合框架库定位）
- **回调挂载**：`payment.Register()` 按配置自动把 notify 路由经 `api.RegisterPublic` 登记；回调自动验签 + 幂等（复用 lock 包），再分发给业务 handler

遵循既有组件惯例（sysinfo/license）：`config.go`（Config + DefaultConfig + `config.Register("payment", ...)` + GetConfig + normalize）、`boot.go`（init 中 `boot.Register`，Depends config/logger/cache）、`http.go`（Register 登记路由）、`apperrors` 统一错误、`api.Success/Fail` 响应。

## 包布局（单包，两个薄适配器隔离 SDK）

```
payment/
├── payment.go   # Channel 接口、统一类型、Manager、Get()/OnNotify()
├── config.go    # Config/DefaultConfig/GetConfig/normalize/Validate
├── boot.go      # init + Startup/Shutdown + global manager + locker 选择
├── http.go      # Register() + notify gin handler + 渠道应答格式
├── notify.go    # 验签后流程：幂等 + 分发（不依赖 gin，便于测试）
├── wechat.go    # 微信适配器（wechatpay-go）
├── alipay.go    # 支付宝适配器（smartwalle/alipay/v3）
└── *_test.go    # config/payment/notify/wechat/alipay/http 测试
```

## 统一抽象（payment.go）

- `ChannelName`：`wechat` / `alipay`
- `Scene`：`native` / `jsapi` / `h5` / `app` / `page`（page 仅支付宝；不支持的场景适配器返回 apperrors.BadRequest）
- `Order`：OutTradeNo、Amount（**统一分**，适配器负责换算：微信 int64 分、支付宝元字符串两位小数）、Subject、Body、NotifyURL（空取渠道默认）、ReturnURL、Scene、OpenID（jsapi）、ClientIP、ExpireAt、Extra
- `PrepayResult`：CodeURL / PrepayID / H5URL / OrderString / RedirectURL / PayParams（JSAPI 调起五参数，签名在适配器内完成）/ Raw
- `TradeState`：pending/success/refunding/closed/failed/unknown；`Trade` 查询结果
- `RefundRequest`/`RefundResult`、`BillRequest`/`BillFile`（账单下载地址 + 内容）
- `NotifyResult`：Channel、Type(payment/refund)、**IdempotencyKey**（微信 out_trade_no+trade_state，支付宝 out_trade_no+trade_status）、金额、状态、Raw
- `Channel` 接口：`Name / Prepay / Query / Close / Refund / QueryRefund / DownloadBill / ParseNotify(*http.Request)`
- `NotifyHandler func(ctx, *NotifyResult) error`；`OnNotify(name, handlers...)` 可多注册、按序执行、任一 error 短路
- `Manager`：渠道 map + handlers + `lock.Locker`；`Get()` 未启动返回 nil；`Wechat()/Alipay()/Channel(name)` 访问，未启用渠道返回 nil

## Config（config.go）

```yaml
payment:
  enabled: false
  notify_path: /payment/notify    # 登记为 POST {path}/wechat、{path}/alipay
  timeout: 10s
  idempotency: {done_ttl: 48h, processing_ttl: 30s}
  wechat:
    enabled / app_id / mch_id / serial_no
    private_key        # PEM 内容或文件路径（沿用 license 惯例）
    apiv3_key
    notify_url
  alipay:
    enabled / app_id / private_key / public_key
    app_cert_file / alipay_cert_file / root_cert_file   # 任一非空即证书模式
    production         # false = 沙箱
    notify_url / return_url
```

`Validate()`：enabled 时至少一个渠道启用且凭据齐全，否则阻断启动。环境变量自动获得 `GOFRAME_PAYMENT_WECHAT_MCH_ID` 等（config/section.go 已支持嵌套）。

## 回调流程（notify.go + http.go）

1. gin handler 先 `GetRawData` 还原 `Request.Body`（SDK 验签需原始 body），调用 `ch.ParseNotify` 验签/解密；失败 → 直接失败应答，不进幂等层
2. 查 done 标记（key=`goframe:payment:notify:`+IdempotencyKey，TTL=DoneTTL）→ 存在则直接成功 ack
3. `locker.TryLock(proc:<key>)`：`lock.ErrLocked` → 失败应答让渠道重试；成功 → defer unlock
4. 顺序执行 handlers（panic recover 转 error）→ 全部成功写 done 标记 + 成功应答
5. 应答格式：微信 200 空 body / 500 `{"code":"FAIL"}`；支付宝 200 文本 `success` / 500 `failure`
6. locker：`cache.Get()` 为 RedisCache 时 `lock.NewRedis(rc.Client(), ...)`，否则 `lock.NewMemory()` 并日志提示单实例语义

`Register()` 在 boot.Startup 内调用（依赖 config/logger/cache；cache 未注册时 boot 仅 warn 跳过，安全），发生在业务 `api.Mount(web.API())` 之前。web 包不导入 payment，避免框架核心链入支付 SDK。

## boot 生命周期（boot.go）

- init：`boot.Register("payment", &boot.Module{Startup, Shutdown, Depends: []string{"config","logger","cache"}})`
- Startup：normalize → 未启用直接返回 → Validate → 按渠道 Enabled 构建适配器（加载私钥、初始化 SDK client、微信平台证书自动下载器）→ 解析 locker → global manager → `register(cfg)` → Info 日志
- Shutdown：置空 global；微信 downloader 后台协程用可取消 ctx 释放

## 实施步骤

1. `go get github.com/wechatpay-apiv3/wechatpay-go@latest github.com/smartwalle/alipay/v3@latest && go mod tidy`
2. **`go doc -all` 校正两个 SDK 实际 API**（services/native、jsapi、h5、app、refund、bill、notify、certificates；alipay.Client 的 Trade* 方法与 DecodeNotification、证书模式方法名）——最大不确定性来源，先校正再动笔
3. config.go + config_test.go
4. payment.go：类型 + Channel + Manager + OnNotify + fake channel 测试
5. wechat.go 适配器（Prepay/Query/Close/Refund → Bill）
6. alipay.go 适配器（同序）
7. notify.go + http.go + 测试
8. boot.go 装配
9. 同步既有文件：`bootstrap/bootstrap.go` blank import、`configs/config.example.yaml` payment 段、README 目录树与组件文档
10. 全量验证

## 测试策略（离线进 CI）

- config：normalize/Validate/env 键收集
- payment：fake Channel 测 Manager 分发、多 handler 顺序/短路、panic recover
- notify：miniredis + lock.NewRedis（照抄 lock 现有测试 helper）测 done 幂等、processing 锁冲突、handler 失败不写 done；MemoryLocker 回退同跑
- wechat：`core.ClientOptions.HttpClient = httptest.Server.Client()` 伪造 v3 JSON，测类型映射与错误包装；回调验签若 SDK 注入受限则退化为映射层测试
- alipay：测试内生成 RSA 密钥对，构造表单 + RSA2 签名喂 DecodeNotification，完全离线
- http：gin test mode + `api.Reset()` 验证 Register 的路径登记组合
- 冒烟（不进 CI，注释标注）：真实商户凭据下单、平台证书下载、真实账单、证书模式 sn 校验

## 关键文件

- 惯例模板：[config.go](file:///d:/WORK/goframe/sysinfo/config.go)、[boot.go](file:///d:/WORK/goframe/license/boot.go)、[api.go](file:///d:/WORK/goframe/api/api.go)、[redis.go](file:///d:/WORK/goframe/lock/redis.go)
- 需同步：[bootstrap.go](file:///d:/WORK/goframe/bootstrap/bootstrap.go)、[config.example.yaml](file:///d:/WORK/goframe/configs/config.example.yaml)、README.md

## 验证

`go build ./... && go vet ./... && go test ./payment/... && go test ./...` 全绿；`api.Entries()` 快照确认 notify 路径按配置登记。
