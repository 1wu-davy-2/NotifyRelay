# 07 · API 参考

> 前置阅读：根目录 `README.md`（快速开始、五分类与投递保证）、`docs/06-operations.md`（备份、升级、排障）。
> 本文是**接口的逐字段说明**，不讲设计理由——设计理由在 README 的核心设计一节和 `docs/03-plan.md`。
>
> 依据版本：M5.3（commit `e262d47`）。文中每一条都对着代码核过；
> 凡是代码行为与直觉不符的地方，本文按**实际行为**写，并在 §9 单独列出。

**后台里有一份可以直接复制的版本**：`/admin/api-docs`（登录后从导航栏的 **API** 进入）。
它给出同样的端点与错误码，外加 curl / Go / Python / Java / C# / C / C++ 七种语言的完整调用示例，
服务地址按你当前访问的地址自动填好。本文是权威版本，那份是给"现在就要发一条"的人用的。

---

## 0. 阅读约定

- 所有 JSON 响应（除 `/metrics`）的 `Content-Type` 都是 `application/json; charset=utf-8`。
- 表中「必填」指字段缺失或为空会被拒绝；「可选」指可以整个省略。
- 标了 `omitempty` 的字段在零值时**不出现**；没标的字段**总是出现**，哪怕是零值。
- 路径分三组：公开 API（`/api/v1/*`，Bearer 鉴权）、探针（无鉴权）、管理 API（`/admin/*`，会话鉴权）。
- 未匹配的路由由 chi 返回，是 `404 page not found\n` 纯文本，**不是** JSON 错误体。

---

## 1. 鉴权

### 1.1 公开 API — Bearer

```
Authorization: Bearer <token>
```

- 前缀匹配大小写不敏感，token 两端空白会被裁掉。
- 失败返回 **401**，`error` 为 `unauthorized`，并带响应头
  `WWW-Authenticate: Bearer realm="notifyrelay"`。
- **没有 403 路径**——密钥要么对，要么就是 401。

token 的明文不存任何地方，`auth.api_keys` 里存的是 `sha256:` 摘要，用
`notifyrelay --hash-key '<token>'` 生成。

### 1.2 管理 API — 会话 + CSRF 头

两步，缺一不可：

1. **会话 cookie** `nr_admin`（32 字节随机，base64 RawURL 无填充）。登录成功后下发：

   ```
   Set-Cookie: nr_admin=<id>; Path=/admin; HttpOnly; SameSite=Lax; Max-Age=<ttl>[; Secure]
   ```

   `Secure` 在 `r.TLS != nil` 或 `X-Forwarded-Proto: https` 时加上。

2. **`X-NotifyRelay-Admin` 头**，且**只在改状态的方法上要求**。改状态 = 任何不是
   `GET`/`HEAD`/`OPTIONS` 的方法。

   > ⚠️ **只检查这个头是否存在，不比对它的值。** 它的作用是让浏览器无法用
   > `<form>` 或 `<img>` 伪造请求（跨站请求带不上自定义头），不是第二把凭据。
   > 不要把它当成密钥。

会话中间件对所有管理路由生效（登录/登出除外），失败只有三种：

| 状态码 | `error` | 触发 |
|---|---|---|
| 401 | `unauthenticated` | 没有 cookie（`sign in first`） |
| 401 | `unauthenticated` | cookie 无效或已过期（`the session has expired`） |
| 403 | `missing_csrf_header` | 改状态的方法缺 `X-NotifyRelay-Admin` |

会话存在**服务端内存**里，重启踢掉所有人——这是失败该偏的方向。从共用机器上拷走的
cookie 会随进程重启一起失效，而不是一直有效到过期。

### 1.3 无鉴权

`/healthz`、`/readyz`、`/metrics`、`POST /admin/api/login`、`POST /admin/api/logout`、
`GET /admin/login`、`GET /admin/setup`、`GET /admin/lang/{lang}`、管理后台的静态资源。

后台的页面路由（都需要会话）：`/admin/start`（上手清单）、`/admin/channels`、
`/admin/deliveries`、`/admin/deliveries/{id}`、`/admin/audit`、`/admin/keys`、
`/admin/api-docs`、`/admin/password`。

`/metrics` 不鉴权是刻意的：抓取端不该需要凭据，它暴露的是计数不是内容。
它绑定在服务监听的地址上，想让它私有，改 `server.addr`。

### 1.4 界面语言

后台有两种语言：中文（默认）和英文。

| 来源 | 优先级 | 用途 |
|---|---|---|
| 查询参数 `?lang=zh\|en` | 最高 | 让一条链接锁定语言——截图、缺陷报告、书签 |
| Cookie `nr_lang` | 中 | 记住选择，**跨导航存活** |
| 默认值 | 最低 | `zh` |

`GET /admin/lang/{lang}?to=<路径>` 写 cookie 并 302 回 `to`。`to` 只接受
`/admin/` 开头的路径，且**先做 `path.Clean` 再判断**——`/admin/../etc/passwd`
能通过前缀检查，而浏览器会把它解析成 `/etc/passwd`，所以校验必须针对浏览器
实际会去的地方。`to` 里残留的 `lang` 参数会被剥掉，否则下一次请求就把选择撤销了。

