# 02 · 功能边界

> 前置阅读：`docs/00-research.md`（业界做法与反例）、`docs/01-tech-stack.md`（选型）。
> 本文第四节的通道接口设计是**从调研结论直接推导出来的**，是"以后加钉钉/飞书不用改核心路由"的落地答案。

---

## 一、MVP 必须有

### 1.1 邮件通道（第一阶段唯一的下游通道）

| 项 | 要求 |
|---|---|
| 发信协议 | SMTP。**支持两种模式，用配置切换**：① 经上游 SMTP 中继（Gmail / 企业邮箱 / SES SMTP / Postfix smarthost）；② 直投收件人 MX |
| 认证 | LOGIN / PLAIN / CRAM-MD5 三种 AUTH 机制 |
| 加密 | 465 隐式 TLS 与 587 STARTTLS **自动分支**；`RequireTLS` 开关；可配自签 CA |
| 正文格式 | **必须支持 HTML**，且用 `multipart/alternative` 同时给出自动转换的纯文本兜底 |
| 中文 | 主题用 `mime.QEncoding` 编码；正文 `quoted-printable` 或 base64；**中文主题乱码是最容易翻车的地方** |
| 抄送/密送 | `Cc` / `Bcc` 支持 |
| 附件 | MVP 可选（建议**不做**，留到 M2） |
| 多收件人 | 支持，且**部分失败要能区分**（哪些收件人成功、哪些被拒） |

**实现路线**（已定，不再变更）：

- **底座**：`wneessen/go-mail`（MIT，活跃）作为 SMTP 客户端，负责连接、TLS 分支、AUTH、投递。
- **从 `kubesphere/notification-manager` 移植的是"消息组装与编码逻辑"**（**Apache-2.0，文件头保留来源注释 + 署名**），即三块：
  1. `mime.QEncoding` 中文主题编码
  2. `multipart/alternative` 双正文（HTML + 自动降级的纯文本）
  3. `quoted-printable` 正文编码
- **不移植整个 `email.go`**——那是自研 SMTP 客户端（`net.Dialer` + `tls.Dial` + 手写 AUTH），与 `go-mail` 职责完全重叠。

### 1.2 统一发送 API

```
POST /api/v1/notify
```

**请求体**（上游只描述"发生了什么"，不描述"怎么发"）：

```json
{
  "targets": ["mailto://ops@example.com", "email:oncall"],
  "title":   "数据库主从延迟告警",
  "body":    "**延迟**: 12s\n**实例**: db-03",
  "format":  "markdown",
  "type":    "warning",
  "priority": 4,
  "tags":    ["db", "prod"],
  "links":   [{"text": "Grafana", "url": "https://..."}],
  "at":      ["zhangsan"]
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `targets` | ✅ | 目标通道。可以是**通道别名**（`email:oncall`）或 **URL**（`mailto://...`）。**一次请求支持多个目标，服务端并发扇出**。URL 形式在 **M6** 落地 |
| `to` | | 本请求的收件人，M6 新增。与 `mailto://` 目标等价，二选一 |
| `title` | ✅ | 标题 |
| `body` | ✅ | 正文 |
| `format` | | `text` \| `markdown` \| `html`，默认 `text`。**由调用方声明，服务端不猜**（抄 pushbits 的 `contentType` 思路） |
| `type` | | `info` \| `success` \| `warning` \| `failure`，默认 `info`。各通道自行降级（邮件→主题前缀、IM→emoji/配色） |
| `priority` | | 1-5（抄 alphorn 的统一刻度），供将来的路由过滤与 IM 视觉强度使用 |
| `tags` | | 标签数组，供将来的路由过滤使用 |
| `links` / `at` | | 结构化链接与 @ 某人（抄 `alertmanager-webhook-adapter` 的 Payload 模型） |

**响应**：必须**按目标分别返回结果**，不能只给一个总的成功/失败：

```json
{
  "request_id": "01J...",
  "results": [
    {"target": "email:oncall", "status": "sent",    "channel": "smtp-relay", "elapsed_ms": 342},
    {"target": "mailto://x@y.z", "status": "failed", "channel": "smtp-relay",
     "error_class": "PERMANENT", "error": "550 mailbox unavailable"}
  ]
}
```

