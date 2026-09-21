# 01 · 技术栈建议

> 前置阅读：`docs/00-research.md`。本文的选型结论直接引用调研中可移植代码的 License 与质量。

## 结论先行

**推荐技术栈：Go**。核心理由不是"Go 更快"，而是——**第一阶段最耗时的邮件通道实现，在 Go 生态里存在可直接移植的 Apache-2.0 生产级代码，而在 Python / Node 生态里没有等价物。**

**次选：Python + FastAPI**。若团队 Python 熟练度显著高于 Go，或需要尽快出可用版本，Python 是合理的第二选择，代价是运维复杂度与"假异步"陷阱。

**不推荐：Node / NestJS**。队列生态（BullMQ）强制引入 Redis，与"第一阶段不引入外部中间件"的结论冲突；邮件库虽好但与 Go/Python 相比无决定性优势。

---

## 一、三套候选栈的具体组成

所有库的 License 与活跃度均已用 GitHub API 核实（2026-09-21）。

### 方案 A：Go

| 用途 | 选型 | License | 活跃度 |
|---|---|---|---|
| HTTP 框架 | `go-chi/chi` | MIT | 22.8k★，2026-09-18 |
| SMTP 服务端（收信入口） | `emersion/go-smtp` | MIT | 2.0k★，2026-09-17 |
| SMTP 客户端（发信出口） | `wneessen/go-mail` | MIT | 1.5k★，2026-09-14 |
| 邮件消息组装与编码 | **移植** `kubesphere/notification-manager` 的组装逻辑（非整个 email.go） | Apache-2.0 | 2026-06-22 |
| 配置 | `spf13/viper` 或 标准库 + `yaml.v3` | MIT | 30.5k★ |
| 日志 | 标准库 `log/slog` | Go 官方 | — |
| 指标 | `prometheus/client_golang` | Apache-2.0 | 6.0k★ |
| 队列（第一阶段） | **无**，goroutine + channel + SQLite | — | — |
| 队列（M2 之后可选） | `hibiken/asynq`（需 Redis） | MIT | 13.7k★ |

### 方案 B：Python + FastAPI

| 用途 | 选型 | License | 活跃度 |
|---|---|---|---|
| Web 框架 | `fastapi` + uvicorn | MIT | 102.5k★，2026-09-18 |
| 数据校验 | `pydantic` v2 | MIT | 28.8k★，2026-09-18 |
| SMTP 服务端 | `aio-libs/aiosmtpd` | Apache-2.0 | 373★，2026-09-16 |
| SMTP 客户端 | `cole/aiosmtplib` | MIT | 431★，2026-09-18 |
| HTTP 客户端 | `httpx` | BSD-3 | 15.5k★，2026-03-29 |
| 重试 | `tenacity` | Apache-2.0 | 8.8k★，2026-09-01 |
| ORM/迁移 | `sqlalchemy` 2.0 + Alembic | MIT | 12.2k★，2026-09-19 |
| 持久队列参考实现 | **移植** `saderi/SMTP-Switch` 的 spool+SQLite 机制 | MIT | 2026-08-31 |

### 方案 C：Node / NestJS

| 用途 | 选型 | License | 活跃度 |
|---|---|---|---|
| 框架 | `nestjs/nest` | MIT | 76.7k★，2026-09-21 |
| 邮件 | `nodemailer` | ⚠️ GitHub 识别为 NOASSERTION，**需人工确认 LICENSE 文件** | 17.7k★，2026-09-14 |
| 校验 | `colinhacks/zod` | MIT | 44.0k★，2026-09-19 |
| 队列 | `taskforcesh/bullmq` | MIT | 9.4k★，2026-09-21（**需 Redis**） |
| SMTP 服务端 | 无成熟等价物 | — | — |

---

## 二、六维度对比