这个端点**不需要会话**：登录页和首次运行页都要能切换语言，而那时还没有会话——
这也正是选择存在 cookie 而不是会话里的原因。

`lang` 值不认识时回落到默认语言，**不报错**。界面没有的语言不是失败，是回落。

> 界面文案的表在 `internal/admin/i18n`，是**结构体**不是 map：漏翻一条是编译错误，
> 而不是运行时空白。审计记录里的 `detail`（`created`、`changed: host` 等）**刻意不翻译**——
> 它们是记录不是界面文案，翻译会让同一张表混两种语言且历史记录无法统一。

**`GET /admin/start` 的清单是查出来的，不是记下来的。** 四步分别是
「有渠道 / 有密钥 / 发过通知 / 看过结果」，每一步都是当场问服务要的事实，
所以删掉唯一那个渠道之后第一步会重新变成未完成。存一个勾选状态做不到这一点，
而一个谎报进度的页面比没有这个页面更糟。左侧导航只在还有未完成步骤时才显示这个入口。

---

## 2. 错误体与错误码

所有 JSON 错误都是同一个形状，两个字段都**总是出现**：

```json
{ "error": "invalid_request", "message": "limit must be a non-negative integer" }
```

`error` 是稳定的机器可读标记，`message` 给人看，措辞可能变。**按 `error` 分支，不要按 `message`。**

### 完整错误码表

| `error` | 典型状态码 | 含义 |
|---|---|---|
| `unauthorized` | 401 | 公开 API 的 Bearer 缺失或无效 |
| `unauthenticated` | 401 | 管理 API 无会话或会话过期 |
| `invalid_credentials` | 401 | 用户名或密码错误（两者返回**同一句话**，不区分） |
| `missing_csrf_header` | 403 | 改状态的请求缺 `X-NotifyRelay-Admin` |
| `not_found` | 404 | 指定的渠道或投递不存在 |
| `not_replayable` | 409 | 该投递不处于可重放状态 |
| `body_expired` | 409 | 投递存在，但正文已被保留策略清掉 |
| `too_many_attempts` | 429 | 登录失败退避中，带 `Retry-After` |
| `invalid_request` | 400 | 请求体或查询参数不合法 |
| `unknown_target` | 400 | `targets` 里有解析不出来的别名（仅异步路径） |
| `invalid_config` | 400 | 渠道参数没过 schema 校验（消息里会点名是哪个参数） |
| `save_failed` | 400 | 渠道写库失败 |
| `queue_unavailable` | 503 | 入队失败 |
| `not_ready` | 503 | `/readyz` 查库失败 |
| `breaker_disabled` | 501 | 本部署没开熔断器，无法重置 |
| `unavailable` | 501 | 本部署没有投递存储 |
| `internal` | 500 | 其余一切。**`message` 里不会有内部细节** |

---

## 3. 公开 API

### 3.1 `POST /api/v1/notify`

发一条通知。

**请求头**

| 头 | 必需 | 说明 |
|---|---|---|
| `Authorization` | ✅ | `Bearer <token>` |
| `Idempotency-Key` | | 任意非空字符串，见 §3.1.3 |
| `Content-Type` | | `application/json` |

请求体上限 **1 MiB**。**未知字段会被拒绝**（`DisallowUnknownFields`），
多写一个 key 就是 400——这是为了防止拼错的字段被静默忽略。

**请求体**

| 字段 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|
| `targets` | `[]string` | ✅ | | 至少一个。别名 `oncall`，或 `类型:别名` `email:oncall`。可混用 |
| `title` | `string` | ✅ | | 去空白后不能为空 |
| `body` | `string` | ✅ | | 去空白后不能为空 |
| `format` | `string` | | `text` | `text` \| `markdown` \| `html`，大小写不敏感。**由调用方声明，服务端不猜** |
| `type` | `string` | | `info` | `info` \| `success` \| `warning` \| `failure`，大小写不敏感 |
| `priority` | `int` | | `3` | 1–5。给 0 会变成 3；给 6 或 −1 直接 400 |
| `tags` | `[]string` | | | 标签 |
| `links` | `[]object` | | | `{"text": "...", "url": "..."}` |
| `at` | `[]string` | | | @ 提及，仅 IM 类通道使用 |
| `meta` | `object` | | | 通道私有扩展点 |
| `sync` | `bool` | | `false` | `true` 则等投递完成再返回 |

> `message.Attachment` 在模型里存在，但**线路上没有 `attach` 字段**——附件尚未开放。

#### 3.1.1 异步（默认）— 202

入队即返回，由后台 worker 负责送达与重试。

```json
{
  "request_id": "96b3604fede17b44",
  "accepted": true,
  "deliveries": [
    { "id": "282d67a1b3c4e5f6", "target": "oncall", "channel_type": "email", "status": "queued" }
  ]
}
```

`deliveries[].status` 这里是**投递状态**（`queued`），不是结果分类。空数组是 `[]`，不是 `null`。