### 1.3 SMTP 入口（**已确认进 M1**）

调研中 5 个邮件类项目有 4 个是"SMTP 进、SMTP 出"。NotifyRelay 若只有 HTTP 入口，很多现成系统（cron 的 `mail`、Postfix、各类只支持 SMTP 通知的软件）就接不进来。

**服务端底座**：`emersion/go-smtp`（MIT，活跃）。

**路由语法采用 mailrise（MIT）的做法**：**收件人地址即路由指令**

```
ops@relay.local                  → 默认通道
email.failure@relay.local        → email 通道，type=failure
oncall.warning@relay.local       → oncall 通道别名，type=warning
```

local part 用 `.` 分隔：`<通道别名>[.<级别>]`。**零成本、不需要新的 header 约定**，且表达能力与 HTTP 入口对齐。

### 1.4 鉴权

| 项 | MVP 要求 |
|---|---|
| 方式 | **Bearer API Key**（`Authorization: Bearer nr_xxx`） |
| 存储 | **只存哈希**（SHA-256 足够，比 bcrypt 省 CPU。抄 hyvor/relay 的做法） |
| 元数据 | key 上可挂 `name` / `enabled` / `allowed_ips`（可选） |
| 配置来源 | 写在配置文件里（MVP 不做管理后台，所以 key 由配置定义） |
| 常量时间比较 | **必须**，防时序侧信道 |
| SMTP 入口鉴权 | 若做 SMTP 入口：源 IP 白名单 + 可选 SMTP AUTH |

**MVP 不做**：HMAC 签名、时间戳防重放、内容哈希绑定 token（chanify 的方案，留到 M4）。

### 1.5 配置

- **格式**：单个 YAML 文件。
- **结构**：**必须分层，不能扁平**。调研中 heimdallr 把凭据和目标混在同一命名空间，导致 `config/definition.py` 70+ 常量爆炸。

```yaml
server:
  addr: ":8080"
  smtp_addr: ":2525"        # SMTP 入口
auth:
  api_keys:
    - name: "internal-services"
      key_hash: "sha256:..."
      enabled: true
channels:
  - name: "oncall"           # ← 别名，API 里的 targets 用它
    type: "email"            # ← 通道类型，对应代码里的 Channel 实现
    enabled: true
    config:
      host: "smtp.example.com"
      port: 465
      tls: "implicit"        # implicit | starttls | none
      username: "notify@example.com"
      password: "!env SMTP_PASSWORD"   # ← 密钥不落配置文件
      from: "NotifyRelay <notify@example.com>"
retry:
  max_attempts: 5
  backoff: [30s, 2m, 10m, 1h]
  max_age: 24h
```

- **密钥注入**：抄 mailrise 的 `!env_var` YAML constructor（**MIT**，约 30 行）——让配置文件里写 `!env SMTP_PASSWORD` 而不是明文密码。
- **环境变量覆盖**：`NR_CHANNELS_0_CONFIG_PASSWORD=xxx` 之类的规则（抄 SMTP-Switch 的 `SECTION__KEY` 思路）。

### 1.6 日志

- **结构化 JSON**（`log/slog`），字段固定：`request_id` / `target` / `channel` / `channel_type` / `elapsed_ms` / `result` / `error_class` / `error`。
- **必须脱敏**：配置文件里的密码、API key 一律不进日志。调研中 heimdallr 在 debug 模式下把全部环境变量（含所有通道密钥）打进日志，是明确的反例。
- **每个请求一个 `request_id`**，贯穿所有目标的投递日志，便于串联排查。

### 1.7 错误重试

**这是 MVP 里最容易被低估、但决定服务质量的部分。**

**核心契约：所有 Channel 实现必须返回三分类结果**（抄 SMTP-Switch `dispatch/sender.py:26-31`）：

| 分类 | 含义 | 处理 |
|---|---|---|
| `CONNECT_ERROR` | **没能碰到对端**（DNS 失败、连接超时、TLS 握手失败） | 可重试；**不消耗通道配额**；计入通道健康度 |
| `TRANSIENT` | 对端明确说"稍后再试"（4xx、429、5xx 部分） | 可重试；**消耗配额**；计入通道失败计数 |
| `PERMANENT` | 对端明确拒绝（5xx 硬拒绝、地址不存在、鉴权失败） | **不重试**，直接终态失败 |

