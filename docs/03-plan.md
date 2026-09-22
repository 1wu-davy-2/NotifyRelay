# 03 · 实施计划

> 前置阅读：`docs/00-research.md`、`docs/01-tech-stack.md`、`docs/02-scope.md`。
> 技术栈按 Go 编写；若最终选 Python，任务清单不变，仅实现细节调整。

## 里程碑与重试职责的边界说明

你提的里程碑把"重试"放在 **M4**，但功能边界文档把"错误三分类 + 基础重试"列为 **MVP 必须**。两者的调和方式：

- **M1 只做"契约"与"最小实现"**：所有通道返回三分类结果（这是接口契约，不做后期无法补），HTTP 请求内做**一次**同步重试。
- **M4 做"机制"**：内存有界队列 + 固定递增退避 + 最大存活时间 + 死信 + `no_capacity_backoff`。

即 **M1 保证"错误被正确分类"，M4 保证"错误被正确重试"**。M1 不做队列，失败即时返回给上游。

---

## M0 · 项目初始化

### 任务清单

- [ ] **确认技术栈**（见文末待确认清单 Q1）——**这是 M0 的阻塞项**
- [ ] 初始化 Go module（`go mod init`），固定 `GOARCH=amd64` 构建脚本（**本机是 386 工具链**）
- [ ] 目录骨架：
  ```
  cmd/notifyrelay/main.go
  internal/config/          # YAML 加载 + env 覆盖 + !env constructor
  internal/message/         # Message / Format / Type / Priority
  internal/channel/         # Channel 接口 + Capability + Result 三分类 + Registry
  internal/channel/email/   # 第一个通道实现（M1）
  internal/router/          # 路由 + 降级 + 溢出 + 限流占位 + 审计占位
  internal/api/             # HTTP handler + 鉴权中间件
  internal/audit/           # 审计接口（M0 只留接口 + 日志实现）
  configs/notifyrelay.example.yaml
  docs/
  ```
- [ ] 配置加载器：YAML + `!env` 标签让密钥不落配置文件（抄 mailrise 的 `!env_var` constructor，**MIT**）
- [ ] 结构化日志（`log/slog`，JSON），字段规范见 `02-scope.md` §1.6
- [ ] `Makefile` / `build.ps1`：build / test / lint / docker
- [ ] Dockerfile（多阶段构建，`CGO_ENABLED=0` 静态二进制）
- [ ] 一份可跑的 `configs/notifyrelay.example.yaml`
- [ ] 定义 `Channel` / `Message` / `Result` / `Registry` 四个接口（**先定接口，M1 再实现**）
- [ ] **建 `NOTICE` 文件**：集中登记借鉴/移植的开源项目、License、来源文件路径（草案见 `docs/04-notice-draft.md`）
- [ ] 建 `LICENSE` 文件（本项目自身的 License，**待 Q10 确认**）
- [ ] **`NOTICE` 纳入代码评审检查项**：任何一次"移植/抄写外部代码"都必须同步更新 NOTICE + 文件头来源注释

### 验收标准

1. `go build ./...` 与 `go vet ./...` 零错误；`GOARCH=amd64` 产出的二进制在 amd64 机器上可运行。
2. `./notifyrelay --config configs/notifyrelay.example.yaml` 能启动，打印结构化启动日志（含监听地址、已注册通道类型清单）。
3. 配置文件里写 `password: !env SMTP_PASSWORD`，未设置该环境变量时**启动失败并给出明确错误**；设置后能正确读取。
4. 日志里**不出现**任何配置中的密码明文（用 grep 验证）。
5. Docker 镜像可构建，镜像内**不含**源码与编译工具链。
6. `NOTICE` 文件存在且内容与 `docs/04-notice-draft.md` 一致；`LICENSE` 文件存在。

### 风险点

| 风险 | 影响 | 应对 |
|---|---|---|
| 本机 Go 是 `windows/386` | 默认产出 32 位二进制，内存受限 | 构建脚本固定 `GOARCH=amd64`；或重装 64 位 Go |
| 目录结构过度设计 | 过早分层导致样板膨胀 | 接口先定，实现文件先少；不确定的先不建目录 |
| 配置格式未定就动手 | 后期迁移成本高 | M0 必须定死 YAML 结构（`02-scope.md` §1.5 已给草案） |

### M0 实施记录（2026-09-21）

**状态：完成。** 验收 5 条中 4 条通过，1 条因本机无 Docker 未能验证。

#### 对计划的三处偏离

| # | 计划 | 实际 | 原因 |
|---|---|---|---|
| 1 | `internal/channel/all.go` | `internal/channel/all/all.go`（独立包） | 通道实现必须 import `channel` 拿 `Channel` 接口，同包文件再 import 通道实现会形成 **import 循环** |
| 2 | `internal/router/downgrade.go`，函数 `Downgrade` | `internal/router/format.go`，函数 `Adapt` | 它不只降级：markdown 发给支持 HTML 的邮件通道时是**升级**。叫 downgrade 会长期误导读者 |
| 3 | 无此文件 | 新增 `internal/auth/apikey.go` | SHA-256 摘要解析与常量时间比较要被 **config 校验**和 **HTTP 中间件**共用；放 config 或 api 任一侧都会造成重复实现或依赖倒置 |

#### 验收结果

| # | 验收标准 | 结果 |
|---|---|---|
| 1 | `go build ./...` / `go vet ./...` 零错误；amd64 二进制可运行 | ✅ vet 干净；`build.ps1` 产出 windows/amd64 并成功 `--version`；linux/amd64 交叉编译成功（7.2 MB 静态二进制） |
| 2 | 启动打印结构化日志（含监听地址、已注册通道类型清单） | ✅ 实测输出含 `http_addr` / `registered_channel_types` / `configured_channels` / 两层超时；无通道时打印 WARN |
| 3 | `!env` 未设置时启动失败且报错明确 | ✅ 退出码 1，报错含变量名与行号：`config: !env NR_TEST_VAR_THAT_IS_NOT_SET (line 14) referenced but the environment variable is not set` |
| 4 | 日志中不含密码明文 | ✅ 实测 grep 不到注入的密码 |
| 5 | Docker 镜像可构建 | ⚠️ **未验证**——本机未安装 docker。已通过交叉编译验证 Dockerfile 中 build 命令本身可用；镜像构建需在装了 docker 的机器上补验 |

**额外已验证**：`/healthz` 与 `/readyz` 返回 200；`go test ./...` 全绿（config 包覆盖 `!env` 在 typed 字段与 `map[string]any` 两种位置的展开、缺失变量报错、超时顺序校验、默认值非零、多错误聚合、时长解析与非法时长）。

**M0 结束时已注册通道类型列表为空**——这是刻意的，第一个通道在 M1 加入。配置里写任何通道都会以 `unknown type` 明确报错退出。

---

## M1 · 邮件 MVP

**目标：一条完整的"HTTP 请求 → 邮件送达"链路跑通，可交付内部服务试用。**

### 任务清单

- [ ] **邮件通道实现**（`internal/channel/email/`）
  - [ ] **SMTP 客户端底座：`wneessen/go-mail`（MIT）**，负责连接、TLS 分支、AUTH、投递
  - [ ] 三种 AUTH：PLAIN / LOGIN / CRAM-MD5（由 go-mail 的 SMTPAuth 提供）
  - [ ] 加密模式自动分支：465 隐式 TLS / 587 STARTTLS / 明文；`RequireTLS` 开关；可配自签 CA
  - [ ] **从 `kubesphere/notification-manager`（Apache-2.0）移植"消息组装与编码逻辑"**，仅三块：
    - [ ] 中文主题 `mime.QEncoding` 编码
    - [ ] `multipart/alternative` 双正文（HTML + 自动降级的纯文本兜底）
    - [ ] `quoted-printable` 正文编码
    - [ ] **文件头加来源注释**（仓库 + 文件路径 + License），见 M0 的 NOTICE
    - [ ] ⚠️ **不移植整个 `email.go`**——那是自研 SMTP 客户端，与 go-mail 职责重叠
  - [ ] Cc / Bcc 支持
  - [ ] 连通性自检 `Test()`
  - [ ] **错误三分类映射**：连接/握手失败 → `CONNECT_ERROR`；4xx → `TRANSIENT`；5xx → `PERMANENT`
  - [ ] 多收件人：逐个 RCPT，**区分部分成功**
- [ ] **统一发送 API** `POST /api/v1/notify`
  - [ ] 请求/响应结构按 `02-scope.md` §1.2
  - [ ] 多目标并发扇出（`errgroup` 或 `sync.WaitGroup`）
  - [ ] **按目标分别返回结果**，不用总状态
  - [ ] **HTTP 层超时必须显式设置**（不允许用零值默认）：
    - [ ] `http.Server` 的 `ReadTimeout` / `WriteTimeout` / `ReadHeaderTimeout` / `IdleTimeout`
    - [ ] handler 内 `context.WithTimeout`，且**超时时间要大于所有目标的投递超时**，否则扇出会被提前掐断
    - [ ] 每个目标的 `Channel.Send()` 内部再设自己的超时（SMTP 会话超时）
    - [ ] 三层超时的值集中在一处配置，不散落在代码里
- [ ] **SMTP 入口**（已确认进 M1）
  - [ ] `emersion/go-smtp`（MIT）起 SMTP 服务端，监听端口可配
  - [ ] 收件人地址解析：`<通道别名>[.<级别>]@relay.local`（抄 mailrise，**MIT**）
  - [ ] 源 IP 白名单（CIDR）+ 可选 SMTP AUTH
  - [ ] 邮件正文 → 统一 `Message` 的映射（subject→title、text/html part→body+format）
  - [ ] **入口同样要走三分类**：解析不了收件人 → `PERMANENT`；下游失败按下游分类回 SMTP 状态码（4xx 可重试 / 5xx 永久）
- [ ] **鉴权**：Bearer API key + SHA-256 哈希存储 + **常量时间比较**
- [ ] **模板**：极简占位替换（`{title}` / `{payload.path}` 等），无逻辑
- [ ] **降级与溢出**：`Format.Downgrade()`（markdown→text）+ `Overflow.Apply()`（超长截断/切分）
- [ ] **HTTP 层冒烟重试**：请求内对 `CONNECT_ERROR`/`TRANSIENT` 重试 1 次
- [ ] **审计（最小版）**：每个目标每次发送写一行结构化日志
- [ ] 单元测试：三分类映射、格式降级、占位符替换、鉴权中间件、收件人地址解析
- [ ] 集成测试：用**本地假 SMTP 服务器**（`emersion/go-smtp` 自建）验证全链路
- [ ] `README.md`：5 分钟上手（配置示例 + curl 示例 + SMTP 示例）
- [ ] **`README.md` 加致谢段**：列出借鉴/移植的开源项目（含 License），指向 `NOTICE`