#### 3.1.2 同步 — 200

请求体加 `"sync": true`，等所有目标投递完再返回。

```json
{
  "request_id": "96b3604fede17b44",
  "results": [
    {
      "target": "email:oncall",
      "channel": "oncall",
      "channel_type": "email",
      "status": "sent",
      "elapsed_ms": 31,
      "skip_reason": "breaker_open"
    }
  ]
}
```

| 字段 | 类型 | omitempty | 说明 |
|---|---|---|---|
| `target` | `string` | | **原样回显**调用方写的那串（含 `类型:` 前缀） |
| `channel` | `string` | ✅ | 解析出的实例别名。**解析失败时不存在** |
| `channel_type` | `string` | ✅ | 同上 |
| `status` | `string` | | 小写的结果分类，见 §6.2 |
| `error` | `string` | ✅ | |
| `detail` | `string` | ✅ | 对端返回的摘要，凭据已擦除 |
| `elapsed_ms` | `int64` | | |
| `recipients` | `[]object` | ✅ | 每个收件人独立投递的通道才有（如 email）；单端点通道为 `null` |
| `skip_reason` | `string` | ✅ | **仅当通道根本没被调用时出现**，见 §6.3 |

> `recipients[]` 是唯一没有 json tag 的结构，所以键名是 **Go 字段名原样**，
> 且 `Class` 是**数字**不是字符串：
> `{"Address": "ops@example.com", "Accepted": true, "Class": 1, "Detail": ""}`。
> `Class` 的 0–4 对应 §6.2 的顺序。见 §9。

**同步路径的解析失败不报 400**：目标解析不出来会作为一条
`status: "permanent"` 的结果返回，HTTP 仍是 200。异步路径才会在入队前拒掉整个请求（400
`unknown_target`）。这个不对称是有意的——同步调用方要的是「每个目标成没成」，
而不是「有一个目标名字错了所以整批都没发」。

#### 3.1.3 幂等

带上 `Idempotency-Key` 后，**同一个 key 的第二次提交会原样重放第一次的响应**：

- 状态码与响应体逐字节相同；
- 多一个响应头 `Idempotent-Replay: true`；
- 通道**只会收到一条**消息。

只有 200 和 202 会被记录。窗口由 `queue.idempotency_retention` 决定（默认 24h）。
没有命中记录时不会带 `Idempotent-Replay` 头。

#### 3.1.4 状态码

| 状态码 | `error` | 触发 |
|---|---|---|
| 200 | | 同步路径返回 |
| 202 | | 异步路径已入队 |
| 400 | `invalid_request` | 不是合法 JSON / 有未知字段 / 超过 1 MiB / 校验不过（标题空、正文空、格式未知、级别未知、priority 越界）/ `targets` 为空 |
| 400 | `unknown_target` | 目标解析不出来（**仅异步路径**） |
| 401 | `unauthorized` | |
| 500 | `internal` | 响应无法编码 |
| 503 | `queue_unavailable` | 入队失败 |

---

### 3.2 `GET /api/v1/channels`

返回每个**已注册**的通道类型，以及哪些实例在用它。不看源码就能写出合法配置。

无请求体、无查询参数。

**200**

```json
{
  "channels": [
    {
      "type": "webhook",
      "configured_instances": ["hook"],
      "parameters": [ /* ParamSpec[] */ ],
      "capability": {
        "supported_formats": ["text", "markdown", "html"],
        "body_max_len": 4000,
        "body_max_bytes": 0,
        "title_max_len": 0,
        "support_attachment": false,
        "rate_per_sec": 5,
        "overflow_mode": "split",
        "markdown_dialect": "commonmark"
      }
    }
  ]
}
```

`configured_instances` 已排序，没有实例时是 `[]`。

**`parameters[]`（`ParamSpec`）**

| 字段 | 类型 | omitempty | 说明 |
|---|---|---|---|
| `name` | `string` | | 参数名 |
| `type` | `string` | | `string` \| `int` \| `bool` \| `enum` \| `string_list` \| `duration` \| `float` |
| `required` | `bool` | | |
| `private` | `bool` | | **凭据**。见下 |
| `default` | `any` | ✅ | |
| `values` | `[]string` | ✅ | `type: enum` 时的可选值 |
| `label` | `string` | ✅ | 表单标题 |
| `desc` | `string` | ✅ | 表单说明 |
| `show_if` | `object` | ✅ | `{"field": "...", "equals": <any>}`，只做等值判断 |
| `min` | `number` | ✅ | |
| `max` | `number` | ✅ | |

`show_if` 的语义：**该参数在 `field` 等于 `equals` 时才适用；适用时它必填，
除非它声明了 `default`**（空字符串默认值就是「适用但不必填」的表达）。

**`private: true` 的参数不出现在这个端点的任何位置**，而且：

- 它的 `default` 会被抹成 `null` 再序列化——密钥的默认值同样不公开；
- 它也不出现在**投递失败的响应和日志**里。传输错误原本会把整个 URL 带出来，
  而钉钉/飞书/Slack/企微把 token 放在 query、把 secret 放在 path，
  那几个通道的 URL 本身就是凭据。错误里只保留 `scheme://host`。

