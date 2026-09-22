# 运维手册

面向把这个服务跑起来、并且在它出问题时被叫醒的人。

---

## 1. 它是什么，它保证什么

一次接入，多端送达：上游用 HTTP API 或 SMTP 投递一条通知，下游按配置分发到
邮件 / webhook / Slack / 钉钉 / 飞书 / 企微。

**投递保证是"至少一次"，不是"恰好一次"。** 队列是持久化的，被领走超过
`claim_timeout` 没回写的投递会被判为孤儿并归还队列——这是为了不让进程被杀、
worker 卡死、机器掉电丢掉通知。代价是：**如果一个投递实际上成功了但回写失败，
它会被再投一次。** 上游要能接受重复。

**配额与限流是"尽力而为"的边界，不是账本。** 短窗口（秒/分/时）在内存里，
重启会丢几秒的计数——这是刻意的，为几秒的精度落库不值。日/月窗口落库。

---

## 2. 备份

**要备份的是一个目录，不是两个东西。**

| 位置 | 内容 | 丢了会怎样 |
|---|---|---|
| `storage.path`（默认 `data/notifyrelay.db`） | 队列、渠道配置（凭据已密封）、API Key 摘要、管理员账号、投递审计、熔断状态、配额计数 | 未送达的通知全部丢失 |
| `storage.spool_dir`（默认 `data/spool`） | 待投递消息的正文 | 同上 |
| `<数据目录>/keys/secret.key` | 解开渠道凭据的密钥 | **数据库里所有凭据变成不可读的密文** |
| `<数据目录>/keys/session.key` | 签后台会话的密钥 | 所有人被登出，重新登录即可 |

```bash
# 整个数据目录就是一份完整、可恢复的备份。
# SQLite 的 WAL 模式下，直接 cp 数据库文件可能拿到一个不一致的快照——
# 用 SQLite 自己的备份命令，或者停服务再拷。
sqlite3 data/notifyrelay.db ".backup '/backup/notifyrelay-$(date +%F).db'"
cp -a data/spool /backup/spool-$(date +%F)
cp -a data/keys  /backup/keys-$(date +%F)
```

**密钥和数据放在一起，这是有意的取舍。**

早先的版本要求 `secret_key` 必须显式配置、和数据库分开存，理由是"一份只有密文的备份
和没有备份没区别"。现在默认生成到数据目录里，换来的是：**备份一个目录就能完整恢复**。

代价是明确的：**拿到这份备份的人，同时拿到了密文和密钥**。对内部服务来说，
实际会发生的故障是"密钥丢了、备份恢复不了"，而不是"备份泄露"——所以选前者。
但如果你所在的环境判断相反，把 `secret_key` 写进配置（`!env` 注入）即可覆盖自动生成，
两种做法都支持。

**`session.key` 丢了不用慌**，它只签后台的登录态，重新登录就行。它和 `secret.key`
是两个独立的文件、两个独立的值——一个密钥服务两个用途，意味着较弱那个用途的弱点
会变成两者的弱点。

**恢复**：把数据库与 spool 放回原路径，确保 `secret_key` 是当初那一把，
启动。密钥不对时服务会在读取渠道配置时报
`value could not be opened with this key (wrong key, or the value was sealed by another instance)`
——这是明确的，不会静默变成空凭据。

---

## 3. 升级

```bash
# 1. 备份（见上）。这一步不能省。
# 2. 换二进制
install -m 0755 notifyrelay-new /usr/local/bin/notifyrelay
systemctl restart notifyrelay
```

数据库 schema 在启动时自动应用（`CREATE TABLE IF NOT EXISTS` 加按列的
`ALTER TABLE ADD COLUMN`）。**只增不删**，所以回滚到旧版本时多出来的列会被忽略，
不会导致启动失败。

**升级后第一次启动会做一件事**：把配置文件里的 `channels:` 块导入数据库，并在
`meta` 表里记一笔。此后数据库是唯一真相，文件里的 `channels:` 不再被读取。
所以升级前请确认 `channels:` 块是你想要的最终状态——它只会被导入这一次。

**要重新触发导入**：删掉 `meta` 表里的 `channels_imported_at` 行，重启。
不要用"表为空"来判断——那是另一个意思。

---

## 4. 不重启改配置

```bash
systemctl reload notifyrelay     # 发 SIGHUP
```

**会生效**：

| 设置 | 说明 |
|---|---|
| `log.level` | 排查问题时打开 debug，不用重启 |
| `auth.api_keys` | **轮换泄露的密钥不需要重启** |
| `timeouts.handler` / `timeouts.deliver` | |
| `retry.*` | 重试次数与退避节奏 |
| `circuit_breaker.*` | 提高阈值不会关闭已经打开的熔断器——那是"重置"该做的事 |
| 渠道配置 | 从**数据库**重读，所以手工改过 SQLite 也能用 SIGHUP 生效 |