这个分类同时决定三件事：**要不要重试、要不要消耗配额、要不要算作通道故障**。

**重试策略**：
- **固定递增间隔**（抄 hyvor/relay：30s / 2m / 10m / 1h / 4h，与配置对齐），**不用指数退避**——邮件失败的典型原因是 MX 灰名单和临时 4xx，指数退避前几次太密（对邮件域无意义）、后几次太疏。
- **双上限**：最大尝试次数 + **最大存活时间**（`max_age`，超龄直接进死信）。
- **`no_capacity_backoff`**（抄 SMTP-Switch `worker.py:277-283`）：当目标通道整体不可用时，**挂起消息但不消耗尝试次数**。没有这条，下游集体抽风会把整个队列打成死信。
- MVP 的队列是**内存有界队列**；进程重启丢队列是**可接受的**（调研显示 18 个项目中 15 个没有持久化队列）。**持久化留到 M4。**

### 1.8 模板

- MVP：**极简占位替换**，不做逻辑。抄 alphorn 的 `{dotted.path}` 思路或 mailrise 的 `string.Template`（**MIT**）。
- 支持的变量：`{title}` `{body}` `{type}` `{priority}` `{tags}` `{timestamp}` `{request_id}` 以及上游 payload 的点路径（`{payload.instance}`）。
- **明确不做**条件、循环、helper 函数。
- 邮件自带**默认模板**（标题 + 正文 + 可选页脚/来源标记），允许按通道覆盖。

### 1.9 幂等（MVP 建议做最小版）

- 上游可带 `Idempotency-Key` 头；**MVP 只需在内存里记录 key → request_id 的映射（TTL 覆盖重试窗口）**，重复请求直接返回首次结果。
- 完整的"回放整份响应"（hyvor 的做法）留到 M4 与持久化一起做。

---

## 二、后续扩展有什么

| 阶段 | 内容 |
|---|---|
| **M2 多通道抽象落地后** | Webhook 通道（通用 HTTP POST）；通道自声明能力（长度上限、速率、目标格式）驱动的自动降级与切分 |
| **M3 IM 通道** | 钉钉（含加签）、飞书、企业微信（群机器人 + 应用）、Slack。**模板可直接抄 `alertmanager-webhook-adapter`（Apache-2.0）的中文 markdown 模板** |
| **M3 上游适配** | 兼容 Gotify 入口协议（`X-Gotify-Key` header + `?token=` query，抄 pushbits 的 ISC 做法），让现成客户端零改造接入 |
| **M4 可靠性** | 持久化队列（SQLite/Postgres）；崩溃恢复与 orphan 回收；熔断（状态写穿 DB，重启不忘）；配额预占 `try_reserve/commit/release`；完整幂等回放 |
| **M4 审计** | 投递流水表（每次尝试一行：通道/结果分类/错误码/耗时）；失败消息死信箱；保留期清理。**必须在应用层写库，绝不解析日志**（simplerelay 的反例） |
| **M4 限流** | 通道级多窗口限流（每秒/分/时/日）；接有日限额的钉钉/飞书 API 时必需；上游接入侧请求限流 |
| **M5 管理后台** | 渠道配置的增删改查（配置从文件迁移到 DB，**密钥字段加密存储**）；投递记录查询；测试发送；通道连通性自检 |
| **M5 运维** | Prometheus 指标 + 健康检查端点；Docker 镜像 + Helm chart；失败告警（通知发不出去时如何通知——**注意防自环**） |
| **M5+ 消息能力** | 附件、富文本高级特性（卡片/按钮）、多语言模板、消息分组与抑制、延迟发送与取消 |

---

## 三、明确不做什么

**以下均为调研中识别出的、与本阶段目标不匹配或成本收益倒挂的功能，明确排除：**

### 3.1 架构层面