**状态码**：200；401。

---

### 3.3 `GET /api/v1/messages`

按创建时间倒序列出投递。这是查死信用的。

**查询参数**（全部可选）

| 参数 | 类型 | 说明 |
|---|---|---|
| `status` | `string` | `queued` \| `sending` \| `sent` \| `failed`。**不校验**——写错的值返回空列表，不报错 |
| `target` | `string` | 精确匹配 |
| `request_id` | `string` | 精确匹配，用来捞回一次扇出的全部投递 |
| `limit` | `int` | ≥ 0。默认与上限见 §7 |
| `offset` | `int` | ≥ 0，无上限 |

**200**

```json
{ "deliveries": [ /* deliveryView[] */ ] }
```

**`deliveryView`（公开版）**

| 字段 | 类型 | omitempty | 说明 |
|---|---|---|---|
| `id` | `string` | | 投递 ID |
| `request_id` | `string` | | |
| `target` | `string` | | |
| `channel_type` | `string` | | |
| `status` | `string` | | 见 §6.1 |
| `attempts` | `int` | | 已消耗的尝试次数 |
| `last_error` | `string` | ✅ | |
| `last_class` | `string` | ✅ | 大写分类，见 §6.2 |
| `created_at` | `time` | | RFC3339 |
| `updated_at` | `time` | | |
| `next_attempt_at` | `time` | ⚠️ | **总是出现**，见 §9 |
| `sent_at` | `time` | ✅ | 指针，没送达时真的不出现 |

**状态码**：200；400 `invalid_request`（limit/offset 不是非负整数）；401；500 `internal`。

---

### 3.4 `GET /api/v1/messages/{id}`

单个投递，含**完整尝试历史**。

**200**

```json
{
  "delivery": { /* deliveryView */ },
  "attempts": [
    {
      "attempt_no": 1,
      "class": "TRANSIENT",
      "detail": "peer said try later",
      "elapsed_ms": 42,
      "created_at": "2026-09-22T09:14:03Z",
      "channel_type": "email",
      "target": "oncall"
    },
    {
      "attempt_no": 2,
      "class": "NOT_ATTEMPTED",
      "skip_reason": "quota_exhausted",
      "elapsed_ms": 0,
      "created_at": "2026-09-22T09:15:03Z",
      "channel_type": "email",
      "target": "oncall"
    }
  ]
}
```

**`attemptView`**

| 字段 | 类型 | omitempty | 说明 |
|---|---|---|---|
| `attempt_no` | `int` | | 从 1 开始 |
| `class` | `string` | | **大写**，见 §6.2 |
| `detail` | `string` | ✅ | 对端返回的摘要，凭据已擦除 |
| `error` | `string` | ✅ | |
| `skip_reason` | `string` | ✅ | 见 §6.3 |
| `elapsed_ms` | `int64` | | |
| `created_at` | `time` | | |
| `channel_type` | `string` | | |
| `target` | `string` | | |

`attempts` 是 `[]`，不是 `null`。

**状态码**：200；401；404 `not_found`；500 `internal`。

---

## 4. 探针与指标

### `GET /healthz` — 存活

**200** `{"status":"ok"}`。**不查数据库**，没有别的状态码。

### `GET /readyz` — 就绪

**查数据库**。

| 状态码 | 响应体 |
|---|---|
| 200 | `{"status":"ready"}` |
| 503 | `{"error":"not_ready","message":"the delivery store is not reachable"}` |

超时 2 秒。没配置 `Pinger` 时永远 200。

**两者分开是有意的**：存活探针因依赖故障而失败会导致进程被反复重启——既修不好问题，
还会丢掉内存状态；就绪探针查依赖，是为了不在存不下的时候还接受通知。

### `GET /metrics` — Prometheus

无鉴权。仅在配置了指标时注册，否则路由不存在（404）。

| 指标 | 标签 | 看什么 |
|---|---|---|
| 队列深度 | `queued` / `sending` / `sent` / `failed` | `queued` 持续增长 = 投递跟不上，或某通道在熔断 |
| 按分类的尝试次数 | `channel_type`, `class` | `TRANSIENT` 涨 = 对端在拒绝；`CONNECT_ERROR` 涨 = 连不上 |
| 投递耗时 | `channel_type` | 尾部变长 = 对端变慢，该调 `timeouts.deliver` |
| 死信数 | `target`, `channel_type`, `reason` | |
| 归还次数 | `target`, `channel_type` | 无消耗归还的频率 |

**`NOT_ATTEMPTED` 涨说明通道没被调用**，和连不上是两回事——看审计里的 `skip_reason`
区分是熔断、配额还是限流。

---

## 5. 管理 API

全部挂在 `/admin` 下。下面写的是相对路径，实际是 `/admin/api/...`。

**所有** `/admin` 响应都带这些头：

```
Content-Security-Policy: default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
X-Frame-Options: DENY
```

