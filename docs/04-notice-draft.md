# 04 · NOTICE 文件内容草案

> 用途：M0 建 `NOTICE` 文件时，内容以此为准。**待你确认后再落盘为仓库根目录的 `NOTICE`。**
> 待填项已用 `【】` 标出。

---

## 落盘位置与配套文件

| 文件 | 内容 |
|---|---|
| `NOTICE` | 署名与归属声明（本草案正文） |
| `LICENSE` | 本项目自身 License（**待 Q10 确认**） |
| `third_party/licenses/` | 第三方完整 License 原文（Apache-2.0 / MIT 全文），M1 真正发生移植时创建 |

**为什么要 `third_party/licenses/`**：Apache-2.0 第 4(d) 条要求衍生作品随附归属声明，工程惯例是把完整 License 原文单独存放，`NOTICE` 只放归属摘要。

---

## `NOTICE` 正文草案

```
NotifyRelay（通知中继）
Copyright (C) 2026 【组织名称】

This product includes software developed by third parties.

--------------------------------------------------------------------------------
THIRD-PARTY ATTRIBUTIONS
--------------------------------------------------------------------------------

1. kubesphere/notification-manager
   License : Apache License 2.0
   Source  : https://github.com/kubesphere/notification-manager
   Files   : pkg/notify/notifier/email/email.go
   Used in : internal/channel/email/compose.go

   Ported content (message composition & encoding logic ONLY):
     - MIME subject encoding for non-ASCII (Chinese) subjects, via mime.QEncoding
     - multipart/alternative body construction (HTML + auto-generated plain text)
     - quoted-printable body encoding

   NOT ported: the upstream SMTP client implementation (dialing, TLS negotiation,
   AUTH mechanisms). This project uses github.com/wneessen/go-mail for transport.

   Modifications: the ported logic was adapted to NotifyRelay's Channel interface
   and Message model. See the header comment of internal/channel/email/compose.go.


2. bougou/alertmanager-webhook-adapter
   License : Apache License 2.0
   Source  : https://github.com/bougou/alertmanager-webhook-adapter
   Files   : pkg/models/templates/, pkg/webhook-adapter/models/payload.go
   Used in : internal/channel/<im>/templates.go          [M3 — 尚未发生]

   Ported content:
     - Chinese-language message templates for DingTalk / Feishu / WeCom
     - Payload intermediate representation (Links / Buttons / At / Severity)

   Status: NOT YET PORTED. This section describes the planned M3 port and must be
   updated (or removed) when M3 is actually implemented.


3. Third-party Go dependencies
   The following libraries are linked into the binary. Full license texts are
   available in third_party/licenses/.

     github.com/wneessen/go-mail        MIT License
     github.com/emersion/go-smtp        MIT License
     github.com/go-chi/chi              MIT License
     gopkg.in/yaml.v3                   MIT License / Apache License 2.0

   [依赖清单在每次新增依赖时同步更新。M0 只列已确定的项。]


4. Design references (NO code copied)
   The following projects influenced this project's DESIGN only. No source code
   was copied from them.

     caronc/apprise                    BSD 2-Clause   — channel registry design
     saderi/SMTP-Switch                 MIT            — retry/queue mechanism design
     YoRyan/mailrise                    MIT            — SMTP recipient-routing syntax
     kubesphere/notification-manager    Apache-2.0     — Factory registry design
     caronc/apprise-api                 MIT            — /channels metadata endpoint
     idealista/prom2teams               Apache-2.0     — byte-based message splitting

   NOTE: alphorn-dev/alphorn and hyvor/relay are AGPL-3.0. They were read for
   architectural understanding ONLY. NO code, schema, or template was copied from
   them. This must remain true.
```

---

## 需要你决定/确认的点

| # | 待办 | 说明 |
|---|---|---|
| 1 | **【组织名称】** | 版权行需要。可以填公司名，也可以写 "The NotifyRelay Authors" |
| 2 | **Q10：本项目自身 License** | 影响 `LICENSE` 文件。**内部项目若不分发可暂不建**，但 `NOTICE` 里的第三方署名**必须保留**——Apache-2.0 的署名义务与你自己用什么 License 无关 |
| 3 | **第 2 条的"计划性条目"** | 我按"提前声明 M3 会移植什么"写了一段。**如果你希望 NOTICE 只记录已发生的事实**，M0 就先删掉这段，M3 移植时再加。我倾向后者（避免声明与实际不符），但草案里保留了完整形态供你选 |
| 4 | **设计参考清单要不要写** | 严格说法律上不要求（没抄代码），但写上去是诚实的做法，也能防止后人误以为可以抄。**代价是文件变长**。我建议保留，尤其是 AGPL 那两行的"仅阅读未抄写"声明——**这是将来最容易被质疑的地方，留个字据** |

---

## 来源注释模板（M1 移植代码文件头用）

```go
// Copyright 2023 The KubeSphere Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// ---------------------------------------------------------------------------
// This file is derived from:
//   Repository : github.com/kubesphere/notification-manager
//   File       : pkg/notify/notifier/email/email.go
//   License    : Apache-2.0
//
// Ported content: message composition & encoding logic only
//   (mime.QEncoding subject, multipart/alternative body, quoted-printable).
//   The upstream SMTP client is NOT ported; see NOTICE for details.
//
// Modifications: adapted to NotifyRelay's Channel interface and Message model.
// ---------------------------------------------------------------------------
```

**说明**：Apache-2.0 §4(b) 要求修改过的文件带显著修改声明，§4(c) 要求保留原始版权声明——上面这个模板同时满足两条。**不要**只写一行 `// from notification-manager`，那样不满足署名要求。

---

## 什么时候必须更新 NOTICE

| 触发条件 | 动作 |
|---|---|
| 移植/抄写任何外部代码 | 加归属段落 + 在该文件头加来源注释 |
| 新增 Go 依赖 | 第 3 节加一行（License 需核实，**不采信 README 自述**） |
| 读了 AGPL/GPL 项目并"参考了实现思路" | 第 4 节登记，并明确写清"未抄写代码" |
| 复制了外部项目的模板/配置样例 | 视为代码，按第 1 条处理 |

**检查点**：每次代码评审都过一遍这个表（已写入 `03-plan.md` 的 M0 任务清单）。