**需要重启，且服务会在日志里明说**：`server.addr`、`storage.*`、
`queue.workers` / `batch` / `poll_every` / `claim_timeout` / `recover_every` /
各种 retention、`smtp_in.*`、`admin.*`、`secret_key`。

它们各自持有监听器、连接或 goroutine，没法在脚下替换掉自己。服务会打一条
`reload: some settings need a restart and were not applied` 并列出是哪些——
**看到 `reload: done` 不等于全都生效了**。

**改坏了不会出事**：文件解析失败或校验不过，服务保持原配置继续跑，只在日志里报错。

---

## 5. 指标

`GET /metrics`（Prometheus 格式，不需要鉴权）。

| 指标 | 看什么 |
|---|---|
| 队列深度（按状态） | `queued` 持续增长 = 投递速度跟不上，或者某个通道在熔断 |
| 按分类的尝试次数 | `TRANSIENT` 涨 = 对端在拒绝；`CONNECT_ERROR` 涨 = 连不上 |
| `NOT_ATTEMPTED` | 被熔断/配额/限流挡下的。**这个涨说明通道没被调用**，和连不上是两回事 |
| 投递耗时 | 尾部变长 = 对端变慢，该调 `timeouts.deliver` 了 |

**要区分"通道坏了"和"预算用完了"，看审计里的 `skip_reason`**：

| `skip_reason` | 该做什么 |
|---|---|
| `breaker_open` | 查下游。恢复后到后台点"重置熔断器"，不用等 `open_timeout` |
| `quota_exhausted` | 等窗口滚动，或者调高配额 |
| `rate_limited` | 限流太紧，或者积压太多 |

---

## 6. 常见问题

**重启后没立刻发，日志说 channel is still open from a previous run**
熔断状态是**故意持久化**的——重启往往就是事故的一部分，让进程一重启就把积压
灌进还没恢复的下游，是把一次故障变成两次。要么等 `open_timeout`，要么到
后台点"重置熔断器"。

**通知发了但没收到，投递记录是 `failed`**
看尝试历史里的 `class`：
- `PERMANENT` — 对端明确拒绝（4xx、地址不存在）。**重试没有意义**，改配置或改收件人。
- `TRANSIENT` — 对端说稍后再试，重试次数用完了。看 `retry.max_attempts` 和 `max_age`。
- `NOT_ATTEMPTED` — 通道根本没被调用。看 `skip_reason`。

**死信想重发**
投递详情页有 Replay 按钮。它把投递放回队列并**重置重试预算**，尝试历史保留。
只对 `failed` 的投递可用——`sent` 的重放会给真人发第二条。

**`the message body is no longer on disk`**
消息正文被保留策略清掉了（`queue.failed_retention`，默认 30 天）。行还在，正文没了，
所以重放不了。要更长的重放窗口就调大 `failed_retention`——代价是磁盘。

**`secret_key` 没配但想加一个带密码的通道**
服务会拒绝保存，并告诉你生成密钥的命令。这是有意的：**没有密钥就不写明文**。
`notifyrelay --gen-key` 生成一个，写进配置（用 `!env`），重启。

**上游 SMTP 投递收到 451**
这是对的。451 = "稍后再来"，发信方的 MTA 会自己重投。被熔断或被配额挡下的投递
也回 451 而不是 550——一条只是暂时被挡下的通知不该在这一步被退回去。

**后台登录不上**
失败次数多了会按来源 IP 退避（1 秒翻倍，上限 5 分钟），响应头带 `Retry-After`。
等一会儿。这是防爆破的，不是坏了。

---

## 7. 后台的安全边界

后台能改所有通道的配置，**它比通知 API 敏感得多**。

- **默认值分两种。** 二进制默认关闭（`admin.enabled: false`）：手工在机器上起一个服务，
  不该顺带开出一个没人要的管理界面。容器镜像里默认开启，因为整个镜像的意义就是
  "不用编辑器也能配好"，而那需要界面。两个默认值不同是有意的。
- **首次启动没有管理员。** 第一个打开 `/admin` 的人会看到"创建管理员"页面，
  建完之后那个页面永久关闭。**这段窗口是真的**：从服务启动到你打开页面之间，
  先到的人就是管理员。启动日志里会明确写出来。
  不想留窗口，就在配置里写死 `admin.password_hash`，页面不会出现。
- 会话在服务端内存里，**重启会踢掉所有人**。这是失败该偏的方向。
- 密码用 argon2id，登录失败按来源 IP 退避（首次建号也走同一套退避）。
- 所有改状态的请求要求自定义头（`X-NotifyRelay-Admin`），加上 `SameSite=Lax` 的
  cookie——两把独立的锁，防 CSRF。**这个头只查存在、不比值**，它防的是跨站表单，
  不是第二把凭据。
