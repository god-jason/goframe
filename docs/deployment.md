# 生产部署检查清单

本文列出使用 goframe 构建的服务上线前需要逐项确认的要点。

## 1. 配置与密钥

- [ ] 配置文件通过 `GOFRAME_CONFIG` 环境变量或启动参数指定，不依赖开发机默认路径。
- [ ] 所有密钥使用 `${VAR}` 占位符从运行环境注入（支持 `${VAR:-default}`），配置文件与镜像中不包含真实密钥。
- [ ] `security.auth.secret`（JWT）、`security.csrf.secret`、`security.encryption.key` 均为不少于 32 字符的高强度随机值，且各环境不同。
- [ ] `app.environment` 设置为 `production`，`server.mode` 设置为 `release`。
- [ ] 启用许可（`license.enabled: true`）时：许可文件、公钥已就位；`state_file` 所在目录对运行用户可写；服务器时钟同步（NTP），`clock_tolerance` 足够覆盖正常漂移。

## 2. 外部依赖

- [ ] Redis：`redis.enabled` / `taskqueue.enabled` / 支付幂等 均依赖 Redis，确认地址、ACL 账号（`username`/`password`）与库编号（`db`）正确，并配置了 maxmemory 策略。
- [ ] 数据库：`database.driver` 为 `mysql` 或 `postgres` 时，DSN 使用最小权限账号；连接池（`max_open_conns`/`max_idle_conns`）与数据库侧 `max_connections` 匹配。
- [ ] 启用 `outbox` 时数据库已建表，relay 随 boot 正常启动。

## 3. 网络、TLS 与安全

- [ ] TLS：`tls.mode` 三选一——反向代理终止（`none`，代理到源站的流量不可被客户端直连绕过）、加载证书（`file`）、自动签发（`acme`，域名已解析且 80/443 可达，`email` 已填）。
- [ ] `server.trusted_proxies` 只填写真实代理网段，防止伪造 X-Forwarded-For。
- [ ] `server.cors` 默认关闭；前后端分离部署时开启并显式列出 `allowed_origins`，`allow_credentials: true` 时禁止使用 `"*"`。
- [ ] `security.rate_limit.enabled: true`，`global_rate`/`global_burst` 按压测结果设定；`fail_open: false`。
- [ ] `security.csrf` 在浏览器可达的接口上启用，HTTPS 下 `secure: true`。
- [ ] `server.request.max_body_size` 与 `security.upload.max_size` 按业务上限收紧；上传启用 `scan_enabled` 时 clamd 可达。
- [ ] `/metrics`、`/health`、`/system/...`、`/license` 默认要求管理员身份；Prometheus 抓取与运维调用需携带认证信息，或仅在管理员网络内暴露。
- [ ] `websocket.allowed_origins` 明确列出生产域名，不使用 `*`；关闭 `allow_anonymous` 并注入鉴权。
- [ ] `pprof.enabled`、`sysinfo.enabled` 默认关闭；确需开启时仅对内网/管理端口暴露。

## 4. 进程与生命周期