| 不做 | 原因 |
|---|---|
| **K8s Operator / CRD / sidecar** | notification-manager 的做法脱离 K8s 跑不起来，与"通用中继"定位冲突 |
| **内建 Postfix / MTA** | simplerelay 用 Postfix 扛 SMTP 接入 + 正则解析 syslog 做审计，导致审计延迟小时级、保留 24h，且清理任务顺手清空了延迟队列 |
| **DKIM/SPF/DMARC 自动化 + 内建 DNS server** | hyvor/relay 为此内建 DNS server，是本次调研中最大的复杂度陷阱。**第一阶段不做**——用上游 SMTP 中继就由中继方负责发信声誉 |
| **Redis / Kafka / RabbitMQ** | 第一阶段无持久化队列需求；SQLite 足以支撑 M4 的持久化 |
| **多语言混合进程** | hyvor 的三语言三进程（PHP+Go+Svelte）规模远超需要 |

### 3.2 功能层面

| 不做 | 原因 |
|---|---|
| **多租户 / 组织 / 成员 / RBAC / 计费** | 内部服务场景不需要；alphorn 与 notifo 的这套模型是 SaaS 形态。第一阶段"应用 = 一组 API key + 一组目标通道"即可 |
| **用户体系与登录** | 无管理后台就不需要。M5 做后台时再引入最小登录 |
| **规则引擎** | 不做 notifo 的自定义 JS 条件脚本（Jint 执行，安全面与运维成本都不划算）。**只保留最小结构**：目标关联上一个可空的过滤条件（字段限 `priority/tags/title/body`，操作符 `equals/not_equals/contains/regex`，组间 OR、组内 AND），**实现上先只支持"全部发送"**，结构预留 |
| **告警聚合 / 静默 / 抑制 / 分组** | 那是 Alertmanager 的领域语义，NotifyRelay 的消息来自 HTTP/SMTP，没有"告警聚合""静默期"概念 |
| **Lua/JS 脚本插件与热重载** | chanify 的 Lua 插件是过度设计；通道扩展用代码（编译期），不用脚本 |
| **已读回执 / 确认 / 取消发送** | notifo 的 `IfSeen`/`IfNotConfirmed` 是面向终端用户的产品特性，我们的收件人是下游通道 |
| **移动端推送（APNS/FCM）自建** | 需要额外的开发者账号与证书体系，且与"内部服务通知"场景不匹配 |
| **自建 SMTP 收信服务器用于接收外部邮件** | 本服务只接收"我们自己发起的通知"，不是通用 MTA，不需要别名/反垃圾/SPF 校验等能力 |
| **Web UI（M5 之前）** | 调研中 5 个项目的 UI 对我们都无参考价值 |
| **附件支持** | MVP 明确不做，M2 之后再评估 |

### 3.3 明确的安全红线（不做 = 必须避免）

1. **绝不用 `verify=False` / `CERT_NONE` 降级**——onepush 在 SSL 错误时反而关闭证书校验，simplerelay 的健康检查同样关闭校验，两者都是反例。
2. **绝不明文存储通道凭据**——simplerelay 有 `smtp_password_plain` 明文列。
3. **绝不用同一个密钥派生多种用途**——simplerelay 用 `SHA-256(RELAY_SECRET_KEY)` 同时做 JWT 签名与凭据加密，密钥泄露即双向失守。
4. **绝不把 token 放 URL query**——alertmanager-webhook-adapter 的渠道 token 放 query，会进访问日志和浏览器历史。
5. **绝不在 debug 模式打印配置全量**——heimdallr 会打印所有通道密钥。
6. **绝不硬编码 `debug=True` 之类的开发开关**——NotifyHub 的 `app.run(debug=True)` 是明确的事故隐患。

---

## 四、通道统一接口设计

> **目标：以后加钉钉/飞书，只加一个文件 + 一行注册，核心路由代码一字不改。**

### 4.1 设计原则（每条都对应调研中的证据）

1. **抽象边界划在"通道类型"这一层，不是"通道实例"这一层。**
   类型 = 代码（`EmailChannel` / `DingTalkChannel`），实例 = 配置（`oncall` / `dev-group`）。
   → SMTP-Switch 把抽象放在"实例"层（配置即抽象），结果加一个新**类型**要改 4-5 个文件，是反面教材。

2. **注册只能有一处，且必须是显式注册而非手写 if/elif。**
   → onepush 的手写 `_all_providers` 字典导致"加通道必改核心文件"，是反面教材。
   → notification-manager 的 `Register(name, factory)` 是正面范例。

