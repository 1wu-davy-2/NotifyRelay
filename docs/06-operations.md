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
| `storage.path`（默认 `data/notifyrelay.db`） | 队列、渠道配置（凭据已密封）、投递审计、熔断状态、配额计数 | 未送达的通知全部丢失 |
| `storage.spool_dir`（默认 `data/spool`） | 待投递消息的正文 | 同上 |
| `secret_key`（环境变量） | 解开渠道凭据的密钥 | **数据库里所有凭据变成不可读的密文** |

```bash
# SQLite 的 WAL 模式下，直接 cp 数据库文件可能拿到一个不一致的快照。
# 用 SQLite 自己的备份命令，或者停服务再拷。
sqlite3 data/notifyrelay.db ".backup '/backup/notifyrelay-$(date +%F).db'"
cp -a data/spool /backup/spool-$(date +%F)
```

**`secret_key` 要和数据库分开存。** 一份只有密文的备份，和没有备份的区别是
"看起来有备份"。它不在数据库里，所以任何只备份数据目录的方案都会漏掉它。

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

- 默认关闭（`admin.enabled: false`）。打开前先确认 `server.addr` 只监听内网。
- 会话在服务端内存里，**重启会踢掉所有人**。这是失败该偏的方向。
- 密码用 argon2id，登录失败按来源 IP 退避。
- 所有改状态的请求要求自定义头（`X-NotifyRelay-Admin`），加上 `SameSite=Lax` 的
  cookie——两把独立的锁，防 CSRF。
- **凭据不会回到浏览器**。表单显示"已设置"和一个清除勾选框，值本身不出库。
- 后台做的每一件改状态的事都记进 `admin_audit` 表，保留期不受投递保留策略影响。

---

## 8. 还没有被验证的部分

诚实起见，列在这里：

- **Docker 镜像从未构建过。** 写这份文档的机器上没有 Docker。`docker build` 的
  构建命令本身（`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build`）在本地验证过，
  但镜像组装、distroless 基础镜像、非 root 用户下的文件权限都没有实跑过。
- **Helm chart 从未 `helm install` 过。** 模板能通过 Go 模板解析、values 能被解析，
  但渲染结果和集群里的行为没验证过。
- **systemd unit 从未在 systemd 上跑过。**
- **钉钉与飞书的加签算法没有对真实平台验证过。** 代码和测试注释里都标了。
- **`SIGHUP` 的信号投递在 Windows 上没有验证**（Windows 没有 SIGHUP）。
  重载逻辑本身有测试覆盖，未覆盖的是 `signal.Notify` 那一行。

这些不是"应该没问题"，是"没试过"。