- [ ] main 使用 `boot.Run(ctx, serve)`（见 [signal.go](file:///d:/WORK/goframe/boot/signal.go)）：自动监听 SIGINT/SIGTERM，启动失败自动回滚，退出时按依赖逆序 Shutdown。
- [ ] `server.shutdown_timeout` 大于最慢请求耗时；`taskqueue.shutdown_timeout` 大于最长任务执行时间，保证在途任务完成后再退出。
- [ ] 进程由 systemd / K8s 托管，停止信号宽限期（如 K8s `terminationGracePeriodSeconds`）大于上述两者之和。
- [ ] 日志目录（`logger.output`）与 ACME 缓存（`tls.acme.cache_dir`）、许可状态文件目录持久化且可写。

## 5. 多实例部署

- [ ] Web/API 实例无状态水平扩容；会话与限流如依赖 Redis 已确认多实例共享同一实例。
- [ ] `taskqueue` 可随任意实例启动（Asynq 基于 Redis 协调），扩容 worker 实例即可提高吞吐；`concurrency` 按单机 CPU/内存设定。
- [ ] `scheduler` 为进程内 cron，**无分布式锁**：多实例部署时定时任务会在每个实例各跑一次。将定时任务集中在单一实例（独立部署一份只开 scheduler 的进程），或保证任务幂等。
- [ ] `outbox` relay 多实例并行时依赖行级租约（`lease_timeout`），确认数据库支持对应锁语义。
- [ ] WebSocket 多实例需会话粘滞或经网关广播，确认业务可接受。

## 6. 通知与任务队列

- [ ] `notify.enabled: true` 且 `channel` 为 `smtp` / `webhook` / `sms` 之一。
- [ ] `notify.queue`（默认 `notify`）存在于 `taskqueue.queues`，否则任务入队会失败。
- [ ] SMTP 凭据、Webhook `secret`（HMAC 签名）已在环境中注入。
- [ ] `sms` 通道需导入至少一个云包（`cloud/aliyun|tencent|huawei|onenet|ctyun`）并启用其 `sms` 子段；未注册任何服务商时发送会明确报错。

## 7. 云服务 SDK（短信 / 语音 / 对象存储）

- [ ] 按需导入 `github.com/god-jason/goframe/cloud/aliyun`、`cloud/tencent`、`cloud/huawei`、`cloud/onenet`、`cloud/ctyun`（不在 bootstrap 中，避免拖入全部 SDK）。
- [ ] 业务代码若需跨厂商切换，依赖 `cloud.Sms` / `cloud.Voice` / `cloud.Storage` 统一接口，而非具体厂商类型。
- [ ] AK/SK（阿里、华为、天翼）或 SecretId/SecretKey（腾讯）通过环境变量注入，使用仅授予所需产品权限的子账号（RAM / CAM / IAM）。
- [ ] 阿里云：`region` 正确；`sms.sign_name`、`voice.called_show_number` 已在控制台审核通过。
- [ ] 腾讯云：`sms.sdk_app_id` / `voice.sdk_app_id` 已创建应用；COS `bucket` 使用 `名称-APPID` 格式。
- [ ] 华为云：`project_id` 已填（短信与语音必填）；`region` 仅支持 `cn-north-4` / `cn-south-1`；`sms.from` 为已申请的专用号码；语音使用 VCP 1.0，需已购买虚拟号码。
- [ ] OneNET：`sicode` 已开通短信能力获得；`sms.sign_id` 与模板已审核通过；服务器出口 IP 已加入平台白名单。
- [ ] 天翼云：AK/SK 来自云通信控制台；`sms.sign_name` 与模板 CODE 已审核；ZOS `endpoint` 为对应资源池接入域名，`use_ssl` 与桶访问一致。
- [ ] 对象存储桶权限为私有，通过 `SignURL`/预签名 URL 授权临时访问。

## 8. 支付

- [ ] 微信：商户私钥、APIv3 密钥、证书序列号来自安全渠道；`notify_url` 为 HTTPS 公网地址。
- [ ] 支付宝：公钥或证书模式配置正确，生产环境 `production: true`。
- [ ] 回调去重依赖 Redis（`payment.idempotency`），确认 `redis.enabled`。

## 9. 发布验证

- [ ] `go build ./... && go vet ./... && go test ./...` 全绿后再打包。
- [ ] 启动后确认 boot 顺序日志：config → logger → 各组件 Startup 无错误。
- [ ] 健康检查：框架内置 `GET /health/live`、`GET /health/ready`（公开）与 `GET /health`（管理员）；K8s 使用 `/health/live`、`/health/ready` 配置存活/就绪探针，`/health` 仅供管理员排障。
- [ ] 观察首个发布周期：错误日志、`taskqueue` 积压（可用 `taskqueue.InspectQueues` / `ListTasks` 运维接口）、Redis 内存、慢 SQL（`database.slow_threshold`）。