请求体上限 1 MiB，未知字段同样被拒。解码失败一律压成 400 `invalid_request`，
**不会回传 JSON 解析细节**。

### 5.1 登录 / 登出 / 会话

#### `POST /admin/api/login` — 无需鉴权

```json
{ "username": "admin", "password": "..." }
```

**200**

```json
{ "actor": "admin", "expires_in_seconds": 43200 }
```

`expires_in_seconds` 是**配置的 TTL**（`admin.session_ttl`），不是剩余时间。
同时下发 `nr_admin` cookie。

| 状态码 | `error` | 触发 |
|---|---|---|
| 400 | `invalid_request` | 不是合法 JSON / 未知字段 / 超过 1 MiB |
| 401 | `invalid_credentials` | 用户名或密码错。**两者同一句话**，不泄露哪个错了 |
| 429 | `too_many_attempts` | 退避中，带 `Retry-After: <秒>`（最小 1） |
| 500 | `internal` | 会话创建失败 |

**退避规则**：按**来源 IP** 计数（不是按用户名）。前 5 次失败不惩罚，第 6 次起
1 秒、翻倍，上限 5 分钟。登录成功清空记录。表上限 1024 条，超了就整体清空。

#### `POST /admin/api/logout` — 无需鉴权

不读请求体。**永远 200** `{"signed_out": true}`。没有会话也不算错，
并用 `Max-Age=-1` 清掉 cookie。

#### `GET /admin/api/session`

**200**，形状同登录响应。用来让前端确认会话还有效。

#### `POST /admin/api/password` — 修改管理员密码

需要 CSRF 头。

```json
{ "current_password": "...", "new_password": "..." }
```

**200** `{"sessions_ended": 2, "message": "密码已修改，另外 2 个会话已被登出。"}`。

**要改的是会话自己的账号**，不是请求体里给的名字——请求体里根本没有名字。
后台只有一个账号，而一个能指定目标的改密接口就是改别人密码的接口。

**改完会踢掉其它所有会话。** 改密码的常见原因是「我觉得别人知道了」，
而留着对方的会话等于没做这件事。**发起改密的这个会话保留**，
否则运维会对着登录页，分不清是改成功了还是改失败了。

**走登录那套退避**：这个端点校验密码，所以它就是一个可以被猜的端点，
不能是唯一没有锁的那个。

| 状态码 | `error` | 触发 |
|---|---|---|
| 400 | `invalid_request` | JSON 错 / 新密码短于 8 位 |
| 401 | `invalid_credentials` | 当前密码不对（同样计入退避） |
| 409 | `password_in_config` | 这个账号的密码来自配置文件，不在这里改 |
| 409 | `no_credential` | 认证通过后账号行消失了 |
| 501 | `unavailable` | 本部署没有账号存储 |

> **`password_in_config` 不是失败，是实话。** 配置里写了 `admin.password_hash` 的部署，
> 改这里没有意义——重启之后配置里的那把又回来了。响应里说清楚了要改哪儿。
>
> **没有找回。** 忘了密码只能改数据库或者写配置重启。找回需要第二凭据或邮件通道，
> 两者这里都没有，草率做一个比写清楚「只能这样」更糟。

审计动作：`admin.password`。

### 5.2 渠道

#### `GET /admin/api/channels`

**200** `{"channels": [channelView]}`

| 字段 | 类型 | omitempty | 说明 |
|---|---|---|---|
| `name` | `string` | | |
| `type` | `string` | | |
| `enabled` | `bool` | | 存储里没写时视为 `true` |
| `config` | `object` | | **私密参数被整个删掉**，不是打码 |
| `quota` | `object` | | `per_second` / `per_minute` / `per_hour` / `per_day` / `per_month`，都是 `int`，0 = 不限 |
| `secrets_set` | `[]string` | ✅ | 当前有值的私密参数名，已排序 |
| `live` | `bool` | | 路由当前是否持有这个实例 |
| `updated_at` | `time` | ⚠️ | **总是 `0001-01-01T00:00:00Z`**，见 §9 |

**凭据不会回到浏览器**：`config` 里根本没有私密字段，改没改过只能从 `secrets_set` 看出来。
表单显示「已设置」和一个清除勾选框，值本身不出库。

#### `GET /admin/api/channels/types`

**200** `{"channels": [CatalogEntry]}`——和 `GET /api/v1/channels` 同一份目录，只是走管理鉴权。

#### `POST /admin/api/channels` — 新建或更新

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `name` | `string` | ✅ | 别名 |
| `type` | `string` | ✅ | 必须是已注册类型 |
| `enabled` | `bool` | | 省略视为启用 |
| `config` | `object` | | 省略的**私密**参数从已存配置**合并**——不传密码 = 不改密码，不是清空 |
| `quota` | `object` | | 同 `channelView.quota` |
| `editing` | `string` | | 调用方以为自己在做什么：新建表单传 `"__new__"`，编辑表单传渠道名，**不传表示「这就是我想要的状态」** |
| `replace` | `bool` | | 确认覆盖。只在 `editing="__new__"` 且名字已被占用时有意义 |