3. **通道元数据（参数 schema、长度上限、目标格式、速率）作为通道自己的属性声明，由核心读取。**
   → apprise 的类属性元数据是最干净的实现；核心据此做校验、降级、限流、文档生成。

4. **鉴权从通道逻辑里剥出来。**
   → guanguans/notify 的 `Authenticator` 三注入点是正面范例。否则钉钉加签、企微 access_token 刷新会污染每个通道。

5. **富文本降级上收到核心。**
   → apprise 的 `conversion.py`：通道只声明"我要 markdown"，核心负责把上游的 HTML 转成 markdown 或纯文本。

6. **所有通道强制返回错误三分类结果**（`CONNECT_ERROR` / `TRANSIENT` / `PERMANENT`）。

### 4.2 接口定义（Go 形态，其他语言可 1:1 映射）

```go
// ── 1. 统一消息模型：上游只描述"发生了什么" ──────────────────────
type Message struct {
    Title    string
    Body     string
    Format   Format     // Text | Markdown | HTML（上游声明，不猜）
    Type     Type       // Info | Success | Warning | Failure
    Priority int        // 1-5
    Tags     []string
    Links    []Link
    At       []string   // @某人
    Attach   []Attachment
    Meta     map[string]any  // 通道私有扩展，仅目标通道读
}

// ── 2. 发送结果：三分类是所有通道的强制契约 ──────────────────────
type ResultClass int
const (
    ClassSent         ResultClass = iota // 已送达
    ClassConnectError                    // 没碰到对端 → 可重试、不消耗配额
    ClassTransient                       // 对端说稍后再试 → 可重试、消耗配额
    ClassPermanent                       // 对端硬拒绝 → 不重试
)

type Result struct {
    Class     ResultClass
    Err       error
    Detail    string          // 供审计：对端原始响应摘要
    Elapsed   time.Duration
}

// ── 3. 通道能力：由通道自声明，核心据此做降级/切分/限流 ──────────
type Capability struct {
    BodyMaxLen        int      // 0 = 不限（钉钉 5000、Slack 4000、Teams ~24KB）
    TitleMaxLen       int
    SupportedFormats  []Format // 通道能吃的格式
    SupportAttachment bool
    RatePerSec        float64  // 0 = 不限
    OverflowMode      Overflow // Truncate | Split | Error
}

// ── 4. 通道接口：唯一的发送入口 ─────────────────────────────────
type Channel interface {
    // 元数据（编译期常量，注册时读取）
    Type() string                      // "email" / "dingtalk" / "feishu"
    Capability() Capability
    ParamSchema() []ParamSpec          // 配置参数 schema：类型/必填/敏感/默认值

    // 业务
    Send(ctx context.Context, msg *Message) Result
    Test(ctx context.Context) Result   // 连通性自检，供 M5 后台用
}

// ── 5. 鉴权独立于通道逻辑（抄 guanguans/notify 的分层）───────────
type Authenticator interface {
    ApplyToRequest(req *http.Request) error
    // 部分下游（企微/钉钉）需要先换 token 且要缓存，故保留刷新钩子
    Refresh(ctx context.Context) error
}

// ── 6. 注册表：唯一的"新通道接入点" ─────────────────────────────
type Factory func(instanceName string, cfg map[string]any) (Channel, error)

var registry = map[string]Factory{}

func Register(channelType string, f Factory) {
    if _, dup := registry[channelType]; dup {
        panic("duplicate channel type: " + channelType)  // ← 必须报错，不能静默覆盖
    }
    registry[channelType] = f
}

// 各通道在自己文件里自注册（init 或显式 RegisterAll）
func init() { Register("email", NewEmailChannel) }
```

### 4.3 核心路由：代码里不出现任何通道名