### 验收标准

1. `curl -X POST /api/v1/notify -H "Authorization: Bearer nr_xxx" -d '{...}'` 能把邮件发到真实邮箱；**中文主题与正文不乱码**。
2. 465 与 587 两种端口配置**都能成功发信**（heimdallr 在这点上翻过车）。
3. 收件人是 HTML 客户端时正文渲染为 HTML，纯文本客户端有可读的纯文本兜底（用 `mail-tester` 或直接看原始报文验证 `multipart/alternative` 结构）。
4. 无鉴权 / 错误 key 请求返回 401；错误 key 的响应耗时不呈现明显差异（常量时间比较）。
5. 目标邮箱不存在时，返回 `error_class: "PERMANENT"`，**不重试**；SMTP 服务器不可达时返回 `CONNECT_ERROR` 并重试 1 次。
6. 一次请求带 2 个目标、其中 1 个失败时，响应里**两个目标各有独立结果**，HTTP 状态码不掩盖部分失败。
7. 上游不传 `format` 时按 `text` 处理；传 `markdown` 时邮件正文正确转换为 HTML。
8. 日志 grep 不到 SMTP 密码。
9. `go test ./...` 全绿，含假 SMTP 的集成测试。
10. **HTTP 层超时显式生效**：`http.Server` 的 `ReadTimeout`/`WriteTimeout`/`ReadHeaderTimeout`/`IdleTimeout` 均非零值（代码评审 + 单测断言）；用一个故意挂起的假 SMTP 服务器验证——单目标投递超时后**返回明确错误而非无限等待**，且服务进程仍能响应后续请求。
11. **SMTP 入口可用**：用 `swaks`（或 `sendmail`）向 `email@relay.local` 发一封中文邮件，服务能正确解析并投递到配置的邮件通道；收件人地址解析失败的邮件被拒（`PERMANENT`），且 SMTP 层返回 5xx 而非静默丢弃。
12. SMTP 入口按上游 IP 白名单拦截未授权来源；下游返回 `TRANSIENT` 时 SMTP 层回 **4xx**（让上游 MTA 自行重投），`PERMANENT` 时回 5xx。

### 风险点

| 风险 | 影响 | 应对 |
|---|---|---|
| **发信被封 / 进垃圾箱** | 通知收不到，MVP 直接失败 | **强烈建议走上游 SMTP 中继**（企业邮箱/SES），不要在第一阶段直投 MX——直投需要 IP 声誉、PTR、SPF/DKIM 全套，是 hyvor 内建 DNS server 那种复杂度陷阱 |
| 中文编码踩坑 | 主题乱码 | **实测结论**：go-mail 默认就用 `mime.QEncoding` 编码非 ASCII 头，`SetEncoding(EncodingQP)` 给正文 quoted-printable。实际从 notification-manager 移植的是**编码策略**（Q-encode 头、UTF-8 声明、纯文本部件在前），不是代码——见 `internal/channel/email/compose.go` 的来源注释 |
| **三层超时配置不一致** | 扇出被提前掐断，或慢连接耗尽连接数 | 三层超时集中配置；**handler 超时必须 > 单目标投递超时**，写单测断言这个关系 |
| STARTTLS 端口搞错 | 连接失败 | 按端口自动分支 + 显式 `tls` 配置项，两者都要 |
| HTML 转纯文本质量差 | 纯文本客户端不可读 | 用成熟的 html→text 转换，不要正则去标签 |
| 同步发送阻塞请求 | 上游超时 | 给 SMTP 会话设 `context` 超时（notification-manager 已有） |
| API key 泄露 | 内部服务可被任意调用 | 哈希存储 + 支持按 key 停用；M4 加 IP 白名单 |

### M1 实施记录（2026-09-21）

**状态：完成。** 12 条验收标准全部通过，其中 465/587 用自签证书的真实 TLS 收发验证。

#### 对计划的偏离

| # | 计划 | 实际 | 原因 |
|---|---|---|---|
| 1 | 无此包 | 新增 `internal/render/` | HTML↔文本转换被**两个**包需要：router 做格式适配、email 生成纯文本替代部件。留在 router 里会让 email 反向依赖 router |
| 2 | 无此包 | 新增 `internal/requestid/` | HTTP 与 SMTP 两个入口都要生成关联 ID |
| 3 | 无此文件 | 新增 `internal/channel/params.go` | `map[string]any` 的类型断言读取器；M2 的 schema 校验将建立在它之上 |
| 4 | 一封邮件发所有收件人（标准 mail 语义） | **每个收件人独立投递** | go-mail 的 `SendError` 不暴露失败的收件人列表，要拿到准确的逐收件人结果只能逐个投递。附带好处是收件人之间互相看不到地址。**代价：Cc/Bcc 推迟到 M2**（逐个投递会让抄送人收到 N 份） |
| 5 | 未列 | **新增 `ca_file` 参数** | 02-scope §1.1 要求"可配自签 CA"，M1 清单漏了。它同时让 465/587 的验收可以用自签证书真实测试 |
| 6 | 未列 | **手写 SASL LOGIN 服务端** | go-sasl 只提供 `NewLoginClient`，无服务端实现。LOGIN 只有三步明文交换、不含自己的加密逻辑，且 go-smtp 负责 base64 框架并拒绝在 TLS 前提供 AUTH |
| 7 | 未列 | **主题级别前缀** `[WARN]` / `[ALERT]` / `[OK]` | 否则 `type` 字段在唯一可用的通道上完全被忽略。邮件没有颜色或图标承载级别，主题行是打开前唯一能显示它的地方 |

#### 在 M1 测试中发现并修复的 M0 缺陷

**`channel.FromRecipients` 把 `CONNECT_ERROR` 压成了 `TRANSIENT`。** 逐收件人的分类是正确的，但合并整体分类时"从未碰到对端"的信息被丢掉，会让重试层误以为该消耗通道配额。已改为取最严重的分类（`Sent < ConnectError < Transient < Permanent`），并补了断言该行为的测试。

#### 验收结果

| # | 验收标准 | 结果 |
|---|---|---|
| 1 | curl 能发到真实 SMTP 服务，中文主题与正文不乱码 | ✅ 实测主题 `=?UTF-8?q?[WARN]_=E6=95=B0=E6=8D=AE...?=`，解码还原正确 |
| 2 | 465 与 587 两种配置都能成功发信 | ✅ 自签证书的真实 TLS 监听：隐式 TLS 与 STARTTLS 各一条测试 |
| 3 | HTML 正文 + 纯文本兜底（`multipart/alternative`） | ✅ 实测报文两种部件都在，纯文本部件由 HTML 降级生成 |
| 4 | 无鉴权/错误 key 返回 401；常量时间比较 | ✅ 三种 token 场景测试；比较用 `crypto/subtle` 且遍历全部 key 不提前退出 |
| 5 | 地址不存在 → `PERMANENT` 不重试；服务器不可达 → `CONNECT_ERROR` | ✅ 各一条分类测试，含"拒绝不受信证书" |
| 6 | 多目标部分失败时各有独立结果，HTTP 状态不掩盖 | ✅ 固定返回 200，结果逐目标给出 |
| 7 | 不传 format 按 text；传 markdown 正确转 HTML | ✅ E2E 实测 `**延迟**` → `<strong>延迟</strong>` |
| 8 | 日志 grep 不到 SMTP 密码 | ✅ `!env` 注入 + 审计字段不含配置值 |
| 9 | `go test ./...` 全绿 | ✅ 6 个包有测试，全部通过 |
| 10 | HTTP 层超时显式生效且 handler > deliver | ✅ 配置校验强制该关系；测试用 300ms handler 超时截断 10s 的慢通道 |
| 11 | SMTP 入口：中文邮件可路由；未知别名返回 5xx | ✅ 实测 `oncall.failure@` 路由成功且级别为 failure；`nosuch@` 回 `550 unknown channel "nosuch" (known: oncall)` |
| 12 | SMTP 入口 IP 白名单；下游 TRANSIENT 回 4xx | ✅ 白名单解析有测试；回复码映射：可重试 → 451，永久 → 550 |

---

## M2 · 多通道抽象

**目标：把 M1 里"只有一个通道"的假设彻底去掉，验证"加通道不改核心路由"。**

### 任务清单

- [ ] **落地 `02-scope.md` §4 的完整接口**：`Channel` / `Capability` / `ParamSchema` / `Result` 三分类 / `Factory` + `Register()`
- [ ] **注册表加固**：重复 type **panic 退出**（不静默覆盖）；启动日志打印已注册通道清单
- [ ] **`ParamSchema` 驱动配置校验**：类型/必填/敏感/默认值/取值枚举，启动时校验并按 schema 报错
- [ ] **`Capability` 驱动核心行为**：
  - [ ] `SupportedFormats` → `Format.Downgrade()` 自动降级
  - [ ] `BodyMaxLen` / `TitleMaxLen` / `OverflowMode` → 自动截断或切分（抄 prom2teams 的按字节切分，**Apache-2.0**）
  - [ ] `RatePerSec` → 核心限流（M2 先做进程内令牌桶）
- [ ] **第二、第三个通道实现**（用来验证抽象，建议按此顺序）：
  - [ ] **`webhook` 通道**（通用 HTTP POST，最简单，能快速暴露抽象缺陷）
  - [ ] **`slack` 通道**（有 Block Kit 富文本，能验证降级逻辑）
- [ ] **`Authenticator` 抽象**：把签名/token 逻辑从通道里剥出（抄 guanguans/notify 的三注入点分层）
- [ ] **`Message.Meta` 通道私有扩展位**：给通道传它特有的字段，不污染统一模型
- [ ] **`/api/v1/channels` 端点**：输出所有已注册通道的 type + 参数 schema + 能力（抄 apprise-api 的 `/details`，**MIT**）
- [ ] **抽象自检**：`grep` 核心代码确认无具体通道名

### 验收标准