处理顺序是**合并 → 校验 → 落库 → 重载**。顺序很重要：先校验再合并的话，
只改一个 host 会被拒（报「username is set but password is not」），而表单从没拿到过密码，
**没有办法满足**。

**这个端点是 upsert，但重名会先问一句。** 不传 `editing` 时行为完全不变：
名字存在就覆盖，这是脚本想要的语义，也是这个端点一直以来的语义。传了
`editing="__new__"` 且名字已存在时，返回 409 而不是静默覆盖——运维点的是
「新建渠道」，得到的结果不该是「一条正在投递的渠道被换掉了」。确认后带
`replace: true` 重发即可。

| 状态码 | 响应体 | 触发 |
|---|---|---|
| 200 | `channelView` | 保存并重载成功 |
| 200 | `{"saved":true,"live":true}` | 保存成功、重载成功，但回读失败 |
| 202 | `{"saved":true,"live":false,"reload":"<错误>"}` | 已落库且合法，但路由拒绝了它 |
| 400 | `invalid_request` | JSON 错 / 未知字段 / 名字为空 |
| 400 | `invalid_config` | 配置校验失败，消息会点名参数，并另带一个 `fields` 映射（见下） |
| 400 | `save_failed` | 写库失败 |
| 409 | `name_taken` | `editing="__new__"` 且名字已存在、未带 `replace` |
| 500 | `internal` | 合并前后读取失败 |

> **202 是「存下来了但没生效」**，不是失败。响应里的 `reload` 说明为什么。
> 客户端应当把它当成功处理，但要提示运维去看日志。

**`invalid_config` 多带一个 `fields`。** 消息本身没变（它一直是 `errors.Join` 拼出来的，
用 `\n` 分隔），但**能归到某个参数名下的**那些会再出现一次，键是 schema 里的参数名：

```json
{
  "error": "invalid_config",
  "message": "unknown parameter(s) \"hots\" (known: ...)\nparameter \"host\" is required",
  "fields": { "host": "parameter \"host\" is required" }
}
```

后台表单用它把每条抱怨放到对应的输入框下面。**归不到单个字段的不会进 `fields`**——
未知的键、或者「设了 password 但没设 username」这种两个字段一起错的情况——
它们只留在 `message` 里。两个都发而不是只发结构化版本：`message` 是这个端点一直以来的
形状，忽略 `fields` 的客户端什么都不损失。

审计动作：`channel.create` / `channel.update`。

#### `POST /admin/api/channels/{name}/test-notification` — 发一条真实通知

需要 CSRF 头。请求体只有两个字段：

```json
{ "title": "测试通知", "body": "来自信使中枢的测试通知。" }
```

**202** `{"delivery_id": "...", "request_id": "..."}`。

它走的是**完整投递链路**：入队 → worker → 渠道 → 尝试记录。和
`POST /admin/api/channels/{name}/test` 的区别是根本性的：那个只回答「这个配置能不能被解析」，
而且对六种渠道类型里的五种**连网络都不会碰**；这个回答的是「通知到底会不会到」。

**它刻意不接受任何别的东西**：没有 target、没有优先级、没有定时、没有链接、没有 meta。
后台不是第二个发送入口——它只能做运维盯着渠道列表时本来就有权做的那件事。

消息会打上 `test` 标签（服务端打的，不接受客户端指定），事后能在投递列表里认出哪条是测试。
**进审计**（`channel.test_notification`），不像连通性测试那样不进——这条真的发了东西出去。

| 状态码 | 错误码 | 触发 |
|---|---|---|
| 404 | `not_found` | 没有这个渠道 |
| 409 | `channel_disabled` | 渠道已禁用，投递会一直躺在队列里 |
| 400 | `invalid_message` | 标题或正文为空 |
| 501 | `queue_disabled` | 本部署没有投递队列 |
| 503 | `queue_unavailable` | 入队失败 |

#### `GET /admin/api/channels/{name}`

**200** 单个 `channelView`。404 `not_found`；500 `internal`。

#### `DELETE /admin/api/channels/{name}`

需要 CSRF 头。

| 状态码 | 响应体 |
|---|---|
| 200 | `{"deleted":true,"live":true}` |
| 202 | `{"deleted":true,"live":false,"reload":"<错误>"}` |
| 404 | `not_found` |
| 500 | `internal` |

审计动作：`channel.delete`。

#### `POST /admin/api/channels/{name}/test` — 连通性测试

需要 CSRF 头。无请求体。用**已存配置**新建一个实例并真的发一条（20 秒超时）。

**200**

```json
{ "channel": "oncall", "class": "SENT", "ok": true, "detail": "200 OK", "error": "" }
```

- `class` 是**大写**的结果分类——和 notify 响应里的小写不一样，见 §9；
- `ok` 严格等于 `class == "SENT"`；
- `error` 没有错误时是空字符串 `""`，不是 `null`；
- `detail` 是对端响应摘要，已擦除凭据。

404 `not_found`。**测试结果不进审计**——否则每点一次按钮就污染一条记录。