```go
// Router 是"加通道不改核心路由"的兑现点
func (r *Router) Deliver(ctx context.Context, target Target, msg *Message) Result {
    // 1. 解析目标 → 拿到通道实例（别名查配置，或 URL 解析 scheme）
    ch, cfg, err := r.resolve(target)
    if err != nil {
        return Result{Class: ClassPermanent, Err: err}
    }

    // 2. 降级：把上游声明的格式转成通道能吃的格式（核心负责，通道不管）
    msg = Format.Downgrade(msg, ch.Capability().SupportedFormats)

    // 3. 溢出处理：按通道声明的长度上限截断或切分（核心负责）
    msg = Overflow.Apply(msg, ch.Capability())

    // 4. 限流：按通道自声明的速率（核心负责）
    if err := r.limiter.Wait(ctx, target, ch.Capability().RatePerSec); err != nil {
        return Result{Class: ClassTransient, Err: err}
    }

    // 5. 发送 —— 唯一一行涉及具体通道的代码，且只认接口
    res := ch.Send(ctx, msg)

    // 6. 审计与指标（核心负责）
    r.audit.Record(target, ch.Type(), msg, res)
    return res
}

// resolve：别名 → 配置实例；URL → 查注册表问 scheme 归谁，再由那个通道自己解析
// 路由代码里没有任何 "email" / "mailto" / "dingtalk" 字样
func (r *Router) resolveRef(ref string) (channel.Target, error) {
    if d, ok := channel.LookupScheme(schemeOf(ref)); ok {
        t, err := d.ParseTarget(ref)   // ← 通道自己的解析器
        return t, err                  //    URL 里没有凭据，只有"借哪个实例"
    }
    name, wantType := splitRef(ref)    // 别名 / 类型:别名
    ...
}
```

**与本节最初写法的偏离（M6 实测）**：原计划是「URL → 按 scheme 造临时实例」，
实践中改成了「URL → 借用某个**已配置实例**的传输」。原因是临时实例需要凭据，
而凭据只能来自配置——要么写进 URL（`mailto://user:pass@smtp.example.com`，
凭据进日志和审计，正是 `03-plan.md` 反复要避免的），要么等于把实例配置重新拼一遍。
借用的做法还顺带让配额、熔断、审计、密钥擦除**全部落在同一个实例上**，一行都不用改。

### 4.4 新增一个通道需要做什么

```go
// channels/dingtalk/dingtalk.go —— 唯一新增的文件
package dingtalk

func init() { channel.Register("dingtalk", New) }

type Channel struct { auth *SignAuth; client *http.Client }

func (c *Channel) Type() string { return "dingtalk" }

func (c *Channel) Capability() channel.Capability {
    return channel.Capability{
        BodyMaxLen:       5000,                    // 钉钉硬限制
        TitleMaxLen:      64,
        SupportedFormats: []channel.Format{channel.Markdown},
        RatePerSec:       20.0 / 60,               // 20 次/分钟（钉钉频控）
        OverflowMode:     channel.Split,
    }
}

func (c *Channel) ParamSchema() []channel.ParamSpec {
    return []channel.ParamSpec{
        {Name: "webhook",  Type: "string", Required: true,  Private: true, Label: "机器人 Webhook"},
        {Name: "secret",   Type: "string", Required: false, Private: true, Label: "加签密钥"},
    }
}

func (c *Channel) Send(ctx context.Context, msg *channel.Message) channel.Result {
    // 只写钉钉的报文组装与调用；限流/降级/重试/审计都由核心完成
}
```

**改动清单：**
1. 新增 `channels/dingtalk/dingtalk.go`（含 `func init()` 自注册）
2. 在 `channels/all.go` 加一行 `_ "notifyrelay/channels/dingtalk"` 触发注册

**核心路由、消息模型、降级逻辑、限流、审计、配置结构——全部零改动。**

### 4.5 这个设计的自检清单

| 检查项 | 通过标准 |
|---|---|
| 核心代码里有具体通道名吗？ | ❌ 不能有。grep `"email"` / `"dingtalk"` 只应出现在通道自己的包和配置文件里 |
| 加一个通道要改几个文件？ | 2 个（通道文件 + 一行 import） |
| 加一个通道实例要改代码吗？ | ❌ 不用，只改配置 |
| 新通道忘记声明长度上限会怎样？ | ✅ 核心用默认值兜底，不会崩 |
| 两个通道注册同一个 type 会怎样？ | ✅ panic 退出（**不能静默覆盖**——apprise 的 schema 冲突只打日志继续，是隐患） |
| 通道能绕过限流/审计自己发吗？ | ❌ 不能。`Send` 只能被 `Router.Deliver` 调用，且它拿不到 limiter/audit 的引用 |
| 富文本降级是谁做的？ | ✅ 核心。通道只声明"我吃什么格式" |