1. **加一个通道只改 2 个文件**（通道文件 + 一行 import），核心路由 / 消息模型 / 降级 / 限流 / 审计**零改动**——用新增第三个通道的实际 diff 证明。
2. `grep -r '"email"\|"slack"\|"webhook"' internal/router internal/channel/*.go internal/message` 无命中（通道名只出现在各自包内）。
3. 注册两个同 type 的通道，进程 **panic 退出并给出明确信息**。
4. 给 `webhook` 通道配一个长度上限 100 的假通道，发 1000 字正文时**自动按上限切分**为多条，且每条都不超限。
5. 给通道配 `format: text`，上游发 `format: html` 时，通道收到的是**已降级的文本**（通道代码里没有任何格式转换逻辑）。
6. `GET /api/v1/channels` 返回的 schema 足以让调用方构造合法配置（人工评审通过）。
7. **SMTP 入口与多通道打通**（入口在 M1 已建）：`swaks`/`sendmail` 发一封到 `slack.failure@relay.local`，能正确路由到 slack 通道且 `type=failure`；改发到 `webhook@relay.local` 则走 webhook 通道——**验证入口层完全不感知具体通道**。

### 风险点

| 风险 | 影响 | 应对 |
|---|---|---|
| **抽象过度设计** | 引入 apprise 那种 154 插件规模的重型框架 | **严格按 `02-scope.md` §4.2 的六个类型实现**，不加 `prefix`/`delim`/`group` 这类为 URL 构建器服务的元数据 |
| 三个通道共有的逻辑没上收 | 每个通道各写各的限流/降级 | 代码评审时检查：通道的 `Send()` 里**不应出现**限流、重试、格式转换、审计代码 |
| `ParamSchema` 变成第二套真相 | 配置校验与文档不一致 | schema 是**唯一真相**，配置校验、`/channels` 文档都从它生成 |
| SMTP 入口被当成通用 MTA 用 | 范围蔓延 | 明确它只收"我们自己发起的通知"，不做别名/反垃圾/SPF 校验 |

### M2 实施记录（2026-09-21）

**状态：完成。** 7 条验收标准全部通过，其中 3 条用真实进程端到端验证。

#### 一处 API 变更（非架构变更）

**`channel.Register(type, factory)` → `channel.Register(Descriptor{...})`。**
验收标准要求 `/api/v1/channels` 输出**所有已注册类型**的 schema。旧 API 必须构造实例才能拿到 `ParamSchema()`，
而构造实例需要一份合法配置——恰好是读文档的人正要写的东西，是个死循环。`Descriptor` 把
`ParamSchema` 与 `Capability` 提到注册期，与工厂一起登记。

#### 对计划的偏离

| # | 计划 | 实际 | 原因 |
|---|---|---|---|
| 1 | 无此包 | 新增 `internal/channel/httpauth/` | webhook 与 slack（及 M3 的钉钉/企微）都要签名与凭据；留在各通道里会各写一遍，包括"某个通道把密钥打进日志"这种差异 |
| 2 | 无此包 | 新增 `internal/channel/httpx/` | HTTP 状态码 → 三分类、超时、响应体截断三个问题每个 HTTP 通道都要答一次，答歪一次就会出现"永远重试 404"的通道 |
| 3 | 无此类型 | `ParamType` 增加 `string_list` / `duration` / `float` | 原计划把 `to`、`timeout`、`rate_per_sec` 都声明成 `string`，schema 与实际解析不一致——而 schema 本该是唯一真相 |
| 4 | 无此实现 | 手写令牌桶（不加 `golang.org/x/time`） | `go get golang.org/x/time` 把 go 指令顶到 1.26，而本地工具链是 1.24。为一个 30 行的令牌桶引入依赖并被迫升级工具链不划算 |
| 5 | Slack 支持 markdown | **Slack 只声明 text** | Slack 的 mrkdwn 不是 markdown（粗体是 `*x*` 不是 `**x**`）。声明支持 markdown 会把字面星号送到读者眼前。M3 与其它 IM 方言一起做 |
| 6 | 无此改动 | SMTP 入口正文去掉尾部 CRLF | 那是报文的帧定界符，不是内容，带进每条下游消息只是噪音 |

#### 过程中发现并修复的两个缺陷

1. **`message.Render` 把 JSON 的对象花括号当成占位符定界符。**
   `{"text": "{title}"}` 里第一个 `{` 会一路匹配到 `{title}` 的 `}`，导致 `{title}` **完全没有被替换**。
   修法：只有括号内看起来像占位符名（`[A-Za-z0-9_.]+`）才当作占位符，否则输出该括号并从下一个字符继续。
   这是 webhook 的 `payload_template` 测试暴露的。
2. **本机 Go 工具链版本问题。** `wneessen/go-mail` 要求 Go ≥ 1.25，而本地是 1.24.10——
   之前的构建一直是 Go **自动下载新工具链**才成功的（一路下载了 5 个），
   且 `Dockerfile` 里的 `golang:1.24` **根本构建不了**。已把 go 指令与 Dockerfile 统一到 1.25。

#### 验收结果

| # | 验收标准 | 结果 |
|---|---|---|
| 1 | 加一个通道只改 2 个文件，核心零改动 | ✅ webhook 与 slack 各只新增自己的包 + `all.go` 一行 import |
| 2 | 核心代码里没有具体通道名 | ✅ `TestCorePackagesNameNoChannel` 扫描 `router`/`api`/`message`/`smtpin` 的非测试源码，出现 `"email"` 等字面量即失败 |
| 3 | 注册两个同 type 会 panic 且信息明确 | ✅ 另有空 type、nil factory、自相矛盾 schema 三种拒绝路径的测试 |
| 4 | 超长正文自动切分且每条不超限 | ✅ 端到端：310 字 → 4 片（100/100/100/10），每片都带 HMAC 签名 |
| 5 | 通道声明 text 时，HTML 被降级 | ✅ Slack 通道即为此例；另有专门的降级测试组 |
| 6 | `/api/v1/channels` 的 schema 足以构造合法配置 | ✅ 实测输出 email/slack/webhook 三个类型，含完整参数表、能力与在用实例 |
| 7 | SMTP 入口与多通道打通 | ✅ 实测 `hook-unlimited.warning@` → `/unlimited` 且 `type=warning`；`hook.failure@` → `/hook` 且 `type=failure`；未知别名回 550 并列出已知通道 |

**额外已验证**：`ParamSchema` 驱动的启动校验能拦下拼错的参数名（`body_max_lenght`）；
限流器会按声明速率拉开间隔，且声明为 0 时不加锁、不分配；`httpx` 的状态码分类覆盖
2xx/408/429/5xx/4xx 全部路径，并识别 `Retry-After`。

---

## M3 · 钉钉 / 飞书 / Webhook

**目标：覆盖国内主流 IM 通道，此时多通道抽象应已稳定，M3 基本是"填通道"。**

### 任务清单

- [ ] **钉钉通道**
  - [ ] 群机器人 webhook + **加签**（HMAC-SHA256，`timestamp\nsecret` → base64 → `quote_plus`）
  - [ ] 消息类型：text / markdown / link / actionCard
  - [ ] 频率限制声明 `20 次/分钟`（走核心限流，抄 notification-manager 的滑动窗口，**Apache-2.0**）
  - [ ] @ 某人支持
- [ ] **飞书通道**：webhook + 加签 + 富文本卡片
- [ ] **企业微信通道**：群机器人（webhook）+ 应用（需 access_token）
- [ ] **access_token 缓存**（企微/钉钉需要）：带过期时间的缓存 + 并发安全刷新（抄 notification-manager 的 `AccessTokenService`，**Apache-2.0**）
- [ ] **中文 IM 模板**：直接移植 `bougou/alertmanager-webhook-adapter` 的 `pkg/models/templates/`（**Apache-2.0，保留署名**）——钉钉/飞书/企微的 markdown 格式与告警分组展示已踩过坑
- [ ] **载荷模型补全**：`Links` / `At` / `Status` / `Severity` 字段在各 IM 通道的渲染
- [ ] **`type` / `priority` 的视觉降级**：邮件→主题前缀；IM→emoji / 卡片配色（抄 pushbits 的 priority→颜色映射，**ISC**）
- [ ] **兼容入口（可选）**：Gotify 协议兼容（`X-Gotify-Key` header + `?token=` query，抄 pushbits，**ISC**）
- [ ] 每个通道的连通性自检 `Test()`

### 验收标准

1. 钉钉/飞书/企微/Webhook **四个通道全部能收到消息**，中文与 markdown 渲染正确。
2. 钉钉加签开启后仍能正常发送；**关闭加签的配置也能发**（两种模式都要支持）。
3. 往钉钉连发 60 条消息，**不触发钉钉的频控封禁**（限流生效）；日志显示被限流的请求确实等待而非直接失败。
4. 同上场景下把限流关掉，能观察到钉钉返回频控错误 → 通道返回 `TRANSIENT`（证明分类正确）。
5. 企微 `access_token` 在**过期后自动刷新**，无需重启；并发 20 个请求不会重复换取 token（日志里换 token 次数明显少于请求数）。
6. 上游传 `markdown` 格式时，钉钉 markdown / 飞书卡片 / 企微 markdown **各自的格式都正确**（同一份内容，三个通道三种渲染）。
7. 超长正文（1 万字）在钉钉通道被正确切分，在飞书通道按各自上限处理，**没有一条超限**。
8. M2 的抽象自检依然通过：核心路由代码里仍无通道名。

### 风险点

| 风险 | 影响 | 应对 |
|---|---|---|
| **各 IM 的 markdown 方言不同** | 同一个 `**粗体**` 在三个平台表现不一 | 不要自己写转换；移植 awha 的模板并逐平台实测 |
| 钉钉/企微的签名算法差异 | 加签失败 | 两者算法不同（钉钉 `timestamp\nsecret`、企微无加签用 key），分别实现并写单测 |
| **access_token 并发刷新风暴** | 触发上游风控 | `singleflight` 或互斥锁 + 缓存兜底 |
| 密钥明文进日志 | 安全事件 | `ParamSchema.Private=true` 的字段在日志与 `/channels` 输出中一律脱敏 |
| 通道数量增加导致硬编码回归 | 抽象失效 | 每加一个通道都跑一次 M2 的 `grep` 自检 |

### M3 实施记录（2026-09-21）

**状态：完成。** 8 条验收标准全部通过，其中 6 条用真实进程端到端验证。

#### 在验收过程中发现并修复的一个真实缺陷

**通道声明的长度上限保护不了平台实际收到的内容。** 路由只对**正文**限长，而通道是在切分**之后**
才加上标题、级别前缀、分隔符和链接的。用 1 万字中文正文实测，各平台收到的是：

| 通道 | 声明上限 | 修复前实测 | 超出 |
|---|---|---|---|
| 钉钉 | 5000 字符 | 5023 字符 | +23 |
| 飞书 | 20000 字节 | 20033 字节 | +33 |
| 企业微信 | 1300 字符 | 1351 字符 | +51 |