#### `POST /admin/api/channels/{name}/breaker/reset` — 手动重置熔断器

需要 CSRF 头。无请求体。下游恢复或运维重启了下游之后用它，
不必再等 `open_timeout`。

| 状态码 | 响应体 |
|---|---|
| 200 | `{"channel":"oncall","was":"open","state":"closed"}` |
| 404 | `not_found`——渠道必须存在 |
| 501 | `{"error":"breaker_disabled", ...}`——本部署没开熔断器 |

`was` ∈ `closed` \| `open` \| `half_open`。审计动作：`breaker.reset`，
`detail` 里记着之前的状态。

### 5.3 投递

#### `GET /admin/api/deliveries`

查询参数与校验规则和 `GET /api/v1/messages` 完全一致（`status` / `target` /
`request_id` / `limit` / `offset`）。

**200** `{"deliveries": [deliveryView]}`——比公开版多一个字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `replayable` | `bool` | `status == "failed"` **且**正文还在盘上 |

没配投递存储时返回 200 `{"deliveries": []}`。

#### `GET /admin/api/deliveries/{id}`

**200** `{"delivery": deliveryView, "attempts": [attemptView]}`，形状同公开版。

| 状态码 | `error` |
|---|---|
| 404 | `not_found` |
| 501 | `unavailable`——本部署没有投递存储 |
| 500 | `internal` |

#### `POST /admin/api/deliveries/{id}/replay` — 死信重放

需要 CSRF 头。把投递放回队列并**重置重试预算**，尝试历史保留。

**200** `{"replayed":true,"id":"<id>"}`

| 状态码 | `error` | 触发 |
|---|---|---|
| 404 | `not_found` | |
| 409 | `not_replayable` | 状态不是 `failed`（消息里会说是哪个状态） |
| 409 | `not_replayable` | 比较并交换时竞争失败 |
| 409 | `body_expired` | 正文被保留策略清掉了，**行还在但没东西可发** |
| 501 | `unavailable` | |
| 500 | `internal` | |

只对 `failed` 可用——重放一条 `sent` 的投递等于给真人发第二条。

`body_expired` 的解法是调大 `queue.failed_retention`（默认 30 天），代价是磁盘。

审计动作：`delivery.replay`。

### 5.4 统计与审计

#### `GET /admin/api/stats`

**200**——注意键名是 **Go 字段名，首字母大写**：

```json
{ "Queued": 0, "Sending": 0, "Sent": 0, "Failed": 0 }
```

没有投递存储时返回四个 0。500 `internal`。

#### `GET /admin/api/audit`

**200** `{"actions": [auditView]}`，最近 200 条，倒序。

| 字段 | 类型 | omitempty |
|---|---|---|
| `at` | `time` | |
| `actor` | `string` | |
| `action` | `string` | `channel.create` / `channel.update` / `channel.delete` / `breaker.reset` / `delivery.replay` |
| `target` | `string` | ✅ |
| `detail` | `string` | ✅ |

> 没配审计存储时返回的是 `{"actions":null}` 而不是 `{"actions":[]}`，见 §9。

审计保留期**不受投递保留策略影响**——后台做的每一件改状态的事都留痕。

---

## 6. 枚举值

### 6.1 投递状态 `status`

| 值 | 含义 |
|---|---|
| `queued` | 在队列里等 |
| `sending` | 被 worker 领走了 |
| `sent` | 送达 |
| `failed` | 死信 |

终态是 `sent` / `failed`。**只有 `failed` 可重放。**

### 6.2 结果分类 `class` —— ⚠️ 两种大小写

这是最容易踩的一处：**同一个枚举，在不同端点上大小写不同。**

| 场景 | 形式 | 取值 |
|---|---|---|
| `POST /api/v1/notify` 的 `results[].status` | **小写**（`Wire()`） | `not_attempted` `sent` `connect_error` `transient` `permanent` |
| 尝试历史里的 `class` | **大写**（`String()`） | `NOT_ATTEMPTED` `SENT` `CONNECT_ERROR` `TRANSIENT` `PERMANENT` |
| 投递的 `last_class` | **大写** | 同上 |
| `channels/{name}/test` 的 `class` | **大写** | 同上 |
| `/metrics` 的标签 | **大写** | 同上 |

另有两个不由 `ResultClass` 产生的尝试分类：

| 值 | 含义 |
|---|---|
| `RELEASED` | 这次尝试既没投递也没消耗预算（通道不可达 / 配额 / 限流导致的归还） |
| `PAYLOAD_MISSING` | 正文文件读不出来 |

**五分类的语义与队列行为见 README 的核心设计一节**，本文不重复。

### 6.3 `skip_reason`

只在**通道根本没被调用**时出现，恰好三个值：

| 值 | 含义 | 该做什么 |
|---|---|---|
| `breaker_open` | 通道已知不健康，暂时停用 | 查下游。恢复后到后台点「重置熔断器」，不用等 `open_timeout` |
| `quota_exhausted` | 该通道窗口额度用尽 | 等窗口滚动，或调高配额 |
| `rate_limited` | 等不到限流令牌 | 限流太紧，或积压太多 |

