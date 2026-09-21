# 00 · GitHub 开源项目调研

> 调研目标：为 NotifyRelay（统一通知中继）确定通道抽象、可靠性机制、鉴权与配置模型的实现路线。
> 调研日期：2026-09-21

## 调研方法

- **工具**：环境中 `gh` CLI 未安装（`gh --version` 报 command not found，winget 亦不可用），改用
  **GitHub Search REST API**（`api.github.com/search/repositories`）做发现，**`git clone --depth 1`**
  到临时目录 `%TEMP%\nr-repos\` 逐仓库读源码。
- **覆盖**：10 轮通用检索 + 10 轮定向检索，去重后 **102 个候选仓库**；从中筛出 **18 个**深读源码。
- **原则**：每条记录都以源码路径为证，不采信 README 自述。未读源码的维度明确写"无"。

---

## 0. 结论速览

| 项目 | 语言 | License | 通道数 | 通道抽象 | 队列/重试 | 上游鉴权 | 判定 |
|---|---|---|---|---|---|---|---|
| **YoRyan/mailrise** | Python | MIT | 委托 Apprise(~60) | `Router` + 配置即通道 | 无 / 无（靠 SMTP 450 反压） | SMTP AUTH | 借设计 |
| **muety/mailwhale** | Go | MIT | 1（SMTP 出站） | **无抽象** | 无 / 无 | HTTP Basic + bcrypt API key | 已弃坑，借设计 |
| **saderi/SMTP-Switch** | Python | MIT | 1（SMTP provider） | 配置模型（非接口） | **持久化队列 + 指数退避 + 熔断 + 配额预占** | IP 白名单 + SMTP AUTH(argon2id) | **重点借鉴可靠性** |
| **hyvor/relay** | PHP+Go+Svelte | **AGPL-3.0** | 2（MX 直投/Webhook） | **无抽象** | PG `SKIP LOCKED` / 固定递增 7 次 | Bearer API key(SHA-256) | **License 禁用**，借设计 |
| **toinbox/simplerelay** | Python+Postfix | MIT(无 LICENSE 文件) | 1 | **无抽象**（Postfix 路由） | 靠 Postfix / pipe 退出码 | IP 白名单为主 | **反面案例** |
| **caronc/apprise** | Python | BSD-2 | ~160 插件/100+ 服务 | **URL scheme → 插件类注册表** | 无队列 / 有 retry+throttle | URL 内嵌凭据 | **抽象层首选范本** |
| **caronc/apprise-api** | Python/Django | MIT | 继承 apprise | 不做抽象，透传 | 无 / 无 / 无 / 无审计 | **无内建鉴权** | 借接口形态 |
| **guanguans/notify** | PHP | MIT | 35 | 契约+基类，**无注册表** | 全无 | `Authenticator` 抽象 | 借分层 |
| **y1ndan/onepush** | Python | MIT | 18 | `Provider` 基类，**手工注册表** | 全无 | 无抽象 | **反面案例**（改注册表） |
| **LeslieLeung/heimdallr** | Python/FastAPI | **GPL-3.0** | 15 | Channel+Message，**if/elif 链** | 全无 | 裸 token | License 禁用，借 Group 概念 |
| **kubesphere/notification-manager** | Go | Apache-2.0 | 10 | **Receiver + Notifier 双层 + Factory 注册表** | 内存队列 / 仅审计重试 / 钉钉限流 | **无鉴权** | **架构首选范本 + 邮件实现可移植** |
| **pushbits/server** | Go | ISC | 1（Matrix） | 无抽象 | 全无 | Application Token（兼容 Gotify） | 借鉴权与生态兼容 |
| **chanify/chanify** | Go | MIT | 1（APNS） | DB 驱动注册 + 单通道接口 | 全无 | **HMAC 签名 token（含过期+内容绑定）** | 借 token 与降级 |
| **alphorn-dev/alphorn** | TypeScript/Next.js | **AGPL-3.0** | 21 | `ChannelHandler` + Zod + configFields + 注册表 | pg-boss / 5 次重试 | Bearer（timingSafeEqual） | **License 禁用**，数据模型首选范本 |
| **notifo-io/notifo** | C#/.NET | MIT | 7 渠道/多 provider | `ICommunicationChannel` + `IIntegration` | 可插拔 MQ / 4 档重试 | API Key + OAuth | 借配置模型 |
| **bougou/alertmanager-webhook-adapter** | Go | Apache-2.0 | 6 | `Sender` + `init()` 自注册 | 全无 | **无鉴权** | **中文 IM 模板可直接抄** |
| **idealista/prom2teams** | Python/Flask | Apache-2.0 | 1（Teams） | 无抽象 | 无 / 默认关闭的无限重试 | **无鉴权** | 借按字节切分 |
| **zmide/NotifyHub** | Python/Flask | MIT | 7 | 无抽象（if/elif） | 全无 | 裸 token（body 传参） | 借审计表与配置加密 |

**一句话总结**：没有任何一个项目可以直接复用为一整套 NotifyRelay，但**通道抽象可以照搬 apprise 的注册表形态、服务骨架可以照搬 notification-manager 的 Factory 注册表、可靠性机制可以照搬 SMTP-Switch 的三件套、邮件实现可以移植 notification-manager（Apache-2.0）、中文 IM 模板可以抄 alertmanager-webhook-adapter（Apache-2.0）**。

---

## 1. 分组详录

### 1.1 邮件中继组

#### YoRyan/mailrise

1. **语言/框架**：Python 3.8+，`aiosmtpd` 做 SMTP 服务端 + Apprise 做分发，asyncio 单进程。核心仅 7 个源文件（`router.py` / `simple_router.py` / `smtp.py` / `config.py` / `basic_authenticator.py`）。
2. **License**：MIT（`setup.cfg`、`LICENSE.txt`）。
3. **支持通道**：自身零通道，全部委托 Apprise（README 称 60+ 服务）。
4. **通道抽象方式**：抽象类 `Router`（`src/mailrise/router.py:99-109`），唯一方法 `email_to_apprise(...)`。**关键设计：通道不是代码里的类，而是配置里的 Apprise URL 串**——`SimpleRouter` 从 YAML 读配置（`simple_router.py:185-208`），**新增通道 = 改 1 处 YAML，零代码改动**。
5. **队列/重试/限流/审计**：队列**无**（`smtp.py:55-88` 会话内同步 await）；重试**无退避**，靠 SMTP 协议反压回 `450`（`smtp.py:84`）让上游 MTA 重投；限流**无**；审计只有 stdout 日志。
6. **鉴权方式**：仅 SMTP AUTH，`BasicAuthenticator`（`basic_authenticator.py:11-33`）支持 LOGIN/PLAIN，密码明文存 YAML，支持 `!env_var` 从环境变量注入（`config.py:31-51`）。
7. **部署方式**：pip 安装为 `mailrise` 命令或官方 Docker 镜像；**零外部依赖**（无 DB/Redis/MQ），全部配置一个 YAML。
8. **可借鉴点**：① **"收件人地址即路由指令"**——`dingtalk.failure@relay.local` 用 local part 编码通道、用 `.info/.success/.warning/.failure` 后缀编码级别（`simple_router.py:48-67`），NotifyRelay 的 SMTP 入口可直接沿用；② `!env_var` 自定义 YAML constructor（约 30 行）让密钥不落配置文件；③ `string.Template.safe_substitute` 做模板（`simple_router.py:125-142`），比引入 Jinja2 轻。
9. **不适合直接复用的原因**：定位是"通知网关"而非"可靠邮件中继"，无队列/重试/审计表；架构里完全没有 HTTP API 上游这一半；通道层与 Apprise URL 语法强绑定，自研通道（钉钉加签、飞书富文本）会被挡住。活跃度：最后提交 2025-11-08，低速维护。MIT 可放心抄代码。

#### muety/mailwhale

1. **语言/框架**：Go 1.18（`gorilla/mux` + `go-smtp` + `bolthold` 内嵌 KV），Web UI 为 Svelte。
2. **License**：MIT。
3. **支持通道**：仅 1 种——BYO-SMTP 转发（Gmail / 自建 Postfix / 任意 provider）。
4. **通道抽象方式**：**没有抽象**。`service/send.go:13-16` 的 `SendService` 只有一个 `Send(mail)`，内部直接 `sendMail()`。新增一种通道要改 4 处（`types/mail.go`、`service/send.go`、`config/config.go`、`web/routes/api/mail.go`）。
5. **队列/重试/限流/审计**：队列**无**（`web/routes/api/mail.go:91-98` 同步发完才返回）；重试**无**；限流**无**；**审计有事件表**——`ApplicationEvent{Type, UserId, ClientId, CreatedAt, Payload}`（`types/application_event.go`），fire-and-forget 写 bolthold，client 的 `CountMails` 直接数事件条数。另有 `security.block_list` 正则黑名单拦截收件人（`service/send.go:36-38`）。
6. **鉴权方式**：HTTP Basic（`web/handlers/auth.go:32-104`）。client 的 API key 用 `uuid.New()` 生成、**存 `bcrypt(key, pepper)` 哈希，明文只在创建时返回一次**（`types/client.go:114-118`）；支持 per-client 权限数组（`send_mail`/`manage_client`/`manage_user`/`manage_template`）。
7. **部署方式**：单个静态二进制（`CGO_ENABLED=0`）+ Docker + systemd；**零外部依赖**（bolthold 单文件）。
8. **可借鉴点**：① **client/sender 分离 + 默认发件人生成规则**——`Client.SenderOrDefault(baseDomain)` 生成 `user+<clientId前8位>@<yourdomain>`（`types/client.go:89-104`），匿名兜底身份生成可直接借；② **API key + 权限数组 + 发件人绑定**三件套；③ 事件表用"类型化事件 + JSON payload"而非宽表，`CountMails` 用事件数而非独立计数器（不会漂移）。
9. **不适合直接复用的原因**：**README 明写 as of Dec 2023 deprecated，最后提交 2023-12-23，事实弃坑**；无队列/重试/限流；定位是 BYO-SMTP 的 API 前端，不是完整中继。

#### saderi/SMTP-Switch ★ 可靠性机制首选范本

1. **语言/框架**：Python 3.11+，`aiosmtpd`（入站）+ `aiosmtplib`（出站）+ SQLAlchemy 2.0 async / aiosqlite + Alembic + FastAPI（dashboard）+ Pydantic v2 + structlog + prometheus-client。单 asyncio 进程，核心约 4300 行。
2. **License**：MIT。
3. **支持通道**：仅 1 种——SMTP relay 到上游 provider（示例配 smtp2go / mailgun / resend，兼容任何讲 SMTP 的服务）。
4. **通道抽象方式**：**是配置模型而非代码接口**。`ProviderConfig{name, enabled, priority, smtp, limits}`（`smtp_switch/config.py:144-150`），发送端全局只有一个 `relay()` 函数（`dispatch/sender.py:56-129`）。新增 provider **实例**改 config 一处；新增通道**类型**要改 4-5 处（config / sender / worker 调用点 / rate_limiter / metrics）——**这是"配置即抽象"在多协议场景下的反面教材**。
5. **队列/重试/限流/审计**（本组最强）：
   - **队列：持久化**。正文落 spool 文件 `<spool>/<yyyy>/<mm>/<dd>/<uuid>.eml`，元数据落 SQLite `messages` 表（status/attempts/next_attempt_at/last_error/provider_used/claimed_at，带 `(status, next_attempt_at)` 复合索引）。先落盘+写库再回 250。
   - **消费**：单 producer + N consumer（`dispatch/worker.py:1-7`，明确为在 SQLite 上规避 `SELECT ... FOR UPDATE`）；启动 `_recover_orphans()` 把崩溃遗留的 sending 重置为 queued。
   - **重试**：指数退避 + 抖动 + 双上限——`base * 2^(attempt-1)` clamp 到 max，乘 jitter 0.2；默认 base 30s / max 3600s / max_attempts 12 / max_age_hours 48，超限进 DEADLETTER（`util.py:19-34`）。
   - **错误三分类**是策略地基（`dispatch/sender.py:26-31`）：`CONNECT_ERROR` → **配额回滚**；`TRANSIENT`（4xx/超时）→ commit 配额 + 记熔断失败；`PERMANENT`（5xx）→ commit 配额 + 视为 provider 健康。
   - **限流：per-provider 多窗口 + 发送前预占**（`dispatch/rate_limiter.py`）。`per_second/minute/hour` 用 `send_log` 表滑动窗口，`per_day/month` 用 `provider_quota` 固定窗口；`try_reserve()` 在**发起 SMTP 会话之前**占坑，成功 `commit()`，连接都没建起来就失败则 `release()` 整体回滚。
   - **熔断**：`CircuitBreaker`（closed/open/half-open + 半开探针限流），状态**写穿到 `provider_state` 表，重启不忘**（`dispatch/health.py:1-9`）。
   - **审计**：`delivery_attempts` 表逐次记录（attempt_no/provider/result/smtp_code/error/耗时），`_retention_sweep()` 按 168h/720h 分别清理已发与死信。
6. **鉴权方式**：入站 SMTP 源 IP CIDR 白名单（在 EHLO 阶段就拒）+ 可选强制 SMTP AUTH（`530`）；账号存 `accounts` 表，**argon2id** 哈希。Dashboard 用 itsdangerous 签名 cookie + argon2。Provider 密码隐含在 config.yaml，支持 `SMTP_SWITCH_<SECTION>__<KEY>` 环境变量覆盖，**DB 里不存上游密码**。
7. **部署方式**：Docker 单容器（挂 config.yaml + 数据卷），**唯一外部依赖是 SQLite**，无 Redis/MQ。
8. **可借鉴点**：① **正文落 spool 文件、元数据落 DB**（`db/models.py:1-6` 注释明说不存正文）——让 DB 保持轻量；② **配额预占的 commit/release 三态语义**——"连接失败"与"被拒绝"对是否消耗配额意义完全不同，将来接有日限额的钉钉/飞书 API 直接适用；③ **单 producer + worker 池的 claim 模式**，SQLite 下最省事的无锁 claim + orphan 回收；④ **`no_capacity_backoff`：所有通道都不健康时挂起消息但不烧 attempt 次数**（`worker.py:277-283`）——多渠道场景必备，否则下游集体抽风就把队列打成死信；⑤ 熔断状态写穿 DB；⑥ **`CONNECT_ERROR/TRANSIENT/PERMANENT` 三分类应作为所有通道 handler 的强制返回契约**。
9. **不适合直接复用的原因**：只有 SMTP 一种通道，加通道要改 4-5 个文件；**明确的单实例设计**（限流用进程内 `asyncio.Lock`，注释直言 "sufficient for the single-instance deployment this project targets"），无法水平扩展；项目状态 alpha（CHANGELOG `[0.1.0] — unreleased`，无 tag），2026-08-31 才准备公开，未经生产检验；**缺 HTTP API 上游**，不满足 NotifyRelay「HTTP + SMTP 双入口」要求。

#### hyvor/relay

1. **语言/框架**：三语言三进程——backend = PHP 8.4 + Symfony 8（Doctrine/PostgreSQL、messenger、rate-limiter）；worker = Go 单二进制（自研 SMTP 客户端、MX 解析、DSN/ARF 退信解析）；frontend = SvelteKit。共 886 个文件。
2. **License**：**AGPL-3.0**（另售 enterprise license）。
3. **支持通道**：出站邮件两条路径——(1) **直投收件人 MX**（`worker/send.go:getMxHostsFromDomain` + 自研 SMTP 客户端连 25 端口）；(2) **Webhook HTTP 回调**（事件驱动）。
4. **通道抽象方式**：**没有 channel/handler 接口**。路由维度是 `Queue`（DEFAULT/DEDICATED/CUSTOM 未实现）+ IP 池（每个 IP 拉起一组 worker）。投递硬编码在 `worker/send.go:sendEmailHandler()`：dial→EHLO→STARTTLS→MAIL→RCPT→DATA→QUIT，每步结果记录进 `SmtpConversation`（`send.go:91-153`）供 UI 展示。加 IM 类通道等于重写投递层。
5. **队列/重试/限流/审计**：
   - **队列：Postgres 即队列**，claim 用 `FOR UPDATE SKIP LOCKED` + `UPDATE ... RETURNING`（`worker/send_pg.go:44-58`）。
   - **重试：固定递增间隔，非指数**——第 1~6 次分别 +15min/1h/2h/4h/8h/16h，之后 1 day，`MAX_SEND_TRIES = 7`（`send.go:566-586`）。4xx→deferred 可重试，5xx→bounced，网络错误→failed。
   - **限流**：仅 API 层固定窗口（session 60/min、api key 100/min、`/sends` 10/sec），投递层对目标域无限速。
   - **审计极强**：`sends`/`send_recipients`/`send_attempts`/`send_attempt_recipients` + 完整 SMTP 会话 JSON（每步 command、reply code、耗时），保留 30 天；`send_feedback` 处理 DSN/ARF 退信；`suppressions` 表自动抑制；419 行 Prometheus 指标 + Grafana dashboard。
   - **幂等：有**。`ApiIdempotencyRecord` + `IdempotencyService`，按 (project, endpoint, key) **回放整份 JSON 响应**而不只是标记"处理过"。
6. **鉴权方式**：API 用 `Authorization: Bearer <api_key>`，key 是 32 字节随机 hex，**存 SHA-256 哈希**，挂 scopes 数组 + allowed_ips + is_enabled（每 project 上限 10 个）。**自建实例的用户登录官方明确不做**（ROADMAP 原文：OpenID Connect will be the only authentication method available for self-hosted Relay instances）。Webhook 出站带 `X-Signature` 头。
7. **部署方式**：Docker Compose（postgres:18 + 单镜像，host network）；**PostgreSQL 是硬依赖**（同时当 DB 与队列），另含内建 DNS server 自动管理 DKIM/SPF，运维面显著偏重。
8. **可借鉴点**：① `FOR UPDATE SKIP LOCKED` + `send_after` 时间列——若选 Postgres 这是队列标准答案；② **"SMTP 会话逐步记录"作为一等公民**是排障体验的分水岭，NotifyRelay 应为 HTTP 通道做等价物（请求/响应头 + 状态码 + body 摘要）；③ `bounce+<uuid>@<instance_domain>` 作 Return-Path，用 plus 地址把退信路由回具体那条 send 记录，成本极低；④ **固定可预期的重试节奏反而比指数退避更贴合邮件现实**（MX 灰名单通常几分钟到几十分钟）；⑤ 抑制列表 + DSN/ARF 解析——硬退信必须自动进抑制表，否则持续打坏 IP 声誉；⑥ 幂等键回放整份响应。
9. **不适合直接复用的原因**：**AGPL-3.0 是硬约束**——代码并入 NotifyRelay 并对内/对外提供服务，整个项目就要以 AGPL 开源（除非购买商业授权）；三语言三进程 + Postgres + 内建 DNS，规模远超第一阶段；无通道抽象；自建认证被官方放弃；PHP 8.4 / Symfony 8 / `ext-mailparse` 构建门槛高。活跃度很好（2026-09-17），但仍在快速演进、无稳定接口。

#### toinbox/simplerelay ★ 反面案例

1. **语言/框架**：Python 3.12 FastAPI + SQLAlchemy（约 4100 行）+ React/Vite 前端 + **Postfix**（真正承担 SMTP 收信与队列）+ supervisor 编排。
2. **License**：README 声明 MIT，但**仓库根目录没有 LICENSE 文件**，法律上不完整。
3. **支持通道**：仅 1 种——SMTP 转发到上游 provider，按 **sender 地址**路由到对应 provider 账号。
4. **通道抽象方式**：**没有抽象，路由交给 Postfix**。链路：Postfix 监听 2525 → policy server（`backend/services/access_server.py`，做 IP+sender 授权）→ pipe transport 脚本（`backend/services/pipe_transport.py:530-649`）→ `send_via_provider()` 用 `smtplib` 经代理拨号上游。新增通道 = 新增 Postfix transport 或另写 pipe 脚本，**改动落在 Postfix 配置层与脚本层两处，且不在同一语言/进程内**。
5. **队列/重试/限流/审计**：
   - 队列：**完全依赖 Postfix 自身队列**，无自定义队列表。
   - 重试：靠 Postfix 的 pipe 退出码约定（`0=delivered` / `75=temp fail → 重试` / `69=permanent → 退信`）。**但 `log_cleanup.py` 每小时 `flush Postfix deferred queue`，把 Postfix 默认 5 天的重试窗口砍到约 1 小时**——明显的可靠性削弱点。
   - 限流：**有，两档日限额**——per-provider `daily_limit` + 全局 per provider_type 的 `ProviderTypeLimit`；超限 → `EX_UNAVAILABLE`（永久失败退信，**不是排队**）。
   - 审计：`MailLog` 表，但写入方式是**正则解析 Postfix syslog 文本**回填数据库（`mail_logger.py`），保留仅 24 小时。
   - 附带缺陷：健康检查构造 TLS context 时 `check_hostname=False` + `verify_mode=CERT_NONE`（`health_checker.py:14-18`），**不校验证书**。
6. **鉴权方式**：入站**以源 IP 白名单为主**（README 明确 "Without it, the relay rejects all connections"），SMTP AUTH 为辅。表 `AllowedClient` 存 `smtp_password_hash` 与 **`smtp_password_plain`（明文列）**。Dashboard 用 JWT + bcrypt。上游 provider 密码用 Fernet 加密，**但密钥由 `SHA-256(RELAY_SECRET_KEY)` 派生——JWT 签名密钥与凭据加密密钥同源，密钥泄露即双向失守**。
7. **部署方式**：单 Docker 镜像内含 Postfix + supervisor + Python + 静态前端，另加 postgres:16。**Postfix 是硬依赖**，需 syslog 才能做审计，无法在 Windows / Serverless 部署。
8. **可借鉴点**：① **"上游 sender 即路由键 + 每 sender 一份凭据"** 是自建中继最贴近真实需求的心智模型；② **per-provider 日限额 + 全局 per-provider-type 限额两级设计**，既保护单个账号也保护整类通道；③ `domain_routing`：From 头保留原始地址、MAIL FROM 换成 provider 地址（`pipe_transport.py:587-593`）；④ provider 预设表 + 连通性测试 + DNS(SPF/DKIM/DMARC) 自检的接入向导。
9. **不适合直接复用的原因**：核心交付物是 **Postfix 配置生成 + 一个 650 行 pipe 脚本**，无可用抽象；**审计建立在解析 syslog 文本之上，脆弱且只有 24h 保留，且清理任务会顺手清空延迟队列——NotifyRelay 别这么干，审计应在应用层写库、重试策略应由应用层掌控**；仓库里提交了 `pipe_transport.py-backup` 这类备份文件；TLS 校验关闭、明文密码列；无 LICENSE 文件。

---

### 1.2 多通道通知库组

#### caronc/apprise ★ 通道抽象首选范本

1. **语言/框架**：Python（`requires-python >=3.9`），核心依赖仅 `requests`，各插件通过 `requirements = {"packages_required": ...}` 自声明可选依赖。
2. **License**：BSD-2-Clause（宽松）。
3. **支持通道**：`apprise/plugins/` 下 **154 个单文件插件 + 6 个包式插件目录**（email/fcm/irc/matrix/vapid/xmpp）≈ 160 个模块，覆盖 100+ 服务。国内通道齐全：`dingtalk`、`feishu`、`lark`、`wecombot`、`wechat`、`pushplus`（**同时注册 `pushplus` 与 `wecom` 两个 scheme**）、`wxpusher`、`bark`、`serverchan`、`pushdeer`、`chanify`、`ntfy`、`gotify`；邮件走 `mailto` / `mailtos`。
4. **通道抽象方式**（重点）：
   - **基类**：`NotifyBase`（`apprise/plugins/base.py:65`）← `URLBase`（`apprise/url.py:87`）。**所有通道元数据都是类属性声明**：`protocol`/`secure_protocol`（`url.py:96,101`，值可为 tuple，**一个插件可挂多个 scheme**）、`templates`（URL 模板元组）、`template_tokens`/`template_args`/`template_kwargs`（**参数 schema**：`type`/`required`/`private`/`regex`/`default`/`values`/`alias_of`/`delim`/`min`/`max`）、行为声明 `body_maxlen`(32768)、`title_maxlen`(250)、`attachment_support`、`notify_format`、`request_rate_per_sec`(默认 5.5)、`service_retry`、`service_wait`、`overflow_mode`。
   - **发现与注册**：`PluginManager.load_modules()`（`apprise/manager.py:157-315`）——`os.listdir(plugins/)` → 正则过滤文件名 → `__import__` → 找类名匹配 `^Notify(?!Base|Format|ImageSize|Type)[A-Za-z0-9]+$` 且 `hasattr(plugin, "app_id")` 的类 → 调 `plugin.schemas()` 读 protocol → 写入 `self._schema_map[schema] = plugin`。**纯目录扫描 + 类属性，无装饰器、无元类、无 entry_points、无中心清单**。
   - **核心路由**：`Apprise.instantiate(url)` → `url_to_dict()` 用 `GET_SCHEMA_RE` 抽出 scheme → `N_MGR[schema].parse_url(url)` 解析出 kwargs → `N_MGR[schema](**results)` 实例化。**路由代码里没有任何一个具体通道的名字，只认 scheme 字符串**。
   - **新增一个通道的成本：只加 1 个文件，核心路由 0 改动（连 import 都不用加）。**
   - 另留两条扩展路径：`@notify(on="foobar")` 装饰器（`apprise/decorators/notify.py:31`）、`Apprise(plugin_paths=[...])` 外部目录扫描（`manager.py:315`）。
5. **队列/重试/限流/审计**：队列**无**（`async_notify()` 用 `run_in_executor` 并发，非队列）；**重试有**（`service_retry`/`service_wait` 类属性，上限 `APPRISE_MAX_SERVICE_RETRY=10`）；**限流有**（`request_rate_per_sec` + `URLBase.throttle()`，`url.py:328`，各插件在 `send()` 里显式调用）；**审计仅有日志层**（自定义 TRACE 级别、`LogCapture` 上下文、`cwe312_loggable()` 做密钥脱敏），**无持久化审计表**。
6. **鉴权方式**：全部编码在 URL 里，签名逻辑各插件自实现（钉钉的 HMAC-SHA256 加签在 `plugins/dingtalk.py:139-155`）；`template_tokens` 的 `private: True` 供脱敏用。**无统一的 Auth 抽象**。
7. **部署方式**：pip 库 / CLI / Docker；服务化靠 apprise-api。
8. **可借鉴点**：① **scheme → 插件类的注册表 + 基类类属性声明元数据**，这是"加通道不改核心路由"的最干净实现；② **一份 `template_tokens` 同时表达"参数 schema"与"敏感标记"**，可直接生成前端表单与 OpenAPI（apprise-api 的 `/details` 就是这么来的）；③ 一个插件可注册多个 scheme；④ `parse_url()` 由基类统一实现、插件构造函数只收 kwargs，参数解析/默认值/alias/类型转换全部上收；⑤ `request_rate_per_sec` + `throttle()` 的"通道自声明速率、基类执行"模式；⑥ 富文本降级上收到 `apprise/conversion.py` 的 `convert_between()`（markdown↔html↔text 三向转换），插件只用 `notify_format` 声明想要什么——**邮件插件用 multipart/alternative 同时给 HTML 和自动转出的纯文本兜底，是最佳实践**。
9. **不适合直接复用的原因**：Python 技术栈；体量极大（154 插件 + 核心 5000+ 行），只做「邮件 + 几个 IM」属严重过度设计；`template_tokens` 里为 URL 构建器服务的那一层（`prefix`/`delim`/`group`/`map_to`）用不上；`base.py` 里堆了 persistent_store、emoji、overflow、i18n 等与通道路由无关的职责。BSD-2 许可宽松，**可放心参考设计**。

#### caronc/apprise-api

1. **语言/框架**：Python + Django，gunicorn(gevent) + supervisord + nginx。
2. **License**：MIT。
3. **支持通道**：自身不定义通道，等于 Apprise 的通道集；用 `APPRISE_ALLOW_SERVICES`/`APPRISE_DENY_SERVICES` 做白黑名单裁剪，并用 `evict_on_disable` 回收被禁用插件的依赖内存。
4. **通道抽象方式**：**不适用**——把 URL 原样透传给 apprise。真正抽象的是两件事：**配置存储** `AppriseConfigCache`（mode = hash/simple/disabled，`utils.py:76,528`）与 **URL 白/黑名单** `AppriseURLFilter`。自定义通道靠卷挂载 + `APPRISE_PLUGIN_PATHS` 环境变量。
5. **队列/重试/限流/审计**：**四项全无**。只有 `send_webhook()`（`api/utils.py:823`）在通知完成后把结果 JSON POST 到 `APPRISE_WEBHOOK_URL`——最轻量的回执雏形。
6. **鉴权方式**：**无内建鉴权**。安全开关全是环境变量：`APPRISE_CONFIG_LOCK`、`APPRISE_ADMIN`、`APPRISE_STATELESS_URLS`、`SECRET_KEY`（**有硬编码默认值**）。生产依赖反向代理加 Basic Auth。
7. **部署方式**：Docker（`caronc/apprise`），40+ 个 `APPRISE_*` 环境变量驱动，`/config`、`/attach`、`/plugin` 三个卷。
8. **可借鉴点**：① **`/notify/{key}`（有状态，配置存服务端）+ `/notify`（无状态，URL 由白名单约束）双模式**；② **`/details` 端点自动输出所有通道的 URL 模板与参数 schema**，前端零维护生成表单——这是 `template_tokens` 设计的价值兑现点；③ **上游 payload 字段映射（JSONPath 风格，`payload_mapper.py` 支持 `items[0].objectURI`）**，让上游不必改造就能把已有 webhook JSON 映射成统一消息；④ 通知结果 webhook 回执。
9. **不适合直接复用的原因**：Django 全家桶对通知中继过重；本质是 apprise 的 HTTP 壳，脱离 apprise 无价值；**恰好缺队列、重试、鉴权、审计表——正是 NotifyRelay 要自己补的部分**，只能借架构不能借代码。

#### guanguans/notify

1. **语言/框架**：PHP >= 8.2，Guzzle 7 + symfony/options-resolver + PSR-16 缓存。
2. **License**：MIT。
3. **支持通道**：`src/` 下 35 个通道。国内：`DingTalk`、`Lark`(飞书)、`WeWork`（企微，含 Text/Markdown/Image/File/Voice/News/TemplateCard 7 种消息）、`Bark`、`Chanify`、`PushDeer`、`PushMe`、`PushPlus`、`QQ`、`ServerChan`、`WPush`、`XiZhi`、`Ntfy`、`IGot` 等。国际：`Slack`、`Discord`、`Telegram`、`GoogleChat`、`Mattermost`、`MicrosoftTeams`、`Pushover`、`RocketChat`、`Zulip`。**无邮件通道**。
4. **通道抽象方式**：**契约层三个极简接口**——`Client{send(Message)}` / `Message{toHttpMethod, toHttpUri, toHttpOptions}` / **`Authenticator{applyToOptions, applyToRequest, applyToMiddleware}`**（`src/Foundation/Contracts/`）。每个通道一个目录固定三件套：`Client.php`（只设 baseUri）+ `Authenticator.php` + `Messages/*.php` + README。参数声明用**类属性 `$defined` 白名单 + `$allowedTypes` + `$options`**，交给 Symfony OptionsResolver 校验。**注册机制：没有**——无工厂、无注册表、无 scheme 解析，调用方必须硬编码 `new Guanguans\Notify\DingTalk\Client(...)`。新增通道 ≥4 个文件。
5. **队列/重试/限流/审计**：**四项全无**（`HasHttpClient.php` 无 retry 配置；缓存只用于 access_token）。有 `Middleware/Authenticate` + `Middleware/Response` 两个 Guzzle 中间件挂载点，是插入重试/限流/审计的天然位置。
6. **鉴权方式**：`Authenticator` 接口 + **10 个内置实现**（Bearer/Basic/Certificate/Options/TokenUriTemplate/UriTemplate/WebHook/Wsse/Aggregate/Null）。钉钉把 `access_token` + HMAC-SHA256 `sign`/`timestamp` 塞进 query（`src/DingTalk/Authenticator.php:24-45`）。**鉴权与通道逻辑分离，这是它最好的设计。**
7. **部署方式**：composer 库，无独立服务、无 HTTP API。
8. **可借鉴点**：① **`Authenticator` 独立于 `Client`**（签名、token 获取/刷新/缓存全部抽出，三个注入点）——NotifyRelay 通道层应照搬这个分层；② **两个中间件挂载位**，重试/限流/审计都挂这里而非散落进各通道；③ `$defined` 白名单 + OptionsResolver：非法字段直接报错而非静默丢弃；④ `Response` 的 `onError()`/`throw()`/`successful()` 一致化错误处理；⑤ 通道目录结构作为模板。
9. **不适合直接复用的原因**：PHP 技术栈；**它恰恰缺 NotifyRelay 最想要的注册表/路由**——加通道虽不改核心，但也没有"加完即生效"的机制，调用点仍需硬编码；35 个通道却零统一消息模型，跨通道降级要自己写；无邮件通道。

#### y1ndan/onepush ★ 反面案例

1. **语言/框架**：Python，`requests[socks]` + `pycryptodome`，v1.10.0。
2. **License**：MIT。
3. **支持通道**：**18 个**。国内：`Bark`、`ServerChan`、`ServerChanTurbo`、`WechatWorkApp`、`WechatWorkBot`、`DingTalk`、`Lark`、`PushPlus`、`PushDeer`、`Qmsg`、`gocqhttp`、`WPush`、`Gotify`、`Ntfy`；国际：`Discord`、`Telegram`；通用：`SMTP`(邮件)、**`Custom`(任意 HTTP)**。
4. **通道抽象方式**：基类 `Provider`（`onepush/core.py:20`，core.py 仅约 110 行），三个钩子 `_prepare_url` / `_prepare_data` / `_send_message`，`notify()` 固定编排。参数声明是 `_params = {'required': [...], 'optional': [...]}`——**纯字符串列表，无类型、无校验、无默认值**。**注册是手工字典**：`onepush/providers/__init__.py` 逐行 import 后手写 `_all_providers = {bark.Bark.name: bark.Bark, ...}`。**新增通道必须改 `providers/__init__.py` 这个核心注册表——这正是 NotifyRelay 要避免的反面教材。**
5. **队列/重试/限流/审计**：**全无**。`Provider.request()` 只在 `except SSLError` 时用 `verify=False` 重试一次（`core.py:74-84`）——**这是安全反模式**：证书错误反而降级为不校验重试。
6. **鉴权方式**：无抽象，各 provider 自己把 token 拼进 URL query 或 header（钉钉加签在 `dingtalk.py:30-40` 手写）。
7. **部署方式**：pip 库，无服务化封装、无 Docker、无 HTTP API。
8. **可借鉴点**：① `Provider.params` 的"先取参数 schema、再填参数"两段式交互思路（应升级成带类型和校验的版本）；② **`Custom` 任意 HTTP 通道作为兜底 escape hatch**；③ 三钩子骨架是最小可用的极简基类形态；④ `SMTP` provider 的 `set_message_parser()` 类方法允许替换整个邮件构造逻辑——"默认行为 + 可替换钩子"的好例子。
9. **不适合直接复用的原因**：**手工注册表导致加通道必改核心文件，与 NotifyRelay 诉求直接冲突**；无重试/限流/队列/审计；`verify=False` 降级不安全；无附件与富文本降级；`pycryptodome` 重量依赖只为 Bark 加密。

---

### 1.3 服务型通知网关组

#### LeslieLeung/heimdallr

1. **语言/框架**：Python 3.11+ / FastAPI + uvicorn，v1.2.4。
2. **License**：**GPL-3.0**（强 copyleft，不能抄代码进闭源项目）。
3. **支持通道**：15 种（`heimdallr/channel/factory.py:30-44`）：`bark`、`wecom_webhook`、`wecom_app`、`pushover`、`pushdeer`、`chanify`、`email`、`discord_webhook`、`telegram`、`ntfy`、`lark_webhook`、`dingtalk_webhook`、`apprise`、`pushme`、`quote0`。另有兼容 PushDeer / Message-Pusher 协议的兼容路由。
4. **通道抽象方式**：抽象叫 **Channel + Message**（`heimdallr/channel/base.py`）——`Channel._build_channel()` + `send(message) -> (bool, str)`；`Message.render_message()` 负责按通道渲染。**注册与分发是硬编码 if/elif 链**（`factory.py:54-93` 的 `build_channel(name)`、`:96-133` 的 `build_message()`）。分发在 `api/base.py:16-46`，用 `asyncio.TaskGroup` 为每个 channel 起 task。**新增通道要改 4 处**（新增 channel 文件 + `config/definition.py` + factory 的两个分支 + 文档）。
5. **队列/重试/限流/审计**：**全部没有**——全仓库 grep `retry|queue|backoff|ratelimit|idempot` 零命中。两个实现缺陷值得引以为戒：① `channel.send()` 是**同步阻塞**的（`requests.post`/`smtplib`），却在 `async def` 里直接调用，`asyncio.TaskGroup` 并发形同虚设、实际串行且阻塞事件循环；② 邮件用 `smtplib.SMTP` + `starttls()`，但 `.env.example` 给的是 `EMAIL_PORT=465`（隐式 TLS 端口），该组合会失败。
6. **鉴权方式**：**裸 token 即身份**，token 直接放 URL path / Form / JSON body（`api/push.py:14-59`），无签名、无时间戳、无 IP 白名单、无限流，**token 会进访问日志**。debug 模式下 `log_env_vars()` 会把全部环境变量（含所有通道密钥）打进日志。
7. **部署方式**：`python main.py` / Docker / Vercel Serverless / 阿里云、腾讯云函数。**零外部依赖**，配置全在环境变量，状态全在内存。
8. **可借鉴点**：① **Group（分组 = 一组通道 + 一个 token）**是"一次接入、多渠道扇出"的最小可用模型，且支持组嵌套组——概念可直接借鉴（改用 YAML 替代环境变量）；② `Channel`/`Message` 两段式抽象（通道=凭据+发送，消息=按通道渲染）分工清晰；③ 反证：把账号凭据和通知目标混在同一配置命名空间（`<name>_<SUFFIX>`）导致 `config/definition.py` 70+ 常量爆炸——**NotifyRelay 应用结构化 YAML（通道为 map、凭据为子 map）**；④ 兼容层思路（直接兼容 PushDeer/Message-Pusher 请求体）成本极低，可作"零改造接入"卖点。
9. **不适合直接复用的原因**：GPL-3.0（闭源项目不能用）；Python 异步写成了假并发；**无 HTML 邮件能力**，正好缺第一阶段刚需；无重试/队列；配置全环境变量、Serverless 定位。活跃度：**最近提交 2025-07-19，约 14 个月前**。

#### kubesphere/notification-manager ★ 架构首选范本 + 邮件实现可移植

1. **语言/框架**：Go 1.20 + `controller-runtime`（K8s Operator 模式），HTTP 层 `go-restful`/`chi`。Helm Chart appVersion 2.6.1。
2. **License**：**Apache-2.0**（可商用、可抄代码，只需保留声明）。
3. **支持通道**：10 类（`pkg/constants/constants.go:15-24`）：`dingtalk`（含 ChatBot + Conversation 两种）、`email`、`feishu`、`pushover`、`sms`（阿里云/腾讯云/华为云/AWS SNS）、`slack`、`webhook`、`wechat`（企微）、`discord`、`telegram`。
4. **通道抽象方式**（本组最有价值）：
   - **双层抽象**：`internal.Receiver` / `internal.Config` 接口（`pkg/internal/interface.go`）管**配置与身份**（`GetType/GetTenantID/GetHash/GetChannels/Validate/Clone`）；**`notifier.Notifier` 接口**（`pkg/notify/notifier/interface.go:9-12`）只有两个方法 `Notify(ctx, *template.Data) error` 和 `SetSentSuccessfulHandler(...)`，管**发送**。每通道一个子包。
   - **注册是显式工厂注册表，不是 if/elif**：`type Factory func(...) (notifier.Notifier, error)`，`init()` 里 10 行 `Register(constants.Email, email.NewEmailNotifier)`（`pkg/notify/notify.go:30-47`）。
   - **分发**：`notifyStage.Exec()`（`pkg/notify/notify.go:68-126`）按 receiver 查 `factories[receiver.GetType()]` 造 Notifier，再用 `async.NewGroup(ctx)` 并发调 `nf.Notify()`。
   - **新增通道要改 6 处**（CRD 结构、Receiver 实现、Notifier 实现、`Register` 一行、controller factory、常量 + Helm 模板）——比 if/elif 好，但仍有样板成本。
5. **队列/重试/限流/审计**（四个仓库里唯一四项都有实现的）：
   - **队列：有抽象、仅内存实现**。`pkg/store/` 接口 + `provider/memory` 唯一实现——一个 `chan *template.Alert`（默认容量 10000），`Push` 带 3s 超时（满了就丢），`Pull(batchSize, batchWait)` 按批拉。**无持久化，进程挂了就丢**。
   - **批量与背压**：`dispatcher.go:34-56` 用 `semCh chan struct{}` 做并发信号量，抢不到 worker 就丢批次；每 worker 有总超时。
   - **重试：只有审计阶段有**（`historyRetryMax=3` / `historyRetryDelay=5s`，固定间隔非指数）。**业务通知本身没有重试。**
   - **限流：只有钉钉有**（因其 API 有硬性频控）——`dingtalk/throttle.go` 滑动窗口限流器，超阈值 `time.Sleep` 等待，等待超过 `maxWaitTime` 则**直接丢弃**。默认 ChatBot 20 次/分钟、最多等 10s；Conversation 25 次/秒。
   - **去重与聚合**：按 `receiver.GetHash()` 对接收者去重（`route/router.go:140-153`）；按 groupLabels 把多条 alert 聚成一条消息。
   - **幂等/审计**：`alert.ID = utils.Hash(alert)` 用内容哈希作 ID；`NotifySuccessful` + `NotificationTime` 标记；history 阶段**只导出发送成功的告警**——这就是"只审计真正送达的"的审计钩子。
   - **令牌缓存**：`notifier/token.go` 的 `AccessTokenService` 按 key 缓存 access_token 及过期时间，供钉钉/企微复用。
6. **鉴权方式**：**上游 HTTP API 无鉴权**（`pkg/webhook/v1/handler.go` 全部 handler 不做认证），靠 K8s 网络隔离 + CRD 的 RBAC 兜底。凭据通过独立 `Credential` CRD 存放并按 selector 引用。
7. **部署方式**：K8s 原生，两个二进制（notification-manager + operator）+ Helm + 5 个 CRD。外部依赖 **K8s API Server（必需，当配置中心）**；无 DB/Redis/MQ（队列在内存）。
8. **可借鉴点**（最高价值）：
   - ① **Receiver（配置/身份）与 Notifier（发送行为）分离 + 显式 Factory 注册表**——这正是 NotifyRelay 新增通道时该有的形状：**加通道 = 加目录 + 加一行注册，不动分发逻辑**。
   - ② **pipeline + stage 模式**（`dispatcher.go:112-134`：silence→route→filter→aggregation→notify→history），每级是 `stage.Stage` 接口。NotifyRelay 可裁剪成 `filter → render → dispatch → audit`。
   - ③ **邮件实现整段参考**（`pkg/notify/notifier/email/email.go:190-329`）：`net.Dialer`+`DialContext` 支持 context 超时、465 端口自动走 `tls.Dial`、STARTTLS 扩展检查、AUTH 支持 CRAM-MD5/PLAIN/LOGIN（自定义 `LoginAuth`）、可配 CA/客户端证书、`multipart/alternative` + `quoted-printable` + `mime.QEncoding` 编码中文主题、手写 `Message-Id`。**Apache-2.0，可直接移植并署名。**
   - ④ **模板外置 + text/html 同源双引擎 + i18n 字典**（`{{ translate "key" }}`），以及长消息按渲染后长度切分（`template.go:252-314`）。
   - ⑤ **AccessTokenService（带过期缓存）** 与 **钉钉滑动窗口限流器**两个小工具类，是下游通道对接的通用刚需，Go 实现可直接搬。
9. **不适合直接复用的原因**：**本质是 K8s Operator，不是独立通知服务**——配置中心是 CRD、依赖 API Server watch，脱离 K8s 完全跑不起来；上游无鉴权；多租户依赖 namespace/sidecar；代码量与第一阶段需求严重不成比例（`controller.go` 单文件近千行 + dispatcher/stage/controller 三层间接）。活跃度：2026-06-22，活跃。

#### pushbits/server

1. **语言/框架**：Go 1.24 / Gin + GORM。README 自述 **alpha**。
2. **License**：ISC（宽松）。
3. **支持通道**：**只有 1 个——Matrix**（经 mautrix 发到用户所在 room）。它**不是多通道网关**，而是"Gotify 兼容 API + Matrix 作下游"的单通道中继。README 明确 "we need to maintain neither plugins nor clients"。
4. **通道抽象方式**：**没有通道抽象**。`internal/api/notification.go:19-22` 定义了 `NotificationDispatcher` 接口但只有唯一实现。反过来，`internal/api/interfaces.go` + 接口注入（handler 只依赖 interface，测试用 `tests/mockups/` 打桩）是干净的依赖倒置示范。
5. **队列/重试/限流/审计**：**全部没有**（grep 零命中）。发送是同步阻塞的 `SendMessageEvent`，失败直接冒泡 500。唯一"补偿"是 `DeleteNotification` 把已发 Matrix 消息改写为已删除，依赖 Matrix 协议能力，不可移植。
6. **鉴权方式**（本仓库最值得看的部分）：上游用 Application Token，**同时支持 `?token=` query 与 `X-Gotify-Key` header**（`internal/authentication/authentication.go:14-16, 76-107`）——**特意兼容 Gotify 的 header 名，让现成 Gotify 客户端零改造接入**。管理面 HTTP Basic + `RequireAdmin` 中间件。密码用 **Argon2 KDF**（memory 131072 / iterations 4 / parallelism 4）+ 可选 **HIBP 泄露检测**。
7. **部署方式**：单二进制 / Docker（附带 `pbcli` 管理 CLI）；依赖 **Matrix homeserver 账号（必需）+ 数据库（sqlite3/mysql/postgres 三选一）**。无 Redis/MQ。
8. **可借鉴点**：① **`X-Gotify-Key` 兼容思路**——NotifyRelay 的 HTTP 入口若同时兼容 Gotify 的 `/message?token=` 与 header 约定，就能白嫖一大批现成客户端与脚本，成本极低；② **`extras["client::display"]["contentType"]` 由调用方声明内容类型**，比让服务端猜 markdown 更可靠；③ **priority → 视觉强度映射**（<0 灰、0-3 默认、≤10 黄、≤20 橙、>20 红），下游各自降级实现（邮件映射成主题前缀、钉钉映射成 emoji）；④ **handler 只依赖 interface + mockups 打桩**的测试结构，适合早期把通道实现与 HTTP 层解耦；⑤ Argon2 + 可选 HIBP。
9. **不适合直接复用的原因**：只有 Matrix 一个下游，与多通道诉求根本不匹配；无队列/重试/审计；README 顶部明写 **"Looking for Maintainers... I can no longer actively work on this project"**；依赖 Matrix homeserver 账号，运维前置条件重。

#### chanify/chanify

1. **语言/框架**：Go 1.20 / Gin + cobra + viper。
2. **License**：MIT（最宽松）。
3. **支持通道**：**只有 1 个——Apple APNS**（iOS/watchOS/macOS）。它是"自建推送节点"而非网关：serverless 模式下把加密消息转发到官方 api.chanify.net；serverful 模式自己存 token 直连 APNS。另有 Lua 插件式 **webhook 入口**（上游事件适配器，非下游通道）。
4. **通道抽象方式**：下游没有多通道抽象，但有两处可迁移的抽象：① **`model.DB` 接口 + DSN scheme 注册驱动**（`model/model.go:12-23,55-65`，`InitDB(dsn)` 按 `dsn://` 前的 scheme 从 `drivers map[string]OpenDB` 查实现，sqlite/mysql 各一个文件）；② **`APNSPusher` 接口**（`logic/logic.go:68-71`）只暴露 `Push(*apns2.Notification)`，`MockPusher` 让测试可替换——**一个通道客户端的最小接口形状**。上游是插件式：`pluginManager` 把 Lua 脚本加载为 webhook handler，`fsnotify` 监听文件变更**热重载**。
5. **队列/重试/限流/审计**：**全部没有**（grep 零命中）。`SendAPNS` 同步 for 循环遍历设备逐个 Push，只统计成功数。无去重、无幂等键、无审计（只有访问日志中间件）。
6. **鉴权方式**（最精细）：**自包含签名 token**，放 URL path：`base64(protobuf Token).base64(signSys).base64(signNode)`（`model/token.go:26-46`）。校验用 **HMAC-SHA256** + `hmac.Equal` 常量时间比较；token 内含**过期时间** `IsExpires()` 与 **`DataHash`（SHA-1）用于绑定请求体**（`subtle.ConstantTimeCompare`）——即"这个 token 只能发这份内容"。另有 `registerable` 开关 + 用户名白名单控制注册。
7. **部署方式**：预编译二进制 / Docker / 源码；**双模式** serverless（无状态，转发到官方中心）与 serverful（`--datapath`/`--filepath`/`--pluginpath`）。依赖 SQLite（默认）或 MySQL、APNS 证书；无 Redis/MQ。
8. **可借鉴点**：① **自包含签名 token（HMAC + 过期 + 内容哈希绑定 + 常量时间比较）** 是上游鉴权最值得抄的一处：无状态、免查库、可限时效、可限内容；② **DB 驱动按 DSN scheme 注册**的存储抽象写法，适合 NotifyRelay 的存储层（内存/SQLite/Postgres）；③ **内容超限自动降级**（长文本→落盘成 `.txt` 文件消息 `core/core.go:203-248`、图片→缩略图）——下游通道普遍有长度限制（钉钉 5000、Slack 4000），"超限就换表达形式"的策略可直接借鉴；④ Lua 插件式上游适配 + 热重载（**第一阶段应砍掉**，属过度设计）。
9. **不适合直接复用的原因**：下游只有 APNS 且与自家 App/账号体系强绑定；整个服务围绕"用户设备 + 端到端加密 + 官方中心节点"设计，架构假设与通用中继完全不同。活跃度：**最近提交 2023-02-25，约 3.5 年无更新**，仅 token 与 DB 注册两个局部设计值得看。

---

### 1.4 通知路由平台组

#### alphorn-dev/alphorn ★ 数据模型首选范本

1. **语言/框架**：TypeScript + Next.js 16（App Router / Server Actions）+ React 19 + Prisma 7 + PostgreSQL 18；队列用 **pg-boss（复用同一个 PG）**；`src/worker/` 可嵌入式也可独立进程。pnpm 管理。
2. **License**：**AGPL-3.0-or-later**（README 明示双许可，闭源集成需买商业授权）；另有 TRADEMARK.md 限制商标。
3. **支持通道**：`src/channels/index.ts` 注册 **21 个**——telegram、discord、webhook、smtp、slack、teams、google-chat、matrix、mattermost、ntfy、pushover、gotify、twilio-sms、vonage-sms、sendgrid、mailgun、rocketchat、zulip、pagerduty、opsgenie、sse。
4. **通道抽象方式**：抽象叫 **`ChannelHandler<TConfig>`**（`src/channels/types.ts`）：`{ type, displayName, description, icon, setupGuide?, configSchema: z.ZodType<TConfig>, configFields: ConfigField[], oauth?, send(config, notification, context), test?(config) }`。注册表 `src/channels/registry.ts`（`Map<type, handler>` + `registerChannel()`）。配置落库为 `Channel.config Json`（Prisma `model Channel`：`type String` + `config Json`）。**UI 元数据拆到 client-safe 的 `<type>.meta.ts`**，服务端 handler 与前端共用同一份 meta，避免两份定义漂移。**新增通道要改 4 处**（`<type>.ts`、`<type>.meta.ts`、`index.ts` 注册、`meta.ts` 聚合）。
5. **路由与分发规则**：有，是最小可用的路由模型。路由单元是接入点 `Webhook`，与 `Channel` 通过 **`WebhookChannel` 关联表**（`@@id([webhookId, channelId])`，带 `filter Json?` 和 `enabled`）。分发在 `src/app/n/[publicId]/route.ts`：抽取消息 → 对每个关联渠道跑 `evaluateFilter()` → 只对通过的建 Delivery 并入队。规则引擎在 `src/lib/filter/schema.ts`（Zod discriminated union，字段 `priority|tags|title|message|payload`；操作符按字段不同：priority 有 gt/lt/between，tags 有 has_any_of/all_of/none_of，文本类有 equals/contains/starts_with/regex）+ `evaluateFilter`（**groups 之间 OR，group 内 conditions AND**），regex 用 `safe-regex2` 做 ReDoS 校验。优先级 1–5，有 `mapPriorityScale()` 映射到各渠道自己的刻度。
6. **鉴权方式**：上游接入 = 每个 `Webhook` 一个 `apiKey`（`alp_` + 64 hex），`Authorization: Bearer`，**用 `timingSafeEqual` 比较**；Web UI = Better Auth（组织/成员/角色 + 邀请 + TOTP + 邮箱验证 + 可选 OAuth）；Server Action 侧 `requireAdminOrOwner()` 授权。
7. **部署方式**：**只需 PostgreSQL**（pg-boss 复用同一 PG），无 Redis/MQ；`MODE=all|web|worker` 控制进程角色；`docker-compose.yml` 只有 app + postgres 两个服务。多实例可选独立 SSE 服务（共享密钥）；可选 Sentry/S3/Paddle 计费。
8. **队列/重试/限流/审计**：队列有（pg-boss，队列名 `delivery`）；重试有（`MAX_RETRIES = 5`，先 `updateMany` 抢占 PENDING/FAILED 行做幂等，**`PermanentChannelError` 判永久失败不重试**，超限调 `notifyFailure`）；**`sweep.ts` 每分钟清扫孤儿任务**（PENDING 超 2 分钟重新入队、超 5 分钟标 STALE）+ `retention.ts` 按套餐删老 Message；限流有（`RateLimit` 表固定窗口 + 接入侧消息配额 + body 大小限制）；审计有（`Message` + `Delivery` 全量落库，`DeliveryStatus = PENDING|PROCESSING|DELIVERED|FAILED|STALE` + attempts/lastError/deliveredAt）。额外：`webhook-loop/hops.ts` 用签名 trace header + 最大跳数**防 webhook 回环**。
9. **可借鉴点**：① **`ChannelHandler` 三元组（type / configSchema / configFields）就是渠道配置的数据模型**——服务端用 schema 校验、前端用 configFields 生成表单，新增渠道只加文件不改框架；② **`Webhook × Channel` 关联表 + 关联表上的 `filter Json` 列**——把"路由规则"存成一条关联而不是独立规则引擎，简化程度最适合第一阶段；③ **`Delivery` 表结构**（status 枚举 + attempts + lastError + pgbossJobId + deliveredAt）与 sweep 兜底逻辑可直接照抄；④ **永久/瞬时错误分类 + 失败通知渠道（且防自环）**；⑤ 模板 DSL `{dotted.path}` 无逻辑占位替换 + 敏感 header 剥离；⑥ priority 1–5 统一刻度 + 每渠道映射函数。
10. **不适合直接复用的原因**：**AGPL-3.0 是硬伤**（网络服务化即触发源码开放义务，除非买商业许可）；Next.js 全栈 + Prisma + PG 铁三角，服务端逻辑与 React Server Actions 深度耦合，**无法只摘出后端**；21 通道 × 2 文件的样板较重；多租户/2FA/计费/SSE 远超第一阶段。**可作数据模型与交互设计的主要参照，不宜作代码基座。**

#### notifo-io/notifo

1. **语言/框架**：C# / .NET 10，单进程 ASP.NET Core（API + 管理 UI + 后台 worker + SignalR + OpenIddict 都在一个进程）；后端 943 个 .cs 文件；前端 Vite + React。`Notifo.Domain` 不依赖任何数据库，`Notifo.Data.MongoDb` / `Notifo.Data.EntityFramework` 二选一实现仓储。
2. **License**：MIT。
3. **支持通道**：7 个渠道（email / sms / messaging / mobilepush / web / webpush / webhook），每渠道下多个 provider——Email = SMTP / AmazonSES / Mailjet / Mailchimp；SMS = Twilio / MessageBird / Seven / Telekom；Messaging = Telegram / Discord / Threema；MobilePush = Firebase；Webhook = 通用 HTTP。
4. **通道抽象方式**：两层——**渠道** `ICommunicationChannel`（`Name/IsSystem/SendAsync/HandleSeenAsync/GetConfigurations`）+ **Provider** `IIntegration` 与按能力划分的 `IEmailSender`/`ISmsSender`/`IMessagingSender`/`IMobilePushSender`/`IWebhookSender`。配置描述最完整：`IntegrationDefinition(Type, Title, Logo, Properties, UserProperties, Capabilities)` + `IntegrationProperty(Name, PropertyType)`，PropertyType = Text/Number/MultilineText/Password/Boolean，属性带 DefaultValue/AllowedValues/IsRequired/Min/Max/Pattern/Format(Email|HttpUrl)，**校验逻辑内建在 `IntegrationProperty.IsValid()`**。配置存 App 记录内的字典 `App.Integrations`。**新增 provider 改 2 处**（集成类 + `Startup.ConfigureIntegrations`）。
5. **路由与分发规则**：本组最完整（面向最终用户的订阅系统）。`POST /api/apps/{appId}/events` 带 `topic`（形如 `projects/123/tasks/abc`）：`users/<id>` 直投，其他走**前缀匹配订阅**。**四级设置合并** `ChannelSettings.Merged(app, topic, subscription, user)` + `ChannelSetting.OverrideBy()`，`ChannelSetting = { Template, GroupKey, Send(Inherit/Send/NotSending/NotAllowed), Required, Condition(IfNotSeen/IfNotConfirmed/Always/Inherit), DelayInSeconds, Properties }`——**`Inherit` 枚举贯穿三处，是"未设置则继承上层"的干净写法**。provider 级还有 `Priority` + `Condition`（一段 JS 过滤脚本，Jint 执行）。
6. **鉴权方式**：上游 = 每个 App 多个 API Key 或 OpenIddict OAuth 客户端凭据；授权用 `AppResolver`（按 appId 解析 App，再用 `App.Contributors` 角色映射）；最终用户另有 `User.ApiKey` 供 Web SDK。Web UI = 自建 OpenID Connect（`/account`）。
7. **部署方式**：必需一个数据库（MongoDB 或 MySQL/PostgreSQL/SQL Server）；消息传输默认走 DB，要 RabbitMQ/Kafka/PubSub 才额外引入；**Redis 只在多实例集群时需要**（SignalR 背板 + 缓存失效）。
8. **可借鉴点**：① `IntegrationProperty` 这种"声明式字段 + 内建校验 + 供 UI 复用"的模型，**比 alphorn 的"Zod schema + 另一份 configFields"双写更省心，是渠道配置字段定义的最佳参照**；② **分层设置合并 + `Inherit` 枚举 + `OverrideBy` 逐字段覆盖**，是做"全局默认渠道 + 接入点覆盖"的教科书实现；③ `DeliveryResult`(Attempt/Sent/Handled/Failed/Skipped) 与 `ChannelSendInfo`(FirstDelivered/FirstSeen/FirstConfirmed) 的投递状态语义更细，适合做送达回执；④ 每渠道每语言的 Liquid 模板 + 预览；⑤ `LogEntry` 的"同消息去重 + Count"审计模型；⑥ 分组调度 + 延迟 + 确认取消，抑制通知轰炸。
9. **不适合直接复用的原因**：MIT 无许可障碍，但**技术栈与体量不匹配**——943 个 C# 文件、DDD/Mediator 风格基础设施、要么 Mongo 要么 SQL、可选 MQ；核心概念是面向"最终用户订阅 topic"的多租户产品（apps/topics/subscriptions/users/media/SignalR SDK），而 NotifyRelay 的收件人是"下游渠道"，语义需大改；Liquid + MJML + 多语言模板体系是巨大工程量。**只能借设计不能借代码。**

#### bougou/alertmanager-webhook-adapter ★ 中文 IM 模板可直接抄

1. **语言/框架**：Go 1.26 + go-restful + cobra CLI；用了 slack-go、discordgo 第三方 SDK。
2. **License**：Apache-2.0。
3. **支持通道**：6 个——`weixin`（企微群机器人）、`weixinapp`（企微应用，支持 to_user/to_party/to_tag）、`dingtalk`（钉钉群机器人）、`feishu`（飞书群机器人）、`slack`、`discord-webhook`。
4. **通道抽象方式**：抽象叫 **`Sender`**（`pkg/webhook-adapter/models/sender.go`），注册用**全局 map + `init()` 自注册**（`pkg/senders/all.go` 的 `ChannelsSenderCreatorMap` + `RegisterChannelsSenderCreator()`，各渠道文件在 `init()` 里注册 creator）。**渠道配置完全不落库**——creator 直接从请求 URL query 读参数（`token`/`msg_type`/`to_user`/`corp_id`）。新增通道改 3 处（渠道包 + creator 注册 + 模板登记）。
5. **队列/重试/限流/审计**：**逐项都是"无"**——无队列（请求内同步发送）、无重试、无限流、无审计（只有 `--debug` 打印 + HTTP 状态码），无任何数据库。
6. **鉴权方式**：**上游接入无鉴权**——任何能访问的人都能 POST `/webhook/send`；所谓 token 是下游渠道自己的凭据，且**以 URL query 明文传递（会进访问日志与浏览器历史）**。无 Web UI。
7. **部署方式**：无外部组件；单二进制 + Docker + systemd unit + k8s manifests + **Helm chart**。
8. **可借鉴点**：① **`Payload` 渠道无关中间表示**（`pkg/webhook-adapter/models/payload.go`：`Raw/Title/Text/Markdown/Files/Images/Links/Buttons/At` + `Status(firing/resolved)` + `Severity`）——**除文本外还带 Buttons/Links/Images/At(@某人)，且把 Status/Severity 单独作为字段而非从标题反解，NotifyRelay 的统一消息模型值得加 `severity/status` 与 `links/at` 位**；② 按 `msg_type` 分派到不同消息构造函数的 `Payload2MsgFnMap` 模式，比在 `send()` 里堆 if 清晰；③ **现成的中文模板可直接抄**（钉钉/飞书/企微/微信应用的 markdown 格式与告警分组展示都是踩过坑的，`pkg/models/templates/` 每渠道每语言一份，`go:embed` 内置 + `--tmpl-dir` 外部覆盖）；④ `EffectiveSeverity()` 把 resolved 统一降级为 ok 这种"业务语义归一"的小函数；⑤ Helm chart 可改造成 NotifyRelay 的部署模板。
9. **不适合直接复用的原因**：只做了"通知"那一半——无持久化配置、无鉴权、无队列/重试/限流/审计；入口契约被 Alertmanager 数据结构绑死；渠道参数放 URL query 会把密钥写进日志。**适合当载荷模型与中文模板的参考。**

#### idealista/prom2teams

1. **语言/框架**：Python 3.8+ / Flask 1.0.2 + flask-restplus 0.12.1 + marshmallow + Jinja2 + tenacity + uWSGI；依赖 pin 很旧（werkzeug 0.16.1、flask-restplus 已停止维护）。
2. **License**：Apache-2.0。
3. **支持通道**：**只有 Microsoft Teams**，但有"命名 connector"概念——一个 INI 里可定义任意多个具名 Teams webhook。
4. **通道抽象方式**：**没有通道抽象**——`TeamsClient` 只做"POST 渲染好的 JSON 到 URL"，`AlertSender` 负责选 URL + 组装。配置是 **INI 文件**（`[Microsoft Teams]`/`[HTTP Server]`/`[Log]`/`[Template] Path`/`[Group Alerts] Field`/`[Labels] Excluded`/`[Teams Client] RequestTimeout/RetryEnable/RetryWaitTime/MaxPayload`）。**新增通道不可行**；新增一个下游只需在 INI 加一行。
5. **队列/重试/限流/审计**：队列**无**；重试有但很弱——tenacity `@retry(wait=wait_fixed)`，**由 `RetryEnable` 开关控制、默认关闭、无限重试固定间隔**；限流**无请求级限流**，只有**单条消息字节上限 `MaxPayload`（默认 24576）——超限时按 `len(compose().encode('utf-8')) > limit` 把一组告警切分成多条消息发出，这是它最实用的"限流"**；审计**无**（无数据库、无投递记录，只有文件日志 + 探针 + 可选 Prometheus 指标）。
6. **鉴权方式**：**上游无鉴权**（任何人可 POST），靠网络隔离；无 Web UI。下游凭据即 connector URL 本身，写在配置文件里。
7. **部署方式**：无 DB/Redis/MQ；uWSGI + 配置文件 + 模板文件。
8. **可借鉴点**：① **命名 connector** 表示"下游目标"，比 query 参数干净，天然对应 NotifyRelay 的"渠道别名"概念；② **按字节数切分消息而不是报错**（`MaxPayload`）——**对钉钉/飞书/企微/Teams 的 20KB 左右限制是刚需，各渠道实现都应带上**；③ `Template: Path` 覆盖内置模板的外置化做法；④ 配置优先级链（模块默认 → instance/config.py → `APP_CONFIG_FILE` → 命令行）；⑤ `Labels/Annotations Excluded` 这种"瘦身载荷"的开关；⑥ 现成的 Teams Adaptive Card 1.4 模板。
9. **不适合直接复用的原因**：只支持 Teams，是要重写而非改造；依赖栈过旧；无鉴权、无队列、无投递状态；重试默认关闭且是"无限固定间隔"的粗糙实现。

#### zmide/NotifyHub

1. **语言/框架**：Python 3.8 / Flask 2.2.5 + Flask-SQLAlchemy + Flask-Login + Flask-WTF + gunicorn；**单文件应用**（`app.py` 798 行，全部模型/表单/路由/发送函数都在里面）。
2. **License**：MIT。
3. **支持通道**：7 个——`smtp`、`sms`（阿里云短信）、`tg`、`dingtalk`（支持加签）、`feishu`、`wechat`（企微机器人）、`webhook`。
4. **通道抽象方式**：**没有抽象**——`/api/notify` 里一串 `if channel.channel_type == 'smtp': ... elif ...`，每渠道一个独立函数 `send_email/send_sms/...`，签名统一 `(config, content)`。**配置模型是本项目最有参考价值的部分**：DB 表 `NotificationChannel` = `id / user_id / channel_id(用户自定义通道名) / channel_type / config(Text)`，**`config` 是 JSON 字符串且整列 Fernet 加密**（密钥取 `ENCRYPTION_KEY`，带未加密旧数据兼容分支）；**服务端不做字段级校验**，UI 是自由 JSON 文本域 + 每类型一段样例 JSON。新增通道改 4 处（含两个 HTML 模板）。
5. **队列/重试/限流/审计**：队列**无**、重试**无**（请求内同步发送，失败直接 500）、限流**无**；**审计有且做得不错**——`NotificationLog` 表（`user_id/channel_id/channel_type/request_data/status/error_message/timestamp/ip_address`），**在发送前先写入（默认 status=failed）再在成功后更新**，配 `/api/logs`（分页 + 关键词搜索，可搜 request_data）、详情、单条/批量删除接口和 dashboard 页。
6. **鉴权方式**：上游 = 每用户一个 `token`（`secrets.token_bytes(32)` base64），**放在 JSON body 里传**，服务端 `filter_by(token=...)` 查用户再校验通道归属；**没有 HMAC/时间戳/重放保护**。Web UI = Flask-Login + `generate_password_hash`。
7. **部署方式**：Flask + MySQL 8（docker-compose），默认 `DATABASE_URL` 是 sqlite，单机可直接跑；无 Redis/MQ。**注意：`app.py` 末尾 `app.run(..., debug=True)` 是硬编码的，直接 `python app.py` 起在生产是事故。**
8. **可借鉴点**：① **通道配置三元组（channel_id 别名 / channel_type / config JSON）+ 配置列加密**，"用户自定义通道别名"正好作为 API 路由键（`{token, id, content}` 形态极简）；② **`NotificationLog` 表结构与"先写失败日志、发送成功后回填"的写法可直接照抄作审计表**（含 ip_address 与 request_data 原文）；③ 每个渠道在 UI 里给一段样例 JSON 提示（成本极低地替代表单生成）；④ 钉钉加签实现（`HMAC-SHA256(timestamp\nsecret)` 再 `quote_plus(b64)`）；⑤ Telegram 通道带 `is_proxy/https_proxy` 开关这种接地气的细节。
9. **不适合直接复用的原因**：单文件无分层、无队列/重试/限流、无统一消息模型、加通道要动 4 处（含两个 HTML）、配置只做整列加密而无 schema 校验、`debug=True` 硬编码、无测试目录；活跃度最低（最后提交 2025-10-22，单人项目）。**定位是"交互与字段设计的参考"，不是骨架。**

---

## 2. 横向结论

### 2.1 通道抽象：四种形态，只有一种满足"加通道不改核心路由"

| 形态 | 代表 | 新增一个通道 | 问题 |
|---|---|---|---|
| **URL scheme → 插件类注册表** | apprise | **加 1 个文件，核心 0 改动** | 与 URL 语法绑定；元数据层偏重 |
| **接口 + 显式 Factory `Register()`** | notification-manager、awha | 加目录 + 加一行注册 | 仍需改动注册文件（1 行，可接受） |
| **类属性声明 + 无注册表** | guanguans/notify | 加 4 个文件 | **没有任何机制"发现"新通道**，调用点硬编码 |
| **手工字典 / if-elif 链** | onepush、heimdallr、NotifyHub | **必须改核心注册表/分支** | 直接违背 NotifyRelay 的核心诉求 |

**结论**：
1. **抽象边界要划在"通道类型"这一层，而不是"通道实例"这一层。** 定义 `Channel` 接口（`send(msg) -> Result`）+ 每类型一个实现类，实例参数走配置。SMTP-Switch 的"配置即抽象"在加新类型时会摊到 4-5 个文件，是反面教材。
2. **注册只能有一处，且必须是扫描/配置驱动而非手写清单**（onepush 的 `_all_providers` 是反面教材）。若用显式 `Register()`，务必对重复注册**报错退出**——apprise 的 schema 冲突只打日志继续，是静默失败隐患。
3. **通道元数据（参数 schema、长度上限、速率、是否支持附件、目标格式）应当作类属性挂在通道自己身上**，由核心读这些元数据驱动校验、降级、限流与文档生成。这样"加通道"只是"加一个自描述的文件"。
4. **鉴权必须从通道逻辑里剥出来**（抄 guanguans 的 `Authenticator` 三注入点），否则钉钉加签、企微 access_token 刷新会污染每个通道。
5. **富文本降级上收到核心的 `Formatter::downgrade()`**（抄 apprise `conversion.py`），通道只用 `notifyFormat` 声明想要什么。

### 2.2 可靠性：错误三分类是所有项目的公共契约

- **错误三分类是地基**：`CONNECT_ERROR / TRANSIENT / PERMANENT`（SMTP-Switch `sender.py:26-31`；hyvor 的 4xx/5xx/网络三分法）。它同时决定三件事：**要不要重试、要不要消耗配额、要不要算作通道故障**。应作为所有通道 handler 的强制返回契约。
- **队列**：18 个项目中**只有 2 个有持久化队列**（SMTP-Switch 的 SQLite+spool、hyvor 的 PG `SKIP LOCKED`），alphorn 用 pg-boss。**其余全部无队列或仅内存队列**——说明第一阶段不上 Redis/MQ 是被业界接受的。若做持久化，两个可抄的组合拳：
  - 选 SQLite：**正文落 spool 文件 + 元数据落 DB + 单 producer claim + worker 池 + orphan 回收**（SMTP-Switch）。
  - 选 Postgres：**`FOR UPDATE SKIP LOCKED` + `send_after` 时间列**（hyvor）。
- **重试节奏**：hyvor 的**固定递增（15m/1h/2h/4h/8h/16h/1d，7 次封顶）比指数退避更贴合邮件现实**——MX 灰名单通常几分钟到几十分钟，指数退避前几次太密（30s 对邮件域无意义）后几次太疏。
- **必需的两个补丁**：① **`no_capacity_backoff`**——所有通道都不健康时挂起消息但不消耗 attempt 次数（SMTP-Switch `worker.py:277-283`），否则下游集体抽风就把队列打成死信；② **`max_age_hours` 硬截止**，超龄进死信。
- **配额预占（try_reserve / commit / release）**：接有日限额的钉钉/飞书 API 时直接适用（SMTP-Switch `rate_limiter.py`）。
- **熔断状态写穿 DB**，重启不忘（SMTP-Switch `health.py:1-9`）。
- **token 缓存**：接企微/钉钉时必须缓存 access_token 及其过期时间（notification-manager `notifier/token.go`）。
- **内容超限自动降级**：长文本→落盘成文件（chanify）或按字节切分（prom2teams `MaxPayload`）。钉钉 5000 / Slack 4000 / Teams 约 24KB 都是硬限制。

### 2.3 鉴权：入口与出口要分开设计

| 方向 | 可抄的做法 | 反面案例 |
|---|---|---|
| **入口（外部→NotifyRelay）** | **chanify 的自包含签名 token**：HMAC-SHA256 + 过期时间 + **内容哈希绑定** + 常量时间比较——无状态、免查库、可限时效、可限内容。哈希存储抄 hyvor（SHA-256 + scopes + allowed_ips），比 bcrypt 省 CPU | heimdallr 裸 token 进 URL path 且进访问日志；notification-manager 与 awha **完全无鉴权**；NotifyHub token 放 body 无重放保护 |
| **出口（NotifyRelay→下游）** | 通道凭据**加密存储**（NotifyHub 整列 Fernet）；密钥**不要从 JWT secret 派生**（simplerelay 的反例：`SHA-256(RELAY_SECRET_KEY)` 同源，泄露即双向失守） | simplerelay 的 `smtp_password_plain` 明文列 |
| **生态兼容** | **pushbits 兼容 `X-Gotify-Key` header + `?token=` query**，白嫖现成客户端 | awha 把渠道 token 放 URL query，进日志与浏览器历史 |

### 2.4 License 风险清单

| License | 项目 | 可否并入闭源项目 |
|---|---|---|
| **AGPL-3.0** ⛔ | **hyvor/relay、alphorn** | **不可**——网络服务化即触发源码开放义务，除非购买商业授权 |
| **GPL-3.0** ⛔ | heimdallr | **不可**（传染性 copyleft） |
| MIT / ISC / BSD-2 ✅ | apprise、mailrise、mailwhale、SMTP-Switch、apprise-api、guanguans/notify、onepush、pushbits、chanify、notifo、NotifyHub | 可，保留声明 |
| Apache-2.0 ✅ | **notification-manager**、**alertmanager-webhook-adapter**、prom2teams | 可，保留声明 + NOTICE |
| MIT 声明但无 LICENSE 文件 ⚠️ | simplerelay | 法律上不完整，**不要抄代码** |

**关键提醒**：本组调研中**数据模型最完整的两个项目（ alphorn、hyvor/relay）恰好都是 AGPL-3.0**——可以看、可以学思路，**不能抄代码**。

### 2.5 可直接移植的代码清单（License 允许）

| 目标 | 来源 | License | 具体内容 |
|---|---|---|---|
| **邮件消息组装与编码逻辑**（非整个 email.go——连接/TLS/AUTH 用 `wneessen/go-mail`） | `kubesphere/notification-manager` | Apache-2.0 | `pkg/notify/notifier/email/email.go:190-329` 中的：`multipart/alternative` 双正文、`quoted-printable` 正文编码、`mime.QEncoding` 中文主题编码 |
| **中文 IM 消息模板** | `bougou/alertmanager-webhook-adapter` | Apache-2.0 | `pkg/models/templates/`：钉钉/飞书/企微/微信应用的 markdown 模板，每渠道每语言一份 |
| **载荷中间表示** | 同上 | Apache-2.0 | `Payload{Title/Text/Markdown/Links/Buttons/At/Status/Severity}` + `Payload2MsgFnMap` 分派模式 |
| **上游鉴权 token** | `chanify/chanify` | MIT | `model/token.go`：HMAC + 过期 + 内容哈希绑定 + 常量时间比较 |
| **审计表结构** | `zmide/NotifyHub` | MIT | `NotificationLog` 表 + "先写失败日志、成功后回填"的写法 |
| **持久队列机制** | `saderi/SMTP-Switch` | MIT | 正文落 spool + 元数据落 DB + 单 producer claim + orphan 回收 + `no_capacity_backoff` + 熔断写穿 |
| **投递状态机** | `alphorn`（AGPL，**仅参考结构不抄码**） | ⛔ | `Delivery` 表字段（status 枚举 + attempts + lastError + deliveredAt）+ sweep 兜底 |
| **配置文件约定** | `YoRyan/mailrise` | MIT | `!env_var` YAML constructor、收件人地址即路由指令 |
| **限流器 / token 缓存** | `kubesphere/notification-manager` | Apache-2.0 | `dingtalk/throttle.go` 滑动窗口限流器、`notifier/token.go` 带过期的 access_token 缓存 |
| **部署模板** | `bougou/alertmanager-webhook-adapter` | Apache-2.0 | Helm chart + systemd unit + k8s manifests |

### 2.6 明确要避开的坑（来自真实的失败实现）

1. **假异步**：heimdallr 在 `async def` 里调同步阻塞的 `requests.post`/`smtplib`，`asyncio.TaskGroup` 并发形同虚设，还阻塞事件循环。
2. **审计靠解析日志**：simplerelay 正则解析 Postfix syslog 回填 DB，延迟小时级、保留仅 24h，且清理任务顺手清空了延迟队列。**审计必须在应用层写库。**
3. **重试交给外部组件**：simplerelay 把重试甩给 Postfix，又自己每小时 flush 延迟队列，等于把 5 天重试窗口砍到 1 小时。
4. **认证降级**：onepush 遇到 SSLError 反而 `verify=False` 重试；simplerelay 健康检查 `CERT_NONE`。
5. **密钥同源派生**：simplerelay 的 JWT 签名密钥与凭据加密密钥同源。
6. **配置命名空间扁平化**：heimdallr 把账号凭据和通知目标混在同一层，导致 70+ 常量爆炸。
7. **注册表静默冲突**：apprise 的 schema 冲突只打日志继续。
8. **debug 模式泄露**：heimdallr debug 时把全部环境变量（含所有通道密钥）打进日志；NotifyHub 硬编码 `app.run(debug=True)`。