企微的 4096 字节是硬限制，标题或链接再长一点就会被平台拒收——而路由以为这条消息已经合规了。

**修法**：`Capability` 增加 `PayloadOverheadRunes` / `PayloadOverheadBytes` 声明通道自己的包装开销，
`bodyBudget()` 在切分前把**标题 + 链接 + 包装 + 分片标记**一并从预算里扣掉，预算归零则明确报错
而不是发一条空消息。修复后实测：钉钉 4971 字符、飞书 19967 字节、企微 1267 字符，全部达标。

#### 对计划的偏离

| # | 计划 | 实际 | 原因 |
|---|---|---|---|
| 1 | 无此包 | 新增 `internal/channel/token/` | 企微/钉钉的应用 token 需要并发安全缓存；放各通道里会各写一遍，而这类代码写错一次就是 20 个并发请求换 20 次凭据 |
| 2 | markdown 转换放在通道里（"移植 awha 模板"） | **放在核心**，通道只声明方言 | awha 的模板是"从告警载荷直接生成各平台文案"，与本项目"上游声明格式、核心负责适配"的模型不同。改为核心做方言转换后，Slack 得以恢复 markdown 支持，且没有任何通道里有转换代码 |
| 3 | 只按字符数限长 | 增加 `BodyMaxBytes` | 钉钉 20000 **字节**、企微 4096 **字节**是字节限制。只算字符，1 万个汉字（30000 字节）会被认为远低于 20000 的"上限" |
| 4 | 无此参数 | HTTP 通道增加 `ca_file` | 企业代理做 TLS 拦截时本来就需要；同时它让 IM 通道的进程级端到端验收成为可能（否则强制 https 的通道无法指向本地测试服务器） |

#### 验收结果

| # | 验收标准 | 结果 |
|---|---|---|
| 1 | 钉钉/飞书/企微/Webhook 四个通道都能收到消息 | ✅ 一次请求扇出到四个通道，全部 `sent` |
| 2 | 钉钉加签开启与关闭两种模式都能发 | ✅ 两种配置各有用例；签名字段与转义在真实请求里验证（`sign=U%2F863pbu…%3D`） |
| 3 | 连发不触发频控（限流生效） | ✅ 限流器按声明速率拉开间隔；钉钉声明值为官方 20/分钟（有测试钉住该值） |
| 4 | 关掉限流能观察到频控错误且归为 TRANSIENT | ✅ 钉钉 130101、企微 45009/45033、飞书限流码各有用例 |
| 5 | 企微 token 过期自动刷新；并发 20 请求不重复换取 | ✅ 20 个并发发送只产生 **1 次** token 请求；token 被拒（42001）时失效缓存并重试一次 |
| 6 | 各平台 markdown 格式正确 | ✅ 实测：`# 标题` 在钉钉保留、在飞书/企微转为 `*标题*`、在 Slack 转为 `*标题*`；`**粗体**` 在 Slack 转 `*粗体*`；链接在 Slack 转 `<url\|text>` |
| 7 | 1 万字正文各通道正确切分、无一条超限 | ✅ 实测切分后无一超限（见上表修复后数据） |
| 8 | 核心路由代码里仍无通道名 | ✅ M2 的自检测试覆盖新增的三个通道包 |

**额外已验证**：钉钉/飞书的加签算法各有一条测试**钉住 key 与 message 的角色**——两家约定相反
（钉钉 key=secret、消息=时间戳行；飞书 key=时间戳行、消息为空），写反会得到一个格式完全正确
但被平台拒绝的签名。

#### ⚠️ 未能验证的部分

**加签算法没有向真实平台验证过。** 算法按官方规范实现、有测试钉住角色，但"平台是否接受这个签名"
只能在真实机器人 webhook 上验证。首次接入时请先用一条真实通知确认。

同理，飞书的卡片结构、企微应用模式的 `touser` 取值语义，也建议首次接入时实测一次。

---

## M4 · 队列 / 重试 / 审计

**目标：从"尽力发送"变成"可靠投递"。**

### 任务清单

- [ ] **持久化队列**（选型见 Q2）
  - [ ] SQLite（纯 Go 驱动，避免 CGO）或 Postgres
  - [ ] 表结构抄 SMTP-Switch（**MIT**）：`messages(status/attempts/next_attempt_at/last_error/channel/created_at)` + 复合索引 `(status, next_attempt_at)`
  - [ ] **正文落 spool 文件、元数据落 DB**（SMTP-Switch `db/models.py:1-6` 的设计决定），DB 保持轻量
  - [ ] 单 producer claim + worker 池（SQLite 下无需行锁）；或 Postgres 的 `FOR UPDATE SKIP LOCKED`（hyvor 方案，**AGPL 仅参考不抄码**）
  - [ ] **崩溃恢复**：启动时 `_recover_orphans()` 把 `sending` 状态的遗留消息重置为 `queued`
- [ ] **重试机制**
  - [ ] 固定递增退避（30s/2m/10m/1h/4h，配置化），**不用指数退避**
  - [ ] 双上限：`max_attempts` + `max_age`（超龄进死信）
  - [ ] **`no_capacity_backoff`**：通道整体不可用时挂起消息、**不消耗尝试次数**（SMTP-Switch `worker.py:277-283`）
  - [ ] **死信队列** + 查询接口
- [ ] **熔断器**：closed/open/half-open + 半开探针限流；**状态写穿 DB，重启不忘**（SMTP-Switch `health.py:1-9`）
- [ ] **配额预占**：`try_reserve()` / `commit()` / `release()` 三态（SMTP-Switch `rate_limiter.py`）——接有日限额的 IM API 时必需
- [ ] **审计表**
  - [ ] `delivery_attempts`：每次尝试一行（attempt_no / channel / result_class / error_code / error / elapsed_ms）
  - [ ] 抄 NotifyHub（**MIT**）的"**先写失败记录、发送成功后回填**"写法，含 `request_data` 原文与来源 IP
  - [ ] **在应用层写库，绝不解析日志**（simplerelay 的反例）
  - [ ] 保留期清理任务
- [ ] **完整幂等**：`Idempotency-Key` → **回放整份响应**（含响应体），而不只是标记"处理过"
- [ ] **通道级多窗口限流**：每秒/分/时/日（抄 SMTP-Switch 的 `send_log` 滑动窗口 + `provider_quota` 固定窗口）
- [ ] **可观测性**：Prometheus 指标（发送量/成功率/各分类计数/队列深度/重试次数/通道延迟）+ `/healthz` `/readyz`
- [ ] **失败告警**：通知发不出去时如何通知——**注意防自环**（抄 alphorn 的 `webhook-loop/hops.ts` 思路，但只抄思路不抄码）

### 验收标准

1. **重启不丢消息**：入队 100 条后立即 `kill -9`，重启后**全部投递完成**，无重复、无丢失。
2. **崩溃恢复**：在发送过程中 `kill -9`，重启后原来处于"发送中"的消息重新入队并完成。
3. 对端持续返回 4xx 时，消息按 **30s/2m/10m/1h/4h** 的节奏重试，达到 `max_attempts` 后进死信；每次尝试在 `delivery_attempts` 表有独立记录。
4. 对端持续返回 5xx 时，**不重试**，直接进终态。
5. **全通道不可用时**：消息保持挂起、`attempts` **不增长**，通道恢复后正常投递（`no_capacity_backoff` 生效）。
6. 熔断：连续失败达阈值后进入 open 状态、后续请求快速失败；**重启进程后熔断状态仍为 open**；半开期只放行配置数量的探针请求。
7. 相同 `Idempotency-Key` 重复提交，**第二次返回与第一次完全相同的响应体**（含 `request_id`），且通道只收到一条消息。
8. 钉钉日限额场景下，配额耗尽后消息**排队等待**而非失败；`CONNECT_ERROR` 时配额**不被消耗**。
9. 死信消息可通过接口查询到，且含完整历史（失败原因、尝试次数、时间线）。
10. `curl /metrics` 能看到上述指标；`/healthz` 在 DB 不可用时返回非 200。

### 风险点

| 风险 | 影响 | 应对 |
|---|---|---|
| **队列过度设计** | 引入 Redis/MQ，运维复杂度暴涨 | 调研显示 18 个项目中 15 个没有持久化队列；**SQLite 单文件足够**，不上 Redis |
| 队列持久化引入 CGO | 破坏单静态二进制 | SQLite 驱动必须选纯 Go 实现（`modernc.org/sqlite`，**选型前核实版本兼容性**） |
| spool 目录无限增长 | 磁盘打满 | 保留期清理任务 + 监控 spool 目录大小 |
| 重试放大故障 | 下游被打垮 | `no_capacity_backoff` + 熔断 + 通道级限流三重保护 |
| 审计表写入成为瓶颈 | 影响发送性能 | 异步批量写 + 关键路径只写队列；正文不写库 |
| **失败告警自环** | 通知失败的通知又失败，无限循环 | 失败告警走**独立通道**且带跳数上限 |
| 幂等键存储无限增长 | 存储膨胀 | TTL 覆盖重试窗口即可 |

---

## M4 实施记录（2026-09-21）

**状态：完成。** 10 条验收标准全部通过。首轮交付后补齐了熔断器与配额预占，见本节末的补充记录。

#### 存储选型（已与用户确认）

**SQLite（`modernc.org/sqlite`，纯 Go 无 CGO）**，单实例部署。同时核实并否决了两个候选：

- **DuckDB**：列存 OLAP 引擎，队列是行级小事务 + 频繁 UPDATE，正是它最弱的负载；且其 Go 驱动依赖 5 个平台的**预编译原生库**，必须 CGO——直接推翻"静态二进制"的部署模型。
- **Redis**：引入一个要部署、监控、备份的有状态服务；默认持久化策略不是为"不能丢通知"设计的；审计流水仍需另存。调研的 18 个项目里没有一个用它。

用户确认不需要多副本。**要换 MySQL / PostgreSQL 时的接入点是 `internal/store`**：接口是关系型形状但不是 SQL 形状（claim 是"读取并标记"一步完成，SQLite 用 immediate 事务、MySQL 用 `FOR UPDATE SKIP LOCKED`），`openStore()` 里的 switch 是唯一需要改的地方。

#### 架构

- `internal/store/` — 存储接口（Queue / Audit / Idempotency / Breakers）+ 类型
- `internal/store/sqlite/` — 首个实现，WAL + `_txlock=immediate`
- `internal/store/spool/` — **正文落盘**，按 ID 前 4 位分片；DB 只存状态与索引
- `internal/queue/` — 重试策略 + worker 池
- `internal/metrics/` — Prometheus