> **不变式：`skip_reason` 出现 ⟺ `class` 是 `NOT_ATTEMPTED`（小写 `not_attempted`）。**
> 一条被切成两段、第一段发出去、第二段被配额挡下的投递**不报**这个字段——
> 通道确实收到了消息，折叠结果时「未尝试」让位于真实结果。

SMTP 入口对它的回复码是 **451**（让发信方重投），不是 550。

### 6.4 熔断状态

`closed` \| `open` \| `half_open`，出现在重置熔断器的 `was` 字段里。

---

## 7. 分页

`GET /api/v1/messages` 与 `GET /admin/api/deliveries` 共用同一套规则，
**由存储层实现**：

```go
if limit <= 0 || limit > 1000 { limit = 100 }
```

| 传入 `limit` | 实际返回 |
|---|---|
| 省略 | 100 |
| `0` | 100 |
| `1`–`1000` | 原样 |
| `1001` 及以上 | **100**，不是 1000 |

> ⚠️ 超过 1000 不是「截到 1000」，而是**退回默认的 100**。传 `limit=5000`
> 拿到的比传 `limit=1000` 少。见 §9。

`offset` 无上限，只校验 ≥ 0。排序恒为 `created_at DESC`。

**后台的 HTML 页面 `/admin/deliveries` 用的是另一套规则**：`limit` 在
`0 < n <= 500` 时才生效，否则退回 **50**。这是页面自己的分页，和 API 无关。

---

## 8. 响应头汇总

| 头 | 值 | 出现位置 |
|---|---|---|
| `Content-Type` | `application/json; charset=utf-8` | 所有 JSON 响应 |
| `Content-Type` | `text/html; charset=utf-8` | 后台页面 |
| `Idempotent-Replay` | `true` | `POST /api/v1/notify` 命中幂等记录时 |
| `WWW-Authenticate` | `Bearer realm="notifyrelay"` | 公开 API 的 401 |
| `Retry-After` | 整数秒，最小 1 | 仅登录 429 |
| `Set-Cookie` | `nr_admin=...` | 登录 / 登出 |
| CSP、`X-Content-Type-Options`、`Referrer-Policy`、`X-Frame-Options` | 见 §5 | 所有 `/admin` 响应 |
| `Location` | `/admin/channels` 或 `/admin/login` | **仅** HTML 路由的 302，JSON 端点从不设置 |

---

## 9. 已知的不一致

以下每一条都是**当前代码的真实行为**，不是设计意图。写在这里是因为按直觉写客户端会出错。
它们都还没修——修任何一条都是破坏性变更，需要单独决策。

| # | 现象 | 影响 | 位置 |
|---|---|---|---|
| 1 | `class` 在 notify 响应里小写、在尝试历史里大写 | 客户端要写两套映射，或统一 `strings.ToUpper` | §6.2 |
| 2 | `next_attempt_at` 标了 `omitempty` 但类型是非指针 `time.Time`，`encoding/json` 不认为结构体是空 | **总是出现**，没有下次尝试时是 `"0001-01-01T00:00:00Z"`，看着像公元 1 年 | `internal/api/messages.go` |
| 3 | `channelView.updated_at` 同样的问题，**而且处理器根本没给它赋值** | 永远是 `"0001-01-01T00:00:00Z"`，是个纯占位 | `internal/admin/channels.go` |
| 4 | `limit > 1000` 退回 100 而不是截到 1000 | 传大值反而拿得少 | §7 |
| 5 | `recipients[]` 没有 json tag，`Class` 是数字 0–4 | 唯一一处 Go 字段名外泄 + 唯一一处数字枚举 | §3.1.2 |
| 6 | `GET /admin/api/stats` 的键是 `Queued`/`Sending`/... 大写 | 和其他所有端点的 snake_case 不一致 | §5.4 |
| 7 | 没有审计存储时 `/admin/api/audit` 返回 `{"actions":null}`，其余端点返回 `[]` | 客户端要同时处理 `null` 和 `[]` | §5.4 |
| 8 | `X-NotifyRelay-Admin` 只查存在、不比值 | 别把它当第二把凭据；它防的是 CSRF，不是未授权 | §1.2 |
| 9 | `status` 查询参数不校验 | 拼错的状态返回空列表而不是 400，排障时会以为「真的没有」 | §3.3 |

> 第 2、3、7 条是「读起来像有值、其实是零值」的那一类，最费时间——它们不会报错，
> 只是让客户端显示一个公元 1 年的时间。

---

## 10. 相关文档

| | |
|---|---|
| `/admin/api-docs` | 后台内的同一份接口说明，带七种语言的调用示例 |
| `README.md` | 快速开始、五分类与投递保证、通道表、配置三规则 |
| `docs/06-operations.md` | 备份、升级、热加载、指标解读、排障、未验证清单 |
| `docs/02-scope.md` | 功能边界与安全红线 |
| `docs/05-paramschema-audit.md` | `ParamSchema` 的字段为什么是这些——表单能生成到什么程度 |
