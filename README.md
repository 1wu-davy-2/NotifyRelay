# NotifyRelay · 通知中继

> **一次接入，多端送达。**
> One integration, every channel.

为内部服务提供统一的通知发送入口：上游一次接入（HTTP API / SMTP），下游多渠道送达。
第一阶段支持邮件，现已覆盖 Webhook、Slack、钉钉、飞书、企业微信。

A unified notification relay for internal services: one integration upstream
(HTTP API / SMTP), many channels downstream — email, webhook, Slack, DingTalk,
Feishu and WeCom.

**[中文](#中文) · [English](#english)**

---

## 中文

### 状态

| 里程碑 | 内容 | 状态 |
|---|---|---|
| M0 | 项目初始化：配置、日志、接口契约、构建产物 | ✅ |
| M1 | 邮件 MVP：邮件通道 + 统一发送 API + SMTP 入口 + 鉴权 | ✅ |
| M2 | 多通道抽象：schema 校验、限流、webhook / slack、`/channels` 端点 | ✅ |
| M3 | 钉钉 / 飞书 / 企业微信、markdown 方言、token 缓存、按字节限长 | ✅ |
| M4 | 持久化队列（SQLite）、重试与死信、审计、幂等、熔断、配额、指标 | ✅ |
| M5 | 管理后台 / 部署 | 计划中 |

设计文档见 [`docs/`](docs/)：调研、选型、功能边界、实施计划。

### 构建与运行

需要 **Go 1.25+**（`wneessen/go-mail` 要求 ≥ 1.25；低于此版本 Go 会自动下载新工具链）。

```powershell
# Windows
.\build.ps1 -Test              # vet + test + 构建到 bin\notifyrelay.exe
.\build.ps1 -OS linux          # 交叉编译 Linux 二进制

# Make（Git Bash / Linux / macOS）
make all
```

> ⚠️ 构建脚本**显式固定 `GOARCH=amd64`**。在 32 位工具链上开发时不要依赖宿主默认值。

跑测试：

```bash
GOMAXPROCS=2 GOMEMLIMIT=1GiB go test ./... -p 2 -parallel 2
```

**资源限制不是可选项**——不加限制的 `go test` 会把 CPU 跑满一两分钟。`build.ps1` 和 `Makefile`
里已经带上这些参数了。

```powershell
go run ./cmd/notifyrelay --config configs/notifyrelay.example.yaml
```

生成 API Key 摘要：

```powershell
go run ./cmd/notifyrelay --hash-key 'your-token'   # sha256:...
```

只存摘要，明文 token 不写入任何地方。

### 用法

#### HTTP API

```bash
curl -X POST http://127.0.0.1:8080/api/v1/notify \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
        "targets": ["oncall"],
        "title": "数据库主从延迟告警",
        "body": "**延迟**: 12s\n**实例**: db-03",
        "format": "markdown",
        "type": "warning",
        "priority": 4
      }'
```

**默认异步**：入队即返回 `202` 与投递 ID，由后台 worker 负责送达与重试。

```json
{
  "request_id": "96b3604fede17b44",
  "accepted": true,
  "deliveries": [
    {"id": "282d67a1b3c4e5f6", "target": "oncall", "channel_type": "email", "status": "queued"}
  ]
}
```

查询结果（含完整尝试历史）：

```bash
curl -H "Authorization: Bearer $TOKEN" \
     http://127.0.0.1:8080/api/v1/messages/282d67a1b3c4e5f6
```

```json
{
  "delivery": {"status": "sent", "attempts": 2, "last_error": "peer said try later"},
  "attempts": [
    {"attempt_no": 1, "class": "TRANSIENT", "elapsed_ms": 42},
    {"attempt_no": 2, "class": "SENT", "elapsed_ms": 31}
  ]
}
```

`GET /api/v1/messages?status=failed&target=oncall&limit=50` 可列死信。

**同步模式**：请求体加 `"sync": true`，等所有目标投递完再返回，按目标分别给结果。
想立刻知道成没成时用；代价是要等一个可能被限流的通道。

**幂等**：带上 `Idempotency-Key` 头，重复提交会**原样重放**首次的响应体
（`Idempotent-Replay: true`），且通道只会收到一条。

#### SMTP 入口

**收件人地址即路由指令**：`<通道别名>[.<级别>]@<hostname>`

```bash
sendmail -S relay.local:2525 ops@example.com <<'EOF'
To: oncall.warning@relay.local
Subject: 备份完成通知

备份于 03:00 完成
EOF
```

`oncall` → info；`oncall.failure` → failure；`ops.team.warning` → 别名 `ops.team`，级别 warning。
别名不存在时在 RCPT 阶段直接回 550，不会静默丢弃。

下游失败时按三分类回 SMTP 状态码：可重试 → **4xx**（让上游 MTA 自行重投），永久失败 → **5xx**。

#### 查看可用通道

```bash
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/api/v1/channels
```

返回每个已注册通道类型的参数 schema、能力（支持的格式、长度上限、限流、溢出策略），
以及哪些实例在用它——足以在不看源码的情况下写出合法配置。

声明为 `private` 的参数**不出现在这个端点的任何位置**，并且**投递失败时也不会出现在
响应或日志里**：传输错误原本会把整个 URL 带出来，而钉钉/飞书把 token 放在 query、
Slack 把 secret 放在 path——那几个通道的 URL 本身就是凭据。现在错误里只保留
`scheme://host`，另外路由会按 schema 里的 `private` 声明把配置值从所有输出中擦除。

### 通道

| 类型 | 说明 | 格式 | 速率 |
|---|---|---|---|
| `email` | 经上游 SMTP 中继发信；每个收件人独立投递 | text, html | 不限 |
| `webhook` | 通用 HTTP POST，JSON 载荷可模板化，支持 bearer/basic/header/HMAC 签名 | text, markdown, html | 可配 |
| `slack` | Incoming Webhook 或 Bot Token | text, markdown | 1/秒 |
| `dingtalk` | 钉钉群机器人，支持加签、@ 某人 | text, markdown | 20/分 |
| `feishu` | 飞书自定义机器人，支持加签；卡片 header 颜色承载级别 | text, markdown | ~100/分 |
| `wecom` | 企业微信群机器人 或 应用（access_token 自动缓存与刷新） | text, markdown | 20/分 |

**加一个通道类型 = 新增一个包 + `internal/channel/all/all.go` 里一行 import**，
核心路由、消息模型、格式降级、限流、审计全部不动。这条规则由测试强制执行：
核心包里出现 `"email"` 这类通道名字面量就会失败。

#### markdown 方言

**"markdown" 不是一个格式。** Slack 的 mrkdwn 用 `*单星号*` 表示粗体、链接是 `<url|text>`；
钉钉、飞书、企微都不渲染 `#` 标题。直接把 CommonMark 发过去不会报错——它只是把字面星号和
井号送到读者眼前。

通道声明自己的方言，**转换由核心完成**，所以没有任何通道里写着转换代码：

| 输入 | 钉钉 | 飞书 / 企微 | Slack |
|---|---|---|---|
| `# 标题` | 保留 | `*标题*` | `*标题*` |
| `**粗体**` | 保留 | 保留 | `*粗体*` |
| `*斜体*` | 保留 | 保留 | `_斜体_` |
| `[文字](url)` | 保留 | 保留 | `<url\|文字>` |

#### 长度限制

通道声明的上限**是平台实际收到的载荷上限**，不只是正文——路由会先扣掉标题、链接和通道自己的
包装开销再切分。钉钉 20000 **字节**、企微 4096 **字节**是字节限制，不是字符数；只算字符会把
1 万个汉字（30000 字节）当成远低于上限。

### 投递保证

| 通道结果 | 处理 |
|---|---|
| `SENT` | 完成 |
| `PERMANENT` | 立即进死信，不重试 |
| `TRANSIENT` | 按 `retry.backoff` 重试，耗尽后进死信 |
| `CONNECT_ERROR` | 试过了但没碰到对端 → **归还队列、不消耗重试预算** |
| `NOT_ATTEMPTED` | 根本没试（熔断／配额／限流）→ **归还队列、不消耗重试预算** |

最后一条是多通道场景的关键：下游集体故障时，若把连接失败也算作尝试，一次故障就会把所有消息的
重试预算烧光、全部打成死信。`retry.max_age` 负责兜底。

#### `skip_reason`：区分"连不上"和"没尝试"

`CONNECT_ERROR` 的意思是"试过了，没能碰到对端"。而被熔断、被配额、被限流挡下的投递
**根本没碰通道**——把它们报成连接失败，是让一个从没发生过的连接去解释一条消息的命运。
所以这三类统一报 `NOT_ATTEMPTED`，并额外带一个 `skip_reason` 说明是哪一种：

| 值 | 含义 |
|---|---|
| `breaker_open` | 通道已知不健康，暂时停用 |
| `quota_exhausted` | 该通道的窗口额度用尽 |
| `rate_limited` | 等不到限流令牌 |

**不变式：`skip_reason` 出现 ⟺ class 是 `NOT_ATTEMPTED`。** 一个正文被切成两段、
第一段发出去、第二段被配额挡下的投递不报这个字段——通道确实收到了消息，
折叠结果时"未尝试"让位于真实结果。

它会出现在同步响应、审计日志，以及 `GET /api/v1/messages/{id}` 的尝试历史里。
SMTP 入口对它的回复码是 **451**（让发信方重投），不是 550——一个只是暂时被挡下的
通知不该在这一步被退回去。

重试节奏用**固定递增间隔**而非指数退避：邮件与 IM 的失败主要是灰名单和临时 4xx，指数退避的前几步
太密（30s/60s 对邮件域毫无意义）、后几步又太疏。

机制上还有两条保障：

- **claim_timeout**：投递被领走后超过这个时间没回写，即视为放弃并归还队列——判断依据是
  "领取是否超时"，与是否重启无关。配合周期性扫描，进程被杀、worker 卡死、机器掉电都能兜住。
- **熔断器**：通道连续失败达阈值即停用，投递快速失败而不是每条都等满连接超时。状态**落库**，
  重启不会忘记正在进行中的故障。半开期只放行配置数量的探针——刚恢复的通道应该被测试，
  而不是被灌入积压。

`PERMANENT` **不计入通道故障**：对端答复了，而且答复正确，问题出在这条消息上。
把它计入的话，一个坏收件人地址就能熔断整个通道。

### 配额

每个通道实例可声明发送额度（`quota.per_second/minute/hour/day/month`）。
**调用通道之前先预留**，再按结果结算：

| 结果 | 处理 |
|---|---|
| 送达 / 被拒 / 被要求稍后再试 | **提交**——对端确实被调用了 |
| 连不上 | **回滚**——没碰到对端，不该算这条消息的账 |

通道不可达一小时，若照常计数，就会花掉一整天的额度换来一堆没人收到的消息。
配额耗尽的投递**归还队列等待**而不是失败，也不消耗重试预算。

per_second/minute/hour 用滑动窗口；per_day/month 用固定窗口并落库（平台按自然日重置，
且重启不能忘记已用额度）。

### POST /api/v1/notify 字段

| 字段 | 必填 | 说明 |
|---|---|---|
| `targets` | ✅ | 通道别名数组，或 `类型:别名`。可混用 |
| `title` | ✅ | 标题 |
| `body` | ✅ | 正文 |
| `format` | | `text` \| `markdown` \| `html`，默认 `text`。**由调用方声明，服务端不猜** |
| `type` | | `info` \| `success` \| `warning` \| `failure`，默认 `info` |
| `priority` | | 1–5 |
| `tags` / `links` / `at` / `meta` | | 标签、结构化链接、@ 提及、通道私有扩展 |
| `sync` | | `true` 则等待投递完成 |

### 运维

- `GET /metrics` — Prometheus 指标（队列深度、各分类尝试数、投递耗时）
- `GET /healthz` — 存活探针，**不查数据库**
- `GET /readyz` — 就绪探针，**查数据库**

两者分开是有意的：存活探针因依赖故障而失败会导致进程被反复重启——既修不好问题，还会丢掉内存状态；
就绪探针查依赖，是为了不在存不下的时候还接受通知。

### 部署

```bash
# Docker Compose：一条命令
cp .env.example .env          # 填密钥；.env 在 .gitignore 里
cp configs/notifyrelay.example.yaml configs/notifyrelay.yaml
docker compose up -d

# Kubernetes
helm install notifyrelay deploy/helm/notifyrelay   --set image.repository=<你的仓库>/notifyrelay --set image.tag=<版本>

# 裸机 / 虚拟机
install -m 0755 notifyrelay /usr/local/bin/
install -m 0644 deploy/systemd/notifyrelay.service /etc/systemd/system/
systemctl enable --now notifyrelay
```

**不重启改配置**：`systemctl reload notifyrelay`（或 `kill -HUP`）。日志级别、API 密钥、
超时、重试节奏、熔断阈值、渠道配置都会生效；监听地址、存储路径、worker 数量需要重启，
**服务会在日志里列出来是哪些**。

运维手册（备份、升级、指标、排障、还没验证的部分）见 [`docs/06-operations.md`](docs/06-operations.md)。

### 配置

见 [`configs/notifyrelay.example.yaml`](configs/notifyrelay.example.yaml)，每一项都有注释。三条硬规则：

1. **密钥不进配置文件**。写 `!env VAR_NAME`，运行时从环境变量读取；**变量未设置会直接启动失败**，
   而不是静默变成一个空凭据。
2. **超时必须显式设置**。`timeouts.handler`（整个请求，含扇出）必须大于 `timeouts.deliver`（单个目标）。
3. **`queue.claim_timeout` 必须大于 `queue.deliver_timeout`**，否则慢投递会被当成被放弃的投递，发两次。

### 架构

```
上游  HTTP API ─┐
      SMTP 入口 ─┴─→ 路由 ─→ 熔断 ─→ 配额预留 ─→ 限流 ─→ 通道 ─→ 结算
                      │                                        │
                      └─→ 格式方言 / 长度切分 / 审计 ←──────────┘
                                   │
                              持久化队列（SQLite + 正文落盘）
```

- `internal/store/` — 存储接口（Queue / Audit / Idempotency / Breakers / Quotas）
- `internal/store/sqlite/` — 首个实现，WAL + `_txlock=immediate`
- `internal/store/spool/` — 正文落盘，DB 只存状态与索引
- `internal/queue/` — 重试策略与 worker 池
- `internal/breaker/` `internal/quota/` — 熔断与配额
- `internal/channel/` — 通道接口、注册表与各通道实现

**换 MySQL / PostgreSQL 的接入点是 `internal/store`**：接口是关系型形状但不是 SQL 形状
（claim 是"读取并标记"一步完成——SQLite 用 immediate 事务，MySQL 用 `FOR UPDATE SKIP LOCKED`），
`openStore()` 里的 switch 是唯一需要改的地方。

### 致谢

设计借鉴了若干开源项目，邮件的消息组装与编码策略来自
[`kubesphere/notification-manager`](https://github.com/kubesphere/notification-manager)（Apache-2.0）。

完整的第三方署名、来源文件路径与 License 见 [`NOTICE`](NOTICE)。

### License

**GNU Affero General Public License v3.0** — 见 [`LICENSE`](LICENSE)。

AGPL 第 13 条：**通过网络与本服务交互的用户，有权获得其对应源码**。

---

## English

### What it is

An internal notification relay. Producers integrate once — over an HTTP API or
by sending mail — and the service fans the message out to whichever channels
each destination needs. Adding a channel does not change any producer.

### Status

| Milestone | Scope | |
|---|---|---|
| M0 | Project setup: config, logging, interface contracts, build artefacts | ✅ |
| M1 | Email MVP: email channel, send API, SMTP inbound, authentication | ✅ |
| M2 | Multi-channel abstraction: schema validation, rate limiting, webhook, Slack, `/channels` | ✅ |
| M3 | DingTalk / Feishu / WeCom, markdown dialects, token cache, byte-accurate limits | ✅ |
| M4 | Durable queue (SQLite), retry and dead letters, audit, idempotency, circuit breaker, quota, metrics | ✅ |
| M5 | Admin UI and deployment | planned |

Design documents live in [`docs/`](docs/) (Chinese).

### Build and run

Requires **Go 1.25+**.

```bash
./build.ps1 -Test          # Windows: vet + test + build
make all                   # Git Bash / Linux / macOS
```

The build pins `GOARCH=amd64` explicitly.

```bash
GOMAXPROCS=2 GOMEMLIMIT=1GiB go test ./... -p 2 -parallel 2
```

The resource limits are not optional — an unbounded `go test` will sit at 100%
CPU for a minute or two. `build.ps1` and the `Makefile` already pass them.

```bash
go run ./cmd/notifyrelay --config configs/notifyrelay.example.yaml
go run ./cmd/notifyrelay --hash-key 'your-token'    # sha256:...
```

Only the digest is stored; the token itself is never written down.

### Usage

**Send a notification** (asynchronous by default — the request is queued and
returns `202` with a delivery id):

```bash
curl -X POST http://127.0.0.1:8080/api/v1/notify \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
        "targets": ["oncall"],
        "title": "Replication lag on db-03",
        "body": "**lag**: 12s",
        "format": "markdown",
        "type": "warning"
      }'
```

Add `"sync": true` to wait for the outcome instead, in which case the response
carries a per-target result — never a single overall status, because collapsing
a fan-out into one word is how a partial failure gets hidden.

**Check a delivery** — including its full attempt history:

```bash
curl -H "Authorization: Bearer $TOKEN" \
     http://127.0.0.1:8080/api/v1/messages/282d67a1b3c4e5f6
```

**Idempotency**: send an `Idempotency-Key` header and a repeat submission
replays the original response byte for byte, while the channel sees one
message.

**SMTP inbound** — the recipient address carries the routing instruction:

```
oncall@relay.local             -> channel "oncall", type info
oncall.failure@relay.local     -> channel "oncall", type failure
ops.team.warning@relay.local   -> channel "ops.team", type warning
```

An unknown alias is refused at RCPT time with a 550. Downstream outcomes map
onto SMTP replies: retryable becomes 4xx so the sending MTA retries on its own
schedule, permanent becomes 5xx.

**List channels** — `GET /api/v1/channels` returns every registered channel
type with its parameter schema and capabilities, which is enough to write a
valid configuration without reading the source.

A parameter declared `private` appears nowhere in that document, and — since a
transport error renders the whole URL, and for DingTalk, Feishu, Slack and WeCom
the URL *is* the credential — it does not appear in a delivery failure either.
Errors keep `scheme://host` and drop the rest, and the router scrubs every value
the schema declares private from everything it emits.

### Channels

| Type | Notes | Formats | Rate |
|---|---|---|---|
| `email` | Via an upstream SMTP relay; each recipient delivered independently | text, html | unlimited |
| `webhook` | Generic JSON POST, templated payload, bearer/basic/header/HMAC auth | text, markdown, html | configurable |
| `slack` | Incoming webhook or bot token | text, markdown | 1/s |
| `dingtalk` | DingTalk group robot, with optional signing and @-mentions | text, markdown | 20/min |
| `feishu` | Feishu custom bot with optional signing; card header colour carries severity | text, markdown | ~100/min |
| `wecom` | WeCom group robot, or an application with cached, refreshed tokens | text, markdown | 20/min |

**Adding a channel type is one new package plus one import line.** Nothing in
the router, the message model, format handling, rate limiting or auditing
changes. A test enforces it: a channel name appearing as a literal in a core
package fails the build.

#### Markdown dialects

"Markdown" is not one format. Slack's mrkdwn uses `*single stars*` for bold and
`<angle|brackets>` for links; DingTalk, Feishu and WeCom do not render `#`
headings at all. Sending CommonMark to them does not fail — it just puts
literal asterisks and hashes in front of a reader.

Channels declare a dialect and **the core does the conversion**, so no channel
implementation contains a converter.

#### Length limits

A channel's declared limit is a promise about **the payload the platform
receives**, not about the body alone — the router subtracts the title, the
links and the channel's own markup before splitting. DingTalk's 20000 and
WeCom's 4096 are **byte** limits, not character counts.

### Delivery guarantees

| Channel result | What happens |
|---|---|
| `SENT` | done |
| `PERMANENT` | dead-lettered immediately, never retried |
| `TRANSIENT` | retried on the configured backoff, then dead-lettered |
| `CONNECT_ERROR` | tried and could not reach the peer → **returned to the queue without spending an attempt** |
| `NOT_ATTEMPTED` | never tried (breaker, quota, rate limit) → **returned to the queue without spending an attempt** |

That last row matters most when several channels fail at once: charging an
attempt for a call that never reached the peer would let one outage burn
through every message's retry budget and dead-letter the lot.

#### `skip_reason`: "could not reach it" versus "never tried"

`CONNECT_ERROR` means "we tried and could not get there". A delivery held back by
an open breaker, a spent allowance or a rate limit never touched the channel at
all, and reporting it as a connection failure lets a connection that never
happened explain the message's fate. Those report `NOT_ATTEMPTED`, with a
`skip_reason` naming which limit it was:

| Value | Meaning |
|---|---|
| `breaker_open` | the channel is known to be failing |
| `quota_exhausted` | its allowance for the window is spent |
| `rate_limited` | no rate-limit slot could be waited out |

**The invariant: `skip_reason` is present if and only if the class is
`NOT_ATTEMPTED`.** A message split in two whose first part went out and whose
second was blocked reports no reason — the channel did receive it, and folding
the results lets "not attempted" yield to the real outcome.

It appears in the synchronous response, in the audit log, and in the attempt
history at `GET /api/v1/messages/{id}`. Over SMTP it answers **451** so the
sending MTA retries, rather than 550 — a notification that is merely held back
should not be bounced at that point.

Retries use fixed increasing intervals rather than exponential backoff, because
mail and IM failures are dominated by greylisting and temporary 4xx, where the
first few exponential steps are far too dense to let a transient condition
clear.

Two more mechanisms back the same path:

- **claim timeout** — a delivery still in flight after the timeout is returned
  to the queue. The test is against the claim, not against a restart, so a
  killed process, a stuck worker and a power cut are all covered by the same
  sweep.
- **circuit breaker** — a failing channel is taken out of service so deliveries
  fail fast instead of each waiting out a connect timeout. State is persisted,
  so a restart does not forget an outage that is still in progress, and the
  half-open state admits only a configured number of probes: a channel that has
  just come back should be tested, not handed the backlog.

A `PERMANENT` rejection is deliberately **not** a channel fault. The endpoint
answered, and it answered correctly — the problem is the message. Counting it
would let one bad recipient address take the whole channel out of service.

### Quota

Each channel instance may declare an allowance
(`quota.per_second/minute/hour/day/month`). The allowance is **reserved before
the call** and settled by the outcome:

| Outcome | Settlement |
|---|---|
| sent / rejected / told to try later | **commit** — the peer really was called |
| never reached the peer | **rollback** — nothing to charge for |

Without that distinction, a channel that was unreachable for an hour would
spend a day's allowance on messages nobody received and then be refused for the
rest of the day. A delivery that cannot get a slot goes back to the queue
rather than failing, and does not spend a retry attempt.

Short windows slide; day and month are fixed periods, persisted, because that
is what a platform's allowance means.

### Operations

- `GET /metrics` — Prometheus (queue depth, attempts by class, delivery latency)
- `GET /healthz` — liveness, **does not touch the database**
- `GET /readyz` — readiness, **does check the database**

The split is deliberate: a liveness probe that fails on a dependency outage
gets the process restarted, which fixes nothing and loses in-memory state,
while a readiness probe that skipped the check would accept notifications the
service cannot store.

### Configuration

See [`configs/notifyrelay.example.yaml`](configs/notifyrelay.example.yaml).
Three rules are not negotiable:

1. **Secrets never go in the file.** Write `!env VAR_NAME`; a missing variable
   fails startup rather than silently becoming an empty credential.
2. **Timeouts must be set explicitly.** The handler timeout must exceed the
   per-delivery timeout.
3. **The claim timeout must exceed the delivery timeout**, or a slow delivery
   is mistaken for an abandoned one and sent twice.

### Architecture

```
producer  HTTP API ─┐
          SMTP in  ─┴─→ router ─→ breaker ─→ quota reserve ─→ rate limit ─→ channel ─→ settle
                            │                                                      │
                            └─→ format dialect / length split / audit ←────────────┘
                                              │
                                   durable queue (SQLite + bodies on disk)
```

`internal/store` is the seam for another database. The interfaces are
relational-shaped but not SQL-shaped: a claim reads and marks in one step,
which SQLite does with an immediate transaction and MySQL does with
`SELECT ... FOR UPDATE SKIP LOCKED`. Nothing above that package knows which
engine is underneath — only the switch in `openStore()` does.

### Deployment

```bash
# Docker Compose, one command
cp .env.example .env          # fill in the secrets; .env is gitignored
cp configs/notifyrelay.example.yaml configs/notifyrelay.yaml
docker compose up -d

# Kubernetes
helm install notifyrelay deploy/helm/notifyrelay   --set image.repository=<your-registry>/notifyrelay --set image.tag=<version>

# Bare metal
install -m 0755 notifyrelay /usr/local/bin/
install -m 0644 deploy/systemd/notifyrelay.service /etc/systemd/system/
systemctl enable --now notifyrelay
```

**Reload without a restart**: `systemctl reload notifyrelay` (or `kill -HUP`). Log
level, API keys, timeouts, the retry cadence, breaker thresholds and the channel
configuration all take effect; the listen address, storage paths and worker count
need a restart, and **the service names them in its log** rather than leaving you
to guess which half of your edit landed.

The operations manual — backup, upgrade, metrics, troubleshooting, and an honest
list of what has not been verified — is [`docs/06-operations.md`](docs/06-operations.md)
(Chinese).

### Acknowledgements

The email composition and encoding policy is derived from
[`kubesphere/notification-manager`](https://github.com/kubesphere/notification-manager)
(Apache-2.0). Full attributions are in [`NOTICE`](NOTICE).

### License

**GNU Affero General Public License v3.0** — see [`LICENSE`](LICENSE).

Under AGPL section 13, users interacting with this software over a network are
entitled to its corresponding source.