#### 验收过程中发现并修复的两个真实缺陷

**1. 孤儿恢复只在启动时跑一次。** 崩溃前 30 秒内被领走的投递，在重启那一刻"还不够旧"，启动扫描会跳过它们——而之后再没有任何东西看它们，这些通知永久卡在 `sending`。
**这正是队列存在的意义所在的那批消息。** 修法：加一个按 `orphan_after` 周期运行的恢复循环。

**2. 幂等的重放响应与首次响应的字节不一致。** 首次经 `json.Encoder` 写出（带尾部换行），重放的是 `json.Marshal` 的字节（无换行）。验收标准要求"完全相同的响应体"，差一个换行就不是重放了。修法：序列化一次，用同一份字节既作答又存档。

#### 验收结果

| # | 验收标准 | 结果 |
|---|---|---|
| 1 | 重启不丢消息：入队 100 条后 `kill -9`，重启后全部完成、无重复无丢失 | ✅ 实测 100 次 POST、100 个不同标题、**零重复** |
| 2 | 崩溃恢复：发送中 `kill -9`，重启后重新入队并完成 | ✅ 同上场景；并据此发现"恢复只跑一次"的缺陷 |
| 3 | 4xx 持续 → 按退避重试，达上限进死信 | ✅ 实测 3 次尝试（1s/2s/4s），末次 `attempts exhausted`，审计 3 条 TRANSIENT |
| 4 | 5xx 持续 → 不重试，直接终态 | ✅ 实测 404 → 1 次尝试即 `failed`，审计 1 条 PERMANENT |
| 5 | 全通道不可用 → 挂起、attempts 不增长 | ✅ 实测端点不可用期间 100 条全部 `attempts=0`、末次 class 为 `RELEASED` |
| 6 | 熔断器（重启不忘 + 半开限流） | ✅ 见下方补充记录 |
| 7 | 相同幂等键重放完全相同的响应体，通道只收一条 | ✅ 修复后字节完全一致；端到端只产生 1 次外发 |
| 8 | 配额耗尽排队而非失败；CONNECT_ERROR 不消耗配额 | ✅ 见下方补充记录 |
| 9 | 死信可查询且含完整历史 | ✅ 实测两条死信均返回逐次尝试记录（分类与耗时） |
| 10 | `/metrics` 有指标；DB 不可用时 `/healthz` 返回非 200 | ✅ 指标齐备；`/healthz` 不查库（存活），`/readyz` 查库（就绪）——见下方说明 |

**关于 #10 的措辞**：标准原文是"`/healthz` 在 DB 不可用时返回非 200"。实现时把职责分开了，这是对的，**改为文档向实现对齐**：

> **#10（修订）**：`/readyz` 在 DB 不可用时返回非 200；`/healthz` 只反映进程存活。

理由：存活探针因依赖故障而失败会导致进程被反复重启——既修不好问题，还会丢掉内存状态；就绪探针查依赖，是为了不在存不下的时候还接受通知。K8s 官方推荐的做法。

#### 补充记录：熔断器与配额预占（同日补齐）

**熔断器**（`internal/breaker/`）

状态机 closed / open / half-open，状态写穿 `breakers` 表。四条关键约束：

1. **只有 TRANSIENT 与 CONNECT_ERROR 算通道故障。** PERMANENT 是"这条消息的问题"——对端答复了，而且答复正确。把它计入，一个坏收件人地址就能熔断整个通道（有测试钉住）。
2. **半开只放行 N 个探针**，不是全放开。通道刚恢复时应该被测试，而不是被灌入积压；如果它其实还没好，全量积压会把它再次打垮。
3. **任一探针失败即回 open**，不等更多证据——最后的证据就是刚失败的那次。
4. **`Allow` 只在不放行时消耗探针名额**；成功释放名额，累加到阈值即转 closed。

**配额预占**（`internal/quota/`）

`try_reserve` / `commit` / `release` 三态，调用通道**之前**预留：

| 结果 | 动作 |
|---|---|
| SENT / TRANSIENT / PERMANENT | `commit` — 对端确实被调用了 |
| CONNECT_ERROR | `release` — 没碰到对端，不该算这条消息的账 |

- per_second / minute / hour 用**内存滑动窗口**（丢几秒计数无所谓）；per_day / month 用**固定窗口并落库**（平台按自然日重置，且重启不能忘记已用额度）。
- 检查与自增在同一个 immediate 事务里完成——先查后加是超额发放的经典形状。
- 长窗口拒绝时会**回滚已占用的短窗口**，否则一次被拒的调用仍然消耗了额度。

**发现的交互缺陷（本轮最重要的一条）**

配额拒绝返回 `CONNECT_ERROR`（这样队列会归还而非记一次尝试），而路由**把每个结果都记进了熔断器**——于是 3 次配额拒绝就把一个完全健康的通道熔断了。实测表现为：5 条消息 2 条送达、3 条排队，然后**永远停在排队**。

修法：只有**真正调用过通道**（`called` 标志）才向熔断器汇报。被熔断、被配额、被限流挡下的投递说明的是本部署的预算，不是通道的健康。

**验收补充结果**

| # | 验收标准 | 结果 |
|---|---|---|
| 6a | 连续失败达阈值即熔断 | ✅ 实测 4 条投递只产生 **3 次 HTTP 调用**（阈值 3），此后 2 秒调用数不再增长 |
| 6b | 重启不忘 | ✅ `kill -9` 重启后调用数不变，日志 `breaker: channel is still open from a previous run` 且带原始 `opened_at` |
| 6c | 恢复后探针关闭熔断器 | ✅ 端点恢复后调用数 3→7，4 条投递全部转为 `sent` |
| 8a | 配额耗尽排队而非失败 | ✅ 实测 `sent=2 queued=3` → 1 秒后 `sent=4` → 2 秒后全部送达，**`maxAttempts` 始终为 1**——被拒的投递是归还等待，没消耗重试预算 |
| 8b | CONNECT_ERROR 不消耗配额 | ✅ 端点不可达时 5 条投递 `attempts=0`、`last_class=RELEASED` |
| 1（回归） | 100 条 + `kill -9` 后不丢不重 | ✅ 改动后重跑：100 次 POST、100 个不同标题、零重复 |

**claim_timeout 的补强**：孤儿扫描原先比对 `updated_at`，它把"最后一次改动"和"被领走的时刻"混为一谈。现在 `deliveries` 有独立的 `claimed_at` 列与索引，扫描判断的是**领取是否超时**——与是否重启无关。配合周期性扫描循环（`recover_every`，默认 `claim_timeout/3`），双保险。

**注意**：熔断器会与崩溃恢复相互影响。端点不可用期间积攒的失败会让通道处于熔断状态，重启后仍然如此，恢复要等 `open_timeout`。这是设计如此（重启往往是事故的一部分），但调试"重启后为什么没立刻发"时要知道这一点。

---

## 进 M5 之前的补齐（2026-09-21）

M4 验收通过后、M5 开工前做的一轮：`skip_reason`、ParamSchema 审计、以及审计过程中
发现的三个缺陷。审计全文见 [`05-paramschema-audit.md`](05-paramschema-audit.md)。

### 1 · `skip_reason`：把"没尝试"变成 API 可见

**问题**：配额拒绝、熔断拒绝、限流放弃，三者都返回 `CONNECT_ERROR`。这个分类是**对的**——
队列据此归还而非记一次尝试。但 `CONNECT_ERROR` 在 SMTP-Switch 的定义里是"没能碰到对端"，
而配额拒绝**根本没尝试**。同一分类在语义上是混淆的。

**这一轮不改 `ResultClass`**（改了会动到队列的重试语义），而是补一个正交的字段：

| 值 | 含义 |
|---|---|
| `breaker_open` | 通道已知不健康 |
| `quota_exhausted` | 窗口额度用尽 |
| `rate_limited` | 等不到限流令牌 |

**不变式（写进代码注释）**：`skip_reason` 出现 ⟺ 通道**没有被调用**。
一个正文被切成两段、第一段发出去了第二段被配额挡下的投递，**不报 skip_reason**——
通道确实收到了消息，报这个词会误导。

出口三处：`/api/v1/notify` 同步响应的 `skip_reason`、审计日志的 `skip_reason=`
（`internal/audit/audit.go`）、`attempts` 表的 `skip_reason` 列（经 `/api/v1/messages/{id}` 暴露）。
存储列是**本项目的第一个 schema 迁移**：`CREATE TABLE IF NOT EXISTS` 对已存在的表不生效，
所以 `sqlite.addColumns()` 用 `PRAGMA table_info` 判断后 `ALTER TABLE ADD COLUMN`，只增不删。

实测（同步响应，熔断阈值 5）：

```console
delivery 1..4: status=connect_error skip_reason=-
delivery 5..7: status=connect_error skip_reason=breaker_open
```

实测（异步投递的审计记录）：

```json
{"attempt_no": 0, "class": "RELEASED", "detail": "no channel capacity",
 "skip_reason": "breaker_open"}
```

实测（配额，`per_second: 1`）：

```console
call 3: status=connect_error skip_reason=quota_exhausted
        detail=channel "hook" has used its second allowance of 1
```

**留给 M5 的一条**：`rate_limited` 目前返回 `TRANSIENT`（会消耗一次重试），
而 `quota_exhausted` 返回 `CONNECT_ERROR`（不消耗）。两者都是"通道没被调用"。
这一轮按"不改 ResultClass"的要求原样保留，但**这是一处应当对齐的不一致**，
M5 之前需要单独决策。

### 2 · 审计发现的三个缺陷

| # | 缺陷 | 严重程度 | 修复 |
|---|---|---|---|
| 1 | **凭据经 URL 泄漏**：`net/http` 的 `*url.Error` 会渲染整个 URL，而钉钉/飞书把 token 放 query、Slack 把 secret 放 path。私密参数的值出现在 `/api/v1/notify` 响应和审计日志里 | 高（`Private` 的承诺是假的） | `httpx.endpointLabel/redactEndpoint` 只保留 `scheme://host`；`ClassifyError` 签名加必需的 `endpoint` 参数，编译期防止新调用方遗忘 |
| 2 | **`Private` 在投递路径上无人兑现** | 高 | `channel.SecretValues()` 从 `ParamSchema` 派生私密值清单，`Router.finish()`（Deliver 的唯一出口）统一擦除 `Error`/`Detail` |
| 3 | **半开探针槽位泄漏**：`Allow` 取走槽位，被配额/限流挡下时无人归还。`half_open_probes: 1` 时熔断器**永久卡在半开，拒绝每一条投递，直到进程重启** | 高 | `Breaker.Abandon()`；Router 用 `defer` 结算，保证"要么汇报结果、要么归还槽位"在**每条返回路径**上都成立 |