- **凭据不会回到浏览器**。表单显示"已设置"和一个清除勾选框，值本身不出库。
- **API Key 的明文只出现一次**，就是创建它的那个响应。库里只有 sha256 摘要，
  丢了只能删掉重建。
- 后台做的每一件改状态的事都记进 `admin_audit` 表，保留期不受投递保留策略影响。

---

## 8. 还没有被验证的部分

每一条都写了**触发条件**——不是"以后有空验一下"，是"什么情况下这件事必须被验"。

### 8.1 钉钉 / 飞书加签：未对真实平台验证

代码和测试注释里都标了。**单测过了不等于对端接受**——单测验证的是"我们按文档算出了
一个签名"，不是"对方的服务器认这个签名"。

**触发条件**：M3 的通道首次接入生产时，用**真实 webhook** 做一次连通性测试
（后台的"测试连通"按钮会真的发一条）。

签名被拒时的排查顺序：

1. **先查时间戳是否超出对端窗口。** 钉钉的窗口是 **±1 小时**，容器时钟漂移、
   NTP 没同步、或者跨时区部署都会踩到。这是最常见的原因，也是最容易被忽略的——
   签名算对了，时间戳不对，一样被拒。
2. **再查 secret 的编码。** 钉钉加签的 secret 是 `SEC` 开头的字符串，
   要**原样**参与 HMAC，不要做 base64 解码或 URL 解码。飞书同理。
   如果 secret 是通过 `!env` 注入的，确认环境变量里没有多余的空格或换行
   （`echo $SECRET | ...` 写进 `.env` 时会带上换行）。

### 8.2 Docker 镜像：✅ 已由 CI 验证

`.github/workflows/docker.yml` 在 main 上跑通（2026-09-22，commit `80460f3`）。
它构建镜像、断言 `--healthcheck` 对不可达地址返回非 0、启动容器并断言日志里
出现 `listening`。

**这条曾经不是"没试过"而是"试了会失败"**：镜像里的数据目录归 root 所有而服务以
nonroot 运行，容器会在启动时因权限失败。distroless 没有 shell，没法用 `RUN chown`
修，得在构建阶段建好目录再 `COPY --chown` 进来。CI 跑通说明这处修对了。

本地仍然只验证过构建命令本身（`CGO_ENABLED=0 GOOS=linux GOARCH=amd64` 产出
`ELF 64-bit LSB executable, x86-64, statically linked, stripped`）——
但镜像组装、基础镜像、文件权限现在有了真实证据。

### 8.3 Helm chart：渲染 ✅ 已由 CI 验证 / 安装仍待验证

`.github/workflows/helm.yml` 在 main 上跑通（2026-09-22，commit `80460f3`）：
`helm lint` 通过，`helm template` 渲染出 Deployment / Service / ConfigMap，
replicas 固定在 1，ConfigMap 里的配置能被解析。

**它证明不了 chart 能在集群里装起来**——那需要 `helm install` 和一个真实集群。
所以这一条只撤销一半："渲染正确"有证据，"安装成功"仍然没有。

**触发条件**：首次在真实集群上 `helm install` 时。

### 8.4 systemd unit：未在 systemd 上运行过

`deploy/` 里的检查只断言了关键指令存在（`WorkingDirectory`、`ReadWritePaths`、
`ExecReload`、非 root 用户）。**它证明不了 unit 能启动。**

**触发条件**：首次在 systemd 主机上部署时。

### 8.5 SIGHUP 信号投递：Windows 上无法验证

Windows 没有 SIGHUP。重载逻辑本身有测试覆盖（注入信号通道，断言策略真的变了），
**未覆盖的是 `signal.Notify` 那一行**。

**触发条件**：首次在 Linux 上用 `systemctl reload` 或 `kill -HUP` 时。
验法是改一下 `retry.max_attempts`，reload，发一条必然失败的异步投递，数尝试次数。

> 注意：容器镜像里 `--healthcheck` 已经在 CI 里跑过了，但**信号投递没有**——
> 两者是不同的东西，`--healthcheck` 是子命令，SIGHUP 是进程信号。

### 8.6 M5 验收 #10：15 分钟真人验证

**待真人验证。** 前提：一台有 Docker 或能跑静态二进制的 Linux，加一个可用的上游
SMTP。找一位**没读过本项目**的同事，只给 README，限时 15 分钟，
看他能不能完成部署并发出一条通知。

这条不能自己验——自己验的时候脑子里已经有那 15 分钟里不该有的东西。

---

这些不是"应该没问题"，是"没试过"。**单测过了不等于对端接受；
本地能构建不等于镜像能跑。** 写清楚未验证，比假装验证过更可信。