| 维度 | Go | Python FastAPI | Node / NestJS |
|---|---|---|---|
| **开发速度** | 中。样板代码多，但通道数量少（第一阶段 1-2 个），样板成本可控 | **最快**。调研中同类项目 Python 占多数（mailrise / SMTP-Switch / heimdallr / simplerelay / prom2teams / NotifyHub） | 快。TS 类型 + 装饰器，但 NestJS 模块/DI 样板不轻 |
| **部署与维护成本** | **最低**。`CGO_ENABLED=0` 单静态二进制，无运行时、无 node_modules、无虚拟环境；Windows/Linux 同源 | 中。需 Python 运行时；Windows 上打单 exe 麻烦（PyInstaller），Docker 是常规解 | 中高。需 Node 运行时 + node_modules；单文件打包（pkg/nexe）生态不如 Go 稳 |
| **邮件库生态** | **好，且有杀手锏**：`emersion/go-smtp`（服务端）+ `wneessen/go-mail`（客户端）双活跃；**`notification-manager` 的 email notifier 是 Apache-2.0 的生产级完整实现，可直接移植**（中文主题 MIME 编码、multipart/alternative、STARTTLS/465 分支、CRAM-MD5/PLAIN/LOGIN、可配 CA） | **好**：`aiosmtpd` + `aiosmtplib` 是 aio-libs 系；但服务端库仅 373★，比 Go 侧重；**无现成可移植的中文场景生产级邮件实现** | **好**：`nodemailer` 是最成熟的邮件库（17.7k★）；但 **License 需人工确认**，且 Node 无成熟 SMTP **服务端**库 |
| **队列/重试生态** | **最好且无需外部依赖**。goroutine + channel + `context` 天然做超时/取消/并发；持久队列无需 Redis（SQLite 即可） | 好。`tenacity` 做重试；持久队列需自己写（**有 SMTP-Switch 的实现可参考**）；注意必须用 async 库，否则踩"假异步"陷阱 | **最差（对本项目）**。BullMQ 是标准答案但**强制引入 Redis**，与"第一阶段不引入外部中间件"冲突 |
| **钉钉/飞书/Webhook 接入难度** | **低，且有现成模板**。这类通道本质是 HMAC 签名 + JSON POST，标准库足够；**`alertmanager-webhook-adapter` 的中文 IM 模板与 Payload 模型是 Apache-2.0 可直接抄**（钉钉/飞书/企微 markdown 格式、@某人、Buttons/Links） | 低。但需自行踩中文 markdown 格式的坑（heimdallr 的实现是 GPL，**不能用**） | 低。生态有 slack SDK 等，但中文 IM 模板同样需自己踩坑 |
| **长期扩展性** | **最好**。接口 + Factory 注册表是 Go 的强项，且**有一个真实的生产级范例**（notification-manager 的 `Receiver`/`Notifier` 双层 + `Register()`） | 好。装饰器/目录扫描注册可行（apprise 是 Python 的），但**caronc/apprise 是 BSD-2，可作为设计范本** | 好。但缺一个多通道中继的成熟 Go/Python 级别范例 |

---

## 三、为什么推荐 Go（逐条理由）

### 1. 决定性因素：可移植代码的质量与 License

第一阶段的工作量大头是**邮件通道**。三个生态的对比：

- **Go**：`kubesphere/notification-manager`（**Apache-2.0**）的 `pkg/notify/notifier/email/email.go` 里有已经踩过坑的**消息组装与编码逻辑**——中文主题 `mime.QEncoding`、`multipart/alternative` 双正文、`quoted-printable`。**这三块可以移植并保留署名**，省掉至少一周的编码踩坑（连接/TLS/AUTH 交给 `wneessen/go-mail`，不重复造）。中文 IM 模板同理（`alertmanager-webhook-adapter`，**Apache-2.0**）。
- **Python**：`heimdallr` 有邮件 + 5 个国内 IM 通道，但它是 **GPL-3.0，不能用**；`SMTP-Switch` 只有 SMTP 一种通道且不能直接移植通道层（其价值在**队列机制**，机制可移植）。
- **Node**：`nodemailer` 成熟，但**没有任何一个同类中继项目的邮件+中文 IM 实现是可抄的**。

**结论**：选 Go 相当于**用一次技术栈选择，换回两周以上的邮件通道开发与踩坑时间**，且 License 干净。

### 2. 部署成本：这是要长期常驻的内部服务

NotifyRelay 的定位是"为内部服务提供统一通知发送入口"，意味着要长期跑在服务器上。Go 的单二进制 + 零依赖在这一场景是数量级的优势：

- 无 Python 运行时/虚拟环境/node_modules 的版本漂移问题。
- 第一阶段连 SQLite 都不需要——**内存有界队列即可**（调研显示 18 个项目里 15 个都没有持久化队列，第一阶段不上 Redis/MQ 是被业界接受的做法）。
- 后期上持久化，SQLite 单文件即可满足（照搬 SMTP-Switch 的"正文落 spool 文件 + 元数据落 DB"），依然无外部中间件。

### 3. "假异步"陷阱在 Go 里不存在

调研中最严重的实现缺陷就来自 Python：**heimdallr 在 `async def` 里调同步阻塞的 `requests.post` / `smtplib`，`asyncio.TaskGroup` 并发形同虚设，实际串行且阻塞事件循环**（`heimdallr/api/base.py:44`）。这是 Python 异步生态的经典陷阱——写起来不报错，压测才暴露。

Go 的 `context` + goroutine + channel 把超时、取消、并发扇出变成语言级一等公民，写错的概率显著更低。对一个"扇出到多个下游通道"的服务，这是实打实的可靠性差异。

### 4. 通道抽象落地最自然