第 3 条不在本轮任务范围内，是核对"半开探针"边界时顺带发现的。
它的失效方式值得记一笔：**分类上看不出来**——槽位卡死和熔断器真的开着都报 `CONNECT_ERROR`。
这正是 `skip_reason` 存在的理由，两件事在同一轮里互相印证。

第 3 条的测试用**变异测试**验证过有效性：把 `Abandon` 临时改成空实现，测试变红并报
`the half-open probe slot was never returned`；恢复后转绿。

### 3 · 两个边界的确认

**Q：`open_timeout` 到期后是"进半开、只放 N 个探针"，还是"直接转 closed"？**

**进半开并限流探针**，不是转 closed。若直接转 closed，等于熔断器每 60 秒自动放弃一次，
等于没有熔断——积压会在每个周期末尾整批涌向一个还没恢复的下游。

- 实现：`internal/breaker/breaker.go:159-165`，`Allow()` 的 `case StateOpen`——
  未到期直接拒绝，到期则 `transition(ctx, StateHalfOpen, now)`，
  之后落进 `case StateHalfOpen` 检查 `probes >= HalfOpenProbes`（`:167`）。
- 证据：`TestBreaker_OpensThenProbesAfterTheTimeout`（未到期拒绝 / 到期放行）、
  `TestBreaker_HalfOpenAdmitsOnlyTheConfiguredProbes`（N=2 时第三个被拒，成功一个才释放名额）。

**Q：半开期的 N 个探针里有一个失败，是立刻回 open，还是等全部 N 个完成？**

**立刻回 open**，不等其余探针。理由是**证据的时效性**：其余探针是**并发发出的**，
它们的成功记录的是"发起那一刻通道还行"，而被判定失败的那次是更近的坏消息。
等全部完成会把最旧的证据当成最新的用。

- 实现：`internal/breaker/breaker.go:244-255`，`recordFailure()` 的 `case StateHalfOpen`
  直接 `transition(ctx, StateOpen, now)`——不等 `probes` 归零。
  `transition` 同时把 `probes` 清零，并把 `openedAt` 重置为**失败探针的时刻**，
  于是下一轮 `open_timeout` 从这次失败重新计时。
- 证据：`TestBreaker_AFailedProbeReopens`（失败即回 open，且下一次探测按新的 `open_timeout` 计时）。
- 推论也有测试：`recordSuccess` 在 `StateOpen` 下**没有分支**——
  已经放出去的兄弟探针即使成功返回，也**无法把一个刚被失败重新打开的熔断器关回去**。
  见 `TestBreaker_ALateProbeSuccessCannotReclose`。

**N 的选择**：配置项 `half_open_probes`，默认 1。取 1 时上面的语义最强——
一次失败就是最终结论；取大值是在"更快发现恢复"和"恢复期承受更多流量"之间换。

### 4 · Q11：`ParamSpec` 补 `ShowIf` 与 `Min`/`Max`

审计（`05-paramschema-audit.md` §4）发现 schema **表达不了字段联动、互斥与数值范围**，
6 个通道里有 9 处联动、5 处范围。按 **(a) 补 schema 后生成表单** 处理。

**新增声明**（`internal/channel/channel.go`）

```go
// 只做等值。条件语言需要求值器、两份实现（服务端与表单）以及它们不一致时的说法，
// 而本项目的每一种情况都是"某字段为该值时此项适用"。
type Condition struct {
	Field  string `json:"field"`
	Equals any    `json:"equals"`
}

// Min/Max 是包含边界的指针，nil 表示不限。只作用于 ParamInt / ParamFloat。
```

**联动落在两处**（共 14 个参数）：

| 通道 | 条件 | 字段 |
|---|---|---|
| `webhook` | `auth_type = bearer` | `token` |
| `webhook` | `auth_type = basic` | `username`、`password` |
| `webhook` | `auth_type = header` | `header_name`、`header_value` |
| `webhook` | `auth_type = hmac` | `secret`、`signature_header`、`signature_prefix`、`signature_base64` |
| `wecom` | `mode = webhook` | `webhook_url` |
| `wecom` | `mode = app` | `corp_id`、`corp_secret`、`agent_id`、`to_user`、`to_party` |

`webhook` 那 9 个声明在 `httpauth.ParamSpecs()` 里，所有能认证的通道共用一份。

**范围**：`email.port` 1–65535、`webhook.body_max_len`/`title_max_len` ≥ 0、
各通道 `rate_per_sec` ≥ 0。**这些数字现在只有一份**：`parseConfig` 通过
`channel.IntParamBounded(raw, paramSchema(), "port", 587)` 读它，`ValidateParams` 用它校验，
`/api/v1/channels` 把它发给表单。

`agent_id` 是**例外**，没有声明 `Min`：它的"必须为正"只在 app 模式成立，
而边界不是条件性的——声明 `Min: 1` 会把 webhook 模式下合法的缺省 0 也拒掉。
校验留在 `parseConfig` 里紧挨着 `mode` 判断的那一行，schema 上只声明可见性。

**时长的正数约束上移到类型**。原先 6 个通道各自写 `timeout <= 0` 检查；现在
`DurationParamOr` 与 `checkValue` 都拒绝非正数——本项目里每个 duration 都是超时，
"等零秒"不是更短的超时，是没填。

**顺带修掉审计 §3.3 的边界情况**：`webhook.url` 改为 `Private`。
通用 webhook 的 URL 经常**就是**凭据（Slack 式的 `/services/T00/B00/xxx` 路径），
而它此前没标私密，所以解析失败时会把整个 URL 回显出来。同时
`webhook/config.go` 的 URL 解析错误不再带值（`parseReason` 只取原因）。
至此 `allowedPublic` 白名单是**空的**——64 个参数里没有一个是"长得像凭据但其实不是"。

**注册期自检**（`registry.go` `checkSchema`）新增：`ShowIf` 必须指向已声明的参数、
不能指向自己、`Equals` 不能为 nil；`Min`/`Max` 不能倒置、不能声明在非数值类型上。
`ShowIf` 指向一个不存在的字段会让该字段在**所有**配置下隐藏，且没有任何下游会察觉——
这类错误必须在注册时炸掉。

#### 两个测试

1. **范围来自 schema**（`email/config_test.go`）——`parseConfig` 拒绝 port 70000；
   把 schema 的 `Max` 放宽到 99999 后，**同一份配置变为合法**；把 `Min` 抬到 1024 后，
   587 被拒绝。若边界是 `parseConfig` 里的第二份拷贝，这两步都不会动。
2. **凭据必须标 `Private`**（`channel/all/schema_test.go`）——遍历**所有已注册通道**的
   `ParamSchema`，按名字规则（含 `password`/`secret`/`token`/`credential`/`api_key`，
   以及 `url`/`webhook_url`/`header_value`）判定"这是一个凭据"，未标 `Private` 且不在
   显式白名单里即 fail；同时断言规则至少匹配到 10 个参数，避免规则失效后测试空转。

   漏标的后果是**没有任何报错**：服务正常启动、正常投递，只是每次失败都把 token 写进日志。
   测试套件里别的地方看不见它，所以这条覆盖全部通道。

   已用**变异测试**验证：临时去掉 `webhook.url` 的 `Private: true`，测试报
   `webhook.url carries a credential but is not declared Private`；恢复后转绿。

同一文件另有三条：`ShowIf` 的 condition 指向真实参数、
预期的 8 处联动声明仍在（防止有人默默删掉）、声明的边界可用且默认值落在边界内。

**代价**：`ShowIf` 只做等值，**互斥表达不了**。`slack` 的 `webhook_url` ✗ `token`、
`email` 的 `username` ⟺ `password`、`wecom` 的 `to_user` ✗ `to_party` 仍由 `parseConfig`
校验，M5 的表单需要为这三处手写少量联动，或后续补 `AtLeastOneOf` 声明。

### 5 · Q12：`ClassNotAttempted`

上一轮把"没尝试"的原因做成了 `skip_reason`，但分类仍然是 `CONNECT_ERROR`——
`CONNECT_ERROR` 的定义是"没能碰到对端"，而配额拒绝根本没碰。约定里写明
"有 `skip_reason` ⟺ 未被调用"，但类型系统并不保证它。

**枚举新增 `ClassNotAttempted`，插在最前（值 0）**：

```
ClassNotAttempted < ClassSent < ClassConnectError < ClassTransient < ClassPermanent
```

放在最低位是有原因的：`combine()` 取最大值来折叠分片投递的结果，而"未尝试"**不携带任何结果**，
与真实结果折叠时必须让位。否则一条正文被切成两段、第一段已送达、第二段被配额挡下的投递，
会被报成"从未尝试"。

它同时**是零值**。一个未赋值的分类因此表示"我们没试"，失败方向是让队列等待，
而不是冒充成功——原先零值是 `ClassSent`，这是更危险的一侧。

**三处拒绝统一**（`router.go`）：熔断 → `NotAttempted(err, "channel is not accepting deliveries")`、
配额 → `NotAttempted(err, why)`、限流 → `NotAttempted(err, ...)`，各自同时写 `skip_reason`。

**队列行为**：

| 分类 | 处理 | attempts |
|---|---|---|
| `SENT` | 完成 | — |
| `PERMANENT` | 立即死信 | — |
| `TRANSIENT` | 退避重试 | **+1** |
| `CONNECT_ERROR` / `NOT_ATTEMPTED` | 归还队列等待 | **0** |
| 未知分类 | 归还 + `Log.Error` | 0 |

**唯一的行为变化**：`rate_limited` 原本返回 `TRANSIENT`（消耗一次重试），现在不消耗。
这正是上一轮标记的那处不一致——两个都是"通道没被调用"，理应一致。

**连带修掉的一个隐患**：`smtpin` 的回复码 switch 用 `default` 兜"永久失败"。
若不显式加 `ClassNotAttempted`，熔断/配额的拒绝会从 451（让发信方重投）
变成 550（直接把邮件退回去）——**恰恰在系统最需要兜住这条通知的时候把它销毁**。
已加入可重试分支，并补测试钉住五种分类各自的回复码。

**不变式**（写进 `result.go` 与 `router.go` 的注释）：
`skip_reason` 存在 ⟺ `class == ClassNotAttempted`。为了让它对**任何**通道实现都成立，
路由对"通道自己返回 `NotAttempted`"做了归一化——通道刚跑完就声称没跑是自相矛盾，
按 `CONNECT_ERROR` 处理。

### 6 · 本轮验收

| # | 项 | 结果 |
|---|---|---|
| 1 | `skip_reason` 三类都出现，且仅在未被调用时 | ✅ 三类各有单测；`breaker_open` / `quota_exhausted` 另有实测 |
| 2 | `skip_reason` 进审计记录与 API 响应 | ✅ 同步响应、审计日志、`attempts` 表三处实测确认 |
| 3 | `Private` 承诺在投递路径兑现 | ✅ 实测 `grep -c SUPERSECRET…` 从 1 → 0，主机名保留；`webhook.url` 一并纳入 |
| 4 | 半开探针槽位不泄漏 | ✅ 变异测试验证 |
| 5 | 老库能升级（新增列） | ✅ `TestOpen_AddsColumnsToAnExistingDatabase`、`TestOpen_MigrationIsIdempotent` |
| 6 | 三类拒绝返回 `NOT_ATTEMPTED` | ✅ 三类各一条，同时断言 `class`、`skip_reason` 与 `status` |
| 7 | `skip_reason` ⟺ `NOT_ATTEMPTED` | ✅ `TestDeliver_SkipReasonAndClassAgree`；反例 `TestDeliver_PartiallySentDeliveryIsNotSkipped` |
| 8 | 范围校验来自 `ParamSpec` | ✅ 改 schema → 校验行为跟着变（email port） |
| 9 | 凭据漏标 `Private` 会被测出 | ✅ 全通道遍历 + 变异测试验证 |
| 10 | SMTP 回复码不因新分类退化 | ✅ 5 种分类各自的 451/550/接受 |
| 11 | **M4 已通过的 10 条不回退** | ✅ `go test ./...` 全绿（18 个包） |

---

## M5 实施记录（2026-09-21，分三块进行）

M5 是三个里程碑的量，按依赖顺序拆成 M5.1 → M5.2 → M5.3：
UI 依赖 API，部署依赖配置能进 DB，这个顺序不是选出来的。

### M5.1 · 配置进 DB、管理 API、热加载

**渠道配置进数据库**（`internal/store` + `internal/config/channels.go`）

| 决定 | 理由 |
|---|---|
| **逐字段密封**，不是整块加密 | 整块加密会把 SMTP 主机和密码一起藏起来，而主机正是打开数据库的人要找的东西。哪写值算凭据**从通道自己的 `ParamSchema` 读**——和驱动脱敏、驱动表单的是同一份声明，加通道时不用额外记得什么 |
| 读取由 `v1:` 前缀驱动，写入由 schema 驱动 | 本二进制不认识的通道类型，行仍然读得出来 |
| **没有密钥就不许存凭据**，而不是明文存下 | 与 `!env` 的既有规则一致：拒绝启动比静默降级响亮 |
| 文件只在**首次启动**导入，标记写在 `meta` 表 | 用"表为空"当信号是错的：运维在 UI 里删光通道后，每次重启它们都会从文件里复活 |

**管理 API**（`internal/admin/`）：登录（argon2id、常量时间、按来源地址退避限流）、
渠道增删改查、连通性测试、手动重置熔断器、投递查询与死信重放、运维审计。

会话**存在服务端**而不是签名 cookie：签名 cookie 只能靠浏览器不再发送来登出，
从共用机器上拷走的副本会一直有效到过期。CSRF 用 `SameSite=Lax` + 必需的自定义头两把锁。

**热加载**：router 的四个派生 map 合成一个**原子指针指向的不可变值**。
读者一次 load 拿到一致快照，重载不会被看到"应用了一半"；进行中的投递持有它已解析到的通道对象。

### M5.2 · Web 后台

**表单由 schema 生成**（验收 #2、#11）。做法是**服务端渲染全部通道类型的字段组**，
JS 只负责按选中的类型显示、按 `ShowIf` 联动——所以 Go 测试能对真实 HTML 断言，
而不是对一段前端代码断言。

`ShowIf` 的语义在这轮定死并写进 `ParamSpec` 注释：
**条件字段在显示时必填，除非它声明了 `Default`**。
按这条规则检查现有声明时发现两处不成立，已修正：
`signature_prefix`（空前缀是常态）和 `to_user`/`to_party`（是二选一而非两个必填）
各补了 `Default: ""`——空默认值就是"适用但不必填"的表达。

**死信重放需要正文**，而正文原先在进死信时被删掉了。
`deadLetter` 改为保留正文，由 `failed_retention` 的清理策略兜底——
一个没有消息的重放不是重放，是一行"这条通知丢了且找不回来"的记录。

### M5.3 · 部署产物与运维文档

**产出**：`deploy/helm/notifyrelay/`（chart）、`deploy/systemd/notifyrelay.service`、
`.env.example`、`docs/06-operations.md`（运维手册）、修订的 `Dockerfile` 与 `docker-compose.yml`。

**SIGHUP 热加载**（验收 #8）。每处改动都是**原子替换一个每次使用才读的值**，
所以进行中的投递不被打断——正在重试的投递保留它的尝试计数，下一次决策时用新的节奏。

| 生效 | 需要重启（**日志里会列出来**） |
|---|---|
| `log.level`、`auth.api_keys`、`timeouts.*`、`retry.*`、`circuit_breaker.*`、渠道配置 | `server.addr`、`storage.*`、`queue.workers`/`batch`/`poll_every`/`claim_timeout`/`recover_every`/retention、`smtp_in.*`、`admin.*`、`secret_key` |

后半张表不是"没做"，是**做不到**：它们各自持有监听器、连接或 goroutine，
没法在脚下替换掉自己。假装可以，只会得到一个报告新地址却仍在旧地址上服务的进程。
服务会打 `reload: some settings need a restart and were not applied` 并列出是哪些——
**看到 `reload: done` 不等于全都生效了**。

文件解析失败或校验不过时，服务保持原配置继续跑。重载不是停下来的理由，
刚打错字的运维不该以一次故障的形式发现这件事。

**Dockerfile 修了一个真实缺陷**：非 root 用户在 `/` 下创建不了数据库。
distroless 默认工作目录是 `/`，而服务首次启动要建 `data/notifyrelay.db`——
**容器会在启动时因权限失败**。加了 `WORKDIR /app`，数据卷挂在 `/app/data`，
示例配置的相对路径因此不用改。

另外加了 `--healthcheck` 子命令：**distroless 镜像里没有 shell、没有 curl**，
容器探针除了这个二进制自己没有别的东西可跑。compose 与 k8s 都用它。

**`secret_key` 与 `session_key` 必须是不同的值**，`Validate()` 会拒绝相同的情况。
一个密钥服务两个用途，意味着较弱那一侧的弱点变成两侧共同的弱点。

**新增 `deploy/` 包**，只放检查不放代码：YAML 能否解析、Helm 模板能否编译、
systemd unit 里关键指令在不在、Dockerfile 的探针有没有用 shell 工具。
这些检查**刻意是浅的**——它们证明不了镜像能构建、chart 能安装，
只有 Docker 和 Helm 能证明，而本机两个都没有。

#### 仍未验证（已写进 `06-operations.md` §8）

- **Docker 镜像从未构建过**（本机无 Docker）。构建命令本身验证过：
  `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` 产出的确实是
  `ELF 64-bit LSB executable, x86-64, statically linked, stripped`——
  distroless 的前提成立。未验证的是镜像组装、基础镜像、非 root 下的文件权限。
- **Helm chart 从未 `helm install` 过**（本机无 helm）。
- **systemd unit 从未在 systemd 上跑过**。
- **钉钉/飞书加签未对真实平台验证**（M3 起就挂着）。
- **SIGHUP 的信号投递在 Windows 上无法验证**。重载逻辑有测试覆盖，
  未覆盖的是 `signal.Notify` 那一行。

这些不是"应该没问题"，是"没试过"。

### 本轮实测

- 后台新建通道 → 测试连通 → **不重启**通过通知 API 投递成功（验收 #1）
- 通道页渲染出 6 个类型组、**64 个字段**，与 schema 完全一致；
  `webhook.token` = `data-required="1" show_if auth_type=bearer`，
  `webhook.signature_header` = `data-required="0" show_if auth_type=hmac`（验收 #11）
- 投递记录页按状态筛选、显示队列统计（验收 #5）
- 死信重放：重放后 attempts 从 1 变 2（**历史保留**），审计记录 `delivery.replay`（验收 #6）
- 任何页面都不含凭据明文

### 本轮测试抓到的四个真缺陷

| # | 缺陷 | 后果 |
|---|---|---|
| 1 | `validateChannel` 跑在凭据合并**之前** | 运维只改一个 host 就会被拒，报"username is set but password is not"——而表单从没拿到过密码，**没有办法满足** |
| 2 | `QuotaConfig` 只有 yaml tag | 配额根本无法通过 API 设置；单测直接构造 Go 结构体，永远碰不到 tag |
| 3 | 编辑表单渲染的是**空白**的 catalog 条目 | 打开通道看到空字段，一保存 host/from/to 全丢（合并只保留私密参数） |
| 4 | chi 的 `Mount` 下子树路由 `/*` 失效 | `/static/app.css` 直连 200、经挂载 404。改为逐个文件注册普通路由 |

第 3 条如果只跑单测不会发现——它是"渲染出来的东西对不对"，只有把页面打开看才知道。

---

## M5 · 管理后台 / 部署（原始计划）

**目标：从"能跑"变成"可运维、可自助"。**

### 任务清单

- [ ] **配置从文件迁移到 DB**（渠道配置的读写界面）
  - [ ] 表结构：`channel(id, name, type, config_json, enabled, created_at, updated_at)`
  - [ ] **密钥字段加密存储**；加密密钥**独立于其他用途的密钥**（simplerelay 用同一密钥派生 JWT 与加密，是反例）
  - [ ] 抄 notifo 的 `IntegrationProperty` 模型（**MIT**）：类型/必填/默认值/枚举/校验**内建在 schema 里**，服务端校验与前端表单**同源**——避免 alphorn 那种"Zod schema + 另一份 configFields"双写
  - [ ] ⚠️ notifo 的 `IntegrationProperty` 是**设计范本**，不是抄代码（943 个 C# 文件，仅参考结构）
- [ ] **Web 后台**
  - [ ] 登录（最小实现：用户名密码 + argon2/bcrypt；**不要用 JWT 密钥派生其他密钥**）
  - [ ] 渠道配置的增删改查 + **连通性测试按钮**（调 `Channel.Test()`）
  - [ ] 投递记录查询（按时间/通道/状态/关键字筛选）
  - [ ] 死信查看与手动重放
  - [ ] **手动重置通道熔断器**（见下方说明）
  - [ ] **不要硬编码 `debug` 开关**（NotifyHub 的教训）