`docs/00-research.md` 的结论是：**抽象必须落在"通道类型"这一层，注册表必须只有一处且是显式 `Register()` 或扫描驱动**。Go 的接口 + Factory 函数类型恰好是这个形状，且 `notification-manager` 已经验证过：

```go
type Notifier interface {
    Notify(ctx context.Context, msg *Message) Result
}

type Factory func(cfg ChannelConfig) (Notifier, error)

func Register(scheme string, f Factory)   // init() 里一行
```

核心路由只做 `factories[scheme](cfg).Notify(ctx, msg)`，**代码里不出现任何具体通道名**。

### 5. 本机已具备 Go 环境

`go1.24.10` 已安装（**但见下方风险提示**）。Node v22.17.0 / Python 3.12.0 亦可用，但 Go 无需额外运行时配置。

---

## 四、推荐方案的风险提示

### ⚠️ 风险 1：本机 Go 是 32 位工具链

实测 `go version` 输出 **`go1.24.10 windows/386`**——即 **386（32 位）**，而非常规的 `windows/amd64`。

- **影响**：默认 `go build` 产出 32 位二进制，内存受限（单进程约 4GB 地址空间），且不能直接链接某些 64 位-only 的 CGO 库。
- **规避**：交叉编译即可产出 64 位目标——`$env:GOARCH="amd64"; go build`。生产部署若用 Docker（`golang:1.24` 镜像）则完全无影响。
- **建议**：实施前重装 64 位 Go 工具链，或在构建脚本里固定 `GOARCH=amd64`。

### ⚠️ 风险 2：Go 的 `net/smtp` 已冻结

Go 官方已停止为 `net/smtp` 添加新特性（明确不再维护为新功能目标）。**不要用 `net/smtp`**，用 `wneessen/go-mail`（MIT，活跃）作为客户端底座；只在**消息组装与编码**环节移植 notification-manager 的逻辑，不移植它的自研 SMTP 客户端。

### ⚠️ 风险 3：SQLite 驱动的 CGO 依赖

若后期用 SQLite 做持久队列，`mattn/go-sqlite3` 需要 CGO，会破坏 `CGO_ENABLED=0` 单静态二进制。**需改用纯 Go 实现（如 `modernc.org/sqlite`，选型前需核实其当前版本与 SQLite 版本兼容性）**，或直接选 Postgres。

### ⚠️ 风险 4：Go 生态缺少"多通道中继"的现成骨架

`notification-manager` 是 K8s Operator，**不能直接用**（配置中心是 CRD，脱离 K8s 跑不起来）。它的价值是**代码片段与架构参考**，不是基座。Go 侧需要自己写服务骨架，这部分样板工作约 2-3 天。

---

## 五、什么情况下应该改选 Python

以下任一条件成立时，**Python + FastAPI 是更优选择**：

1. **团队 Python 熟练度显著高于 Go**。技术栈选择的隐性成本是维护者的熟悉度，远超语言性能差异。
2. **需要复用 SMTP-Switch 的持久队列实现**（MIT）。它的 spool + SQLite + 单 producer claim + orphan 回收 + `no_capacity_backoff` + 熔断写穿是一套完整且经过设计的机制，**Python 可以直接读源码移植，Go 只能重新实现机制**。
3. **需要在一个月内出可用版本**，且不接受 Go 的样板期。

**改选 Python 时必须守住的底线**（来自调研中的真实失败案例）：
- 出站 HTTP 必须用 `httpx`（异步），**绝不用 `requests`**；出站 SMTP 必须用 `aiosmtplib`，**绝不用 `smtplib`**——否则重蹈 heimdallr 的假异步覆辙。
- 邮件**必须支持 HTML** + `multipart/alternative` 纯文本兜底（heimdallr 只发 plain text，是明确的功能缺陷）。
- 部署用 Docker，不要试图在 Windows 上打单 exe。

---

## 六、最终推荐

| 项 | 选择 |
|---|---|
| **语言** | **Go 1.24+**（构建时固定 `GOARCH=amd64`） |
| **HTTP** | `go-chi/chi`（MIT） |
| **SMTP 入口** | `emersion/go-smtp`（MIT） |
| **SMTP 出口** | `wneessen/go-mail`（MIT）作客户端底座；**只移植** `notification-manager` 的消息组装与编码逻辑（Apache-2.0，文件头署名） |
| **配置** | YAML 文件 + 环境变量覆盖；抄 mailrise 的 `!env_var` constructor 思路（MIT）让密钥不落配置文件 |
| **日志** | 标准库 `log/slog`（结构化 JSON） |
| **存储（M0-M2）** | 无。内存有界队列 |
| **存储（M4+）** | SQLite（纯 Go 驱动）或 Postgres，二选一 |
| **部署** | 单静态二进制 + Docker 镜像 |

> 注：本文件的选型仅为建议，最终由你确认。相关决策点见 `docs/03-plan.md` 末尾的待确认清单。