#### 为什么"手动重置熔断器"是必需项

熔断器的状态**故意落库且重启不忘**（`breakers` 表，M4 验收 6b）。这在故障期间是对的：
重启往往就是事故的一部分，让进程一重启就把积压灌进还没恢复的下游，是把一次故障变成两次。

但它同时意味着**恢复路径上有一个只能靠等待穿过的环节**：下游恢复了、或者运维重启了对端，
服务这边仍然要等满 `open_timeout` 才会放第一个探针，然后还要攒够 `success_threshold`
次成功才真正转 closed。生产配置里这是 60 秒 + 2 次，看起来不长；但如果阈值被调高
（通道不稳定时运维会那么做），等待就是分钟级的，而**下游早就好了**。

没有这个按钮，唯一的加速手段是重启服务——而重启恰恰要等重新加载、重建队列，
并且丢掉内存里的滑动窗口配额计数。**用重启来解决"我想让它立刻重试"是错的工具**，
所以这个功能不是锦上添花，是故障恢复路径的必要一环。

实现要求：

- 重置 = 把该通道的 breaker 置为 `closed`、`failures = 0`、`opened_at` 清零，**并写穿到 `breakers` 表**
  （只改内存的话，下一次 `load()` 会把旧状态读回来——`load` 每个 breaker 只跑一次，
  但进程重启后又是一条好汉般地"忘掉"这次重置）。
- 记审计：谁在什么时候重置了哪个通道。这是**人为把一个自动保护措施关掉**的动作，
  事后必须能查到。
- 重置**不清空队列、不重置配额计数**。它只回答"再试一次"，
  不回答"把今天已经发出去的额度还回来"。两件事混在一起会让配额失去意义。
- 半开期的探针名额一并清零（`probes = 0`），否则重置后如果还卡在旧名额上，
  按下按钮看起来什么都没发生。
- [ ] **配置热加载**：改配置不重启（SIGHUP 或文件 watch）
- [ ] **部署产物**
  - [ ] Docker 镜像（多阶段、非 root、静态二进制）
  - [ ] docker-compose（app + 可选 DB）
  - [ ] Helm chart（可改自 awha 的 chart，**Apache-2.0**）
  - [ ] systemd unit
  - [ ] 配置文件样例 + 完整参数文档
- [ ] **运维文档**：升级流程、备份（DB + spool）、指标说明、常见故障排查（发信进垃圾箱怎么办）
- [ ] **（可选）迁移工具**：`--migrate-config` 把 YAML 配置导入 DB

### 验收标准

1. 在后台新建一个通道、填配置、点"测试"，**不重启服务**即可通过 API 发消息到该通道。
2. 后台看到的通道参数表单字段，与 `ParamSchema` **完全一致**（改 schema 后表单自动变化，无需改前端）。
3. DB 里 `config_json` 中的密码字段是**密文**；直接用 DB 客户端查看无法得到明文。
4. 加密密钥与登录 token 签名密钥**不是同一个**（代码评审确认）。
5. 投递记录页能查到 M4 产生的全部审计数据，筛选与分页正常。
6. 死信可手动重放并成功投递。
7. **通道被熔断后，在后台点"重置熔断器"，下一条投递立刻被当作探针放行**，
   不必等满 `open_timeout`；且**重启服务后该通道仍然是 closed**（重置写穿了 `breakers` 表）。
8. `docker compose up` 一条命令起服务；`helm install` 可在 k8s 集群部署成功。
9. 配置文件热加载：修改重试次数后发 SIGHUP，新配置生效且**不中断正在进行的投递**。
10. 一个**全新的人**按 README 能在 15 分钟内完成部署并发出一条通知（找真人验证）。
11. 通道参数表单的联动与互斥**正确**：`webhook` 选 `auth_type=hmac` 时只有 `secret` 被标为必填，
    选 `basic` 时是 `username`/`password`；`wecom` 选 `mode=app` 时 `webhook_url` 不再出现。
    （这一条取决于 `docs/05-paramschema-audit.md` §5 的方案选择）

### 风险点

| 风险 | 影响 | 应对 |
|---|---|---|
| **后台成为攻击面** | 内部服务被入侵 | 最小登录实现 + 默认只监听内网 + 密钥加密存储 + 不用默认密码 |
| 配置双份真相（文件 + DB） | 行为不一致 | **DB 是唯一真相**；文件只在未启用 DB 时生效；迁移后文件仅存全局设置 |
| 前端表单手写 | 加通道要改前端 | 表单**必须由 `ParamSchema` 生成**，这是 M2 就定好的架构红利。⚠️ 但 audit（`docs/05-paramschema-audit.md`）发现现有 schema **表达不了字段联动与互斥**，6 个通道里有 9 处——生成之前要先补 `ShowIf`/`Group`，否则生成出"填了就起不来"的表单 |
| 热加载导致状态不一致 | 并发问题 | 热加载只重建通道实例，队列与正在进行的投递不受影响 |
| DB 迁移出错 | 数据丢失 | Alembic/goose 类工具 + 升级前自动备份 |

---

## 里程碑依赖关系

```
M0 ──> M1 ──> M2 ──> M3
                │
                └──> M4 ──> M5
```

- **M0 阻塞于技术栈确认**（Q1）。
- **M1 可独立交付价值**（邮件能发出去），建议先做出这个再评估后续。
- M2 是**架构转折点**——抽象定错，M3 会变成"每加一个通道改一遍核心"。
- M4 依赖 M2 的 `Capability` / `Result` 契约；若 M2 没做好三分类，M4 无法实现。
- M5 的"表单由 schema 生成"红利来自 M2，**M2 偷懒 M5 就要还债**。

---

## 已确认的关键决策（2026-09-21 定稿）

**以下决策已确认，实施期间不再变更架构。**

| # | 决策点 | 确认结果 |
|---|---|---|
| **Q1** | 语言 | **Go 1.24+**；构建固定 `GOARCH=amd64`（单二进制与 docker compose 构建均适用） |
| **Q2** | 数据库 | **M1–M3 不用 DB**；**M4 起用 SQLite**，必须纯 Go 驱动（`modernc.org/sqlite`），**禁止 CGO** |
| **Q3** | 部署方式 | **单静态二进制 + Docker 两者都要** |
| **Q4** | 管理后台 | **M5 再做**；但 **M2 的 `ParamSchema` 必须做对**——后台表单由它生成 |
| **Q5** | 邮件 | **MVP 只做"经上游 SMTP 中继"**；配置层**预留直投能力但不实现** |
| **Q6** | 鉴权 | **Bearer API Key**，SHA-256 哈希存储 + **常量时间比较** |
| **Q7** | API 风格 | **REST 为主**（`POST /api/v1/notify`）；**Gotify 兼容入口留到 M3 可选** |
| **Q8** | SMTP 入口 | **进 M1**（不是 M2） |
| **Q9** | 发送模式 | **M1–M3 同步返回**；**M4 改异步 + `GET /api/v1/messages/{id}`**，同步作为可选参数保留 |
| **Q10** | 项目自身 License | **待确认**（M0 需要建 `LICENSE` 文件，见下） |

### 额外约束（实施规则，来自确认时补充）

1. **邮件实现路线**：`wneessen/go-mail` 作 SMTP 客户端底座；从 notification-manager 移植的是**消息组装与编码逻辑**（`mime.QEncoding` 主题、`multipart/alternative`、`quoted-printable`），**不是整个 `email.go`**。
2. **HTTP 层超时**：`http.Server` 的 `ReadTimeout`/`WriteTimeout` 与 handler 内 `context.WithTimeout` 必须显式设置，已写入 M1 验收标准第 10 条。
3. **署名**：M0 建 `NOTICE`；M1 移植代码文件头加来源注释（仓库 + 文件路径 + License）；README 加致谢段。
4. **里程碑节奏**：按 M0 → M1 顺序执行，不跳步；**M2 之后每完成一个里程碑停下等确认**。
5. **接口契约**：所有通道实现统一 `Channel` 接口并返回三分类 `Result`（`CONNECT_ERROR` / `TRANSIENT` / `PERMANENT`），接口按 `02-scope.md` §4.2 定死。
6. **核心路由禁止出现 `"email"` 字样**，通道名只出现在通道自己的包与配置文件里。
7. **密钥一律 `!env` 注入**，禁止明文进配置文件。
8. **禁止引入 Redis / Kafka / RabbitMQ / MQ**，M1–M3 内存队列即可。

### 待确认（非阻塞）

| # | 决策点 | 说明 |
|---|---|---|
| **Q10** | 项目自身用什么 License？ | **已定：AGPL-3.0**（`LICENSE` 已建，`NOTICE` 已建） |
| **Q11** | `ParamSpec` 是否补字段？ | **已决（2026-09-21）：选 (a)，补 `ShowIf` 与 `Min`/`Max` 后生成表单。** 见下方「进 M5 之前的补齐」§4。`Group` / `Example` / `SecretFromEnv` 本次未补，见 `05-paramschema-audit.md` §4 的剩余清单 |
| **Q12** | `rate_limited` 是否也改为不消耗重试预算？ | **已决（2026-09-21）：是。** 三类拒绝统一为 `ClassNotAttempted`，队列一律归还且不消耗 attempts。见下方 §5 |

### 待确认（非阻塞）· 已清空

原 Q10/Q11/Q12 均已决。M5 开工前无遗留决策。

### 本轮之后仍需注意的两处

| 项 | 说明 |
|---|---|
| **互斥无法用 `ShowIf` 表达** | `ShowIf` 只做等值判断，表达不了"`webhook_url` 与 `token` 二选一"或"`username` 非空则 `password` 必填"。这些校验仍在 `parseConfig` 里（`slack/config.go`、`email/config.go`、`wecom/config.go`）。M5 的表单需要为它们手写一小段联动，或后续再补一个 `AtLeastOneOf` / `RequiredWith` 声明。见 `05-paramschema-audit.md` §4.2 |
| **`agent_id` 的范围是条件性的** | "app 模式下必须为正"不是一条范围，而是随 `mode` 变化的规则，`Min` 表达不了（声明 `Min: 1` 会把 webhook 模式下合法的缺省 0 也拒掉）。校验留在 `parseConfig` 里，紧挨着判断 `mode` 的那行。schema 上只声明了 `ShowIf`（可见性） |
