# 后台界面文案清单（i18n inventory）

> 目的：把 `internal/admin` 下所有面向用户的字符串逐条数出来，作为 `internal/admin/i18n`
> 消息表的设计依据。每一条都对应代码里的真实字符串，行号对 `E:\opt\NotifyRelay` 当前工作区核过。
>
> 计划背景见 `docs/08-ui-plan.md` 第 1、2 段。

---

## 1. 数量

去重后的、面向用户的字符串条数。同一句话出现在多处只算一条（例如 "Delete" 在渠道表和密钥表
共用一个 `CommonDelete`）。

跨文件复用的字符串（`Common*` 22 条、`Title*` 8 条）单独成行，不重复计入各页面。

| 文件 | 条数 | 说明 |
|---|---:|---|
| `templates/layout.html` | 7 | 品牌 + 5 个导航 + 退出登录 |
| `templates/login.html` | 4 | |
| `templates/setup.html` | 6 | |
| `templates/channels.html` | 25 | 含 4 个表头、3 个状态标签、配额表单区块 |
| `templates/deliveries.html` | 21 | 含筛选栏、2 个分页按钮、4 个状态枚举 |
| `templates/delivery.html` | 19 | 含 9 个 `<dt>` 字段名、5 个尝试表表头 |
| `templates/audit.html` | 10 | |
| `templates/keys.html` | 18 | 含对话框、配置来源提示 |
| `templates/apidocs.html` | 22 | 已有双语，`{{if .Zh}}` 全部换成消息表 |
| `templates/error.html` | 0 | 模板本身无字面量，文案来自 Go 的 `BackLabel` |
| 共享按钮与通用词（`Common*`） | 22 | Save / Cancel / Delete / Test / Copy / Done / … |
| 页面标题（`Title*`） | 8 | 由 `pageBase` 和 `apidocs.go` 生成 |
| `ui.go` | 18 | 4 条相对时间 + 6 条返回链接 + 8 条 `renderError` |
| `channels.go` | 7 | `writeError` |
| `deliveries.go` | 6 | `writeError` |
| `keys.go` | 10 | `writeError`（含 1 条 `// ?`） |
| `setup.go` | 8 | `writeError` + 1 条 `renderError` |
| `auth_handlers.go` | 2 | `writeError` |
| `admin.go` | 3 | 会话 / CSRF 中间件的 `writeError` |
| `apidocs.go` | 2 | 示例页签的依赖说明（另 5 条技术型见 §4） |
| `static/app.js` | 18 | `alert` / `confirm` / `say` / `prompt` / flash 拼接 |
| `samples/*.txt` | 63 个注释块（去重后约 37） | 见 §5，不逐条列出，不进结构体 |
| **合计** | **236** | 见 §2 的 `Messages` 结构体，字段数已核对 |

### 与 `08-ui-plan.md` 估算的差异

计划里估的是 ~212。实际落到 236，多出来的部分来自：

1. **空状态拆分**（计划 §3.1）——把 7 处空状态拆成 title / body / action，比原来的单句多出
   12 个字段，且投递页要分「从没发过」和「筛选无结果」两种。
2. **`writeError` 去重后是 44 条**，不是计划里的 42 条（`keys.go` 的
   `"this is the only time the token is shown; it is not recoverable"` 和
   `channels.go` 的 `"the channel could not be built from its configuration"` 是两条容易被漏掉的）。
3. **`apidocs.go` 的 14 条端点/错误码说明**没有进结构体（见下），否则还要再涨 14。

### 不在结构体里的两组

* **API 文档的端点表和错误码表**（`apidocs.go` 的 `apiEndpoints` / `apiErrors`，14 条）。
  它们是「行数据」而不是「页面文案」——每条和 `Method`/`Path`/`Code`/`Status` 绑在一起，
  拆成 14 个平铺字段后表格反而更难读。建议在 `i18n` 包里单独给一张
  `var APIDocs = map[string]APIDocRows{...}`（或 `func APIDocs(lang string) ([]Endpoint, []Error)`），
  把现有 `Zh` / `En` 两个字段的值原样搬进去。**现有的中文译文直接复用，不要重译。**
* **渠道 schema 的字段标签与说明**（`internal/channel/*/config.go` 里的 `Label` / `Desc`，
  60 + 50 = 110 条）。它们通过 `fieldView.Label` / `.Description` 渲染进渠道表单，
  确实是用户可见文案，但在 `internal/channel` 包内、不在本次清点范围。详见 §5。

---

## 2. 建议的 `Messages` 结构体

用结构体而不是 map：漏翻一条是编译错误，不是运行时空白。

```go
// Package i18n holds every user-facing string in the operator UI.
//
// Messages is one language's complete copy table. It is a struct rather than a
// map so that a missing translation is a compile error: a map lookup that
// misses returns "" and renders as a blank label nobody notices until a
// customer does.
package i18n

import "html/template"

// Messages is the full copy table for one language.
//
// Fields whose name does not say where they are used carry a one-line comment.
// A field marked `// HTML` carries markup (<code>, <strong>, <em>) and must be
// rendered unescaped — see the note at the end of this section.
// A field marked `// ?` is included on the "probably user-facing" side of a
// judgement call and is worth a second look before it is locked in.
type Messages struct {
	// ------------------------------------------------------------ brand
	AppName string // 品牌名，同时是 <title> 的尾缀

	// ------------------------------------------------------- navigation
	NavChannels   string
	NavDeliveries string
	NavAudit      string
	NavKeys       string
	NavAPI        string
	NavSignOut    string

	// ----------------------------------------------------- page titles
	// Also used as the <h1> where the two are the same string.
	TitleSignIn     string
	TitleSetup      string
	TitleChannels   string
	TitleDeliveries string
	TitleDelivery   string // 投递详情页
	TitleAudit      string
	TitleKeys       string
	TitleAPI        string

	// ---------------------------------------------------- shared copy
	CommonSave        string
	CommonCancel      string
	CommonDelete      string
	CommonTest        string
	CommonCopy        string
	CommonDone        string
	CommonCreate      string
	CommonEnable      string
	CommonDisable     string
	CommonFilter      string
	CommonClear       string
	CommonDetail      string
	CommonReplay      string
	CommonBack        string
	CommonNewer       string // 投递列表分页：更新的
	CommonOlder       string
	CommonAny         string // 筛选项的空值提示
	CommonNever       string // 密钥「从未使用」
	CommonEnabled     string // 状态标签
	CommonDisabled    string
	CommonRequired    string // 必填星号的 title 提示
	CommonSetInConfig string // 密钥来源列：由配置文件固定

	// ------------------------------------------------------------ login
	LoginSubtitle string // 登录卡片副标题
	LoginUsername string
	LoginPassword string
	LoginSubmit   string

	// ------------------------------------------------------------ setup
	SetupIntro        string // 首次运行说明段落
	SetupUsername     string
	SetupPassword     string
	SetupConfirm      string
	SetupSubmit       string
	SetupPasswordRule string // 密码规则说明段落

	// ------------------------------------------- error page back links
	BackToChannels   string
	BackToDeliveries string
	BackToAudit      string
	BackToKeys       string
	BackToAPI        string
	BackToSetup      string

	// --------------------------------------------------------- channels
	ChannelsNew            string // 页头按钮，也是空状态里的行动按钮
	ChannelsTableName      string
	ChannelsTableType      string
	ChannelsTableState     string
	ChannelsTableSecrets   string
	ChannelsTagDisabled    string
	ChannelsTagLive        string
	ChannelsTagNotLoaded   string
	ChannelsResetBreaker   string
	ChannelsEmptyTitle     string
	ChannelsEmptyBody      string
	ChannelsEmptyAction    string
	ChannelFormEditHeading string // "Edit %s" — %s 是渠道名
	ChannelFormNameLabel   string
	ChannelFormNameDesc    string
	ChannelFormTypeLabel   string
	ChannelFormTypeHint    string // 类型下拉的占位项
	ChannelFormTypeDesc    string
	ChannelFormEnabled     string
	ChannelFormConfigHead  string // 「Configuration」小标题
	ChannelFormSecretHint  string // 私密参数的 placeholder
	ChannelFormSecretClear string // 「clear this value」勾选项
	ChannelFormQuotaHead   string // 「Allowance」小标题
	ChannelFormQuotaDesc   string
	ChannelFormTestConn    string

	// ------------------------------------------------------- deliveries
	DeliveriesStats           string // %d 排队 · %d 发送中 · %d 已送达 · %d 失败
	DeliveriesFilterStatus    string
	DeliveriesFilterChannel   string
	DeliveriesFilterRequestID string
	DeliveriesFilterLimit     string
	DeliveriesTableCreated    string
	DeliveriesTableChannel    string
	DeliveriesTableStatus     string
	DeliveriesTableClass      string
	DeliveriesTableAttempts   string
	DeliveriesTableLastError  string
	DeliveriesEmptyTitle      string // 一条都没发过
	DeliveriesEmptyBody       string
	DeliveriesEmptyAction     string
	DeliveriesNoMatchTitle    string // 有筛选条件但没匹配
	DeliveriesNoMatchBody     string
	DeliveriesNoMatchAction   string
	StatusQueued              string // ? 投递状态标签。value="" 仍是英文枚举
	StatusSending             string // ?
	StatusSent                string // ?
	StatusFailed              string // ?

	// -------------------------------------------------- delivery detail
	DeliveryFactID           string
	DeliveryFactRequest      string
	DeliveryFactChannel      string
	DeliveryFactStatus       string
	DeliveryFactAttempts     string
	DeliveryFactCreated      string
	DeliveryFactSent         string
	DeliveryFactNextAttempt  string
	DeliveryFactLastError    string
	DeliveryBodyExpired      string // 消息体已过期，不能重投
	DeliveryNotDeadLetter    string // 非死信，不能重投
	DeliveryAttemptsHeading  string
	DeliveryAttemptWhen      string
	DeliveryAttemptClass     string
	DeliveryAttemptSkipped   string
	DeliveryAttemptDetail    string
	DeliveryAttemptError     string
	DeliveryEmptyTitle       string // 零尝试记录
	DeliveryEmptyBody        string // 解释熔断 / 配额 / 限流三种原因

	// ------------------------------------------------------------ audit
	AuditIntro        string
	AuditTableWhen    string
	AuditTableWho     string
	AuditTableAction  string
	AuditTableChannel string
	AuditTableDetail  string
	AuditDisabledHead string // 本部署未启用审计
	AuditDisabledBody string
	AuditEmptyHead    string // 已启用但还没有记录
	AuditEmptyBody    string

	// ------------------------------------------------------------- keys
	KeysIntro            string
	KeysNewHeading       string
	KeysNameLabel        string
	KeysNameHint         string // ? 输入框示例值
	KeysCreateButton     string
	KeysNameDesc         string
	KeysConfiguredNotice string // %d 个密钥来自配置文件
	KeysTableName        string
	KeysTableSource      string
	KeysTableStatus      string
	KeysTableCreated     string
	KeysTableLastUsed    string
	KeysEmptyTitle       string
	KeysEmptyBody        string // HTML（含 <code>401</code>）
	KeysDialogHeading    string // 「Copy this now」
	KeysDialogBody       string
	KeysDialogCopy       string
	KeysDialogDone       string

	// --------------------------------------------------------- api docs
	APIDocsIntro           string // HTML
	APIDocsBaseURL         string
	APIDocsAuth            string
	APIDocsBaseURLNote     string
	APIDocsTokenNote       string // HTML
	APIDocsPlainHTTP       string // HTML
	APIDocsExamplesHeading string
	APIDocsExamplesIntro   string // HTML
	APIDocsCopy            string
	APIDocsEndpointsHead   string
	APIDocsTableMethod     string
	APIDocsTablePath       string
	APIDocsTableAuth       string
	APIDocsTablePurpose    string
	APIDocsAuthNone        string
	APIDocsErrorsHeading   string
	APIDocsErrorBodyNote   string // HTML
	APIDocsTableStatus     string
	APIDocsTableMeaning    string
	APIDocsGotchasHeading  string
	APIDocsGotchaClassCase string // HTML
	APIDocsGotchaNotAtt    string // HTML
	APIDocsSampleNoteCurl  string // ? 示例页签右侧的依赖说明
	APIDocsSampleNoteGo    string // ?

	// ------------------------------------------------- relative time
	TimeJustNow    string
	TimeMinutesAgo string // %d
	TimeHoursAgo   string // %d
	TimeDaysAgo    string // %d

	// ---------------------------------------------------- error copy
	ErrChannelsUnreadable   string
	ErrChannelUnreadable    string
	ErrChannelNotFound      string
	ErrDeliveriesUnreadable string
	ErrNoDeliveryStore      string
	ErrDeliveryUnreadable   string
	ErrDeliveryNotFound     string
	ErrAttemptsUnreadable   string
	ErrAuditUnreadable      string
	ErrInvalidJSON          string
	ErrChannelNameRequired  string
	ErrChannelNameTaken     string // %s 是渠道名
	ErrChannelDeleteFailed  string
	ErrBreakerDisabled      string
	ErrChannelBuildFailed   string // 渠道探测失败时的 detail
	ErrInvalidIntParam      string // %s 是参数名（limit / offset）；仅 JSON API
	ErrNotReplayableStatus  string // %s 是当前状态
	ErrBodyExpired          string
	ErrReplayFailed         string
	ErrNotReplayable        string
	ErrStatsUnreadable      string
	ErrKeysUnreadable       string
	ErrNoKeyStore           string
	ErrKeyNameRequired      string
	ErrKeyCreateFailed      string
	ErrKeyEnabledRequired   string
	ErrKeyUnreadable        string
	ErrKeyNotFound          string
	ErrKeyUpdateFailed      string
	ErrKeyDeleteFailed      string
	ErrTokenOnce            string // ? 只出现在创建密钥的 JSON 响应里，UI 不读它
	ErrAdminUnreadable      string
	ErrTooManyAttempts      string // 首次运行页
	ErrUsernameRequired     string
	ErrPasswordTooShort     string // %d 是最小长度
	ErrNoCredentialStore    string
	ErrAccountCreateFailed  string
	ErrAlreadyConfigured    string
	ErrSessionCreateFailed  string
	ErrTooManySignIns       string // 登录页
	ErrBadCredentials       string
	ErrSignInFirst          string
	ErrSessionExpired       string
	ErrMissingCSRF          string // %s 是头名（X-NotifyRelay-Admin）

	// ------------------------------------------- JavaScript-only copy
	// 这些字符串服务端模板够不着，通过页面里的 <script type="application/json"
	// id="i18n"> 数据块交给 app.js。%s 的插值在 JS 侧拼接。
	JSSavedButNotLive     string // %s 是 reload 失败原因
	JSSavedFlash          string // %s 是渠道名
	JSConfirmReplace      string // 重名确认的第二段
	JSChooseTypeFirst     string
	JSSaveBeforeTest      string
	JSTestOKPrefix        string // %s 是探测详情
	JSConfirmResetBreaker string // %s 是渠道名
	JSBreakerResetFlash   string // %s 渠道名，第二个 %s 是原状态
	JSConfirmDeleteChan   string // %s 是渠道名
	JSDeletedFlash        string // %s 是被删对象名
	JSConfirmDeleteKey    string // %s 是密钥名
	JSConfirmReplay       string // %s 是投递 ID
	JSReplayedFlash       string // %s 是投递 ID
	JSKeyNameRequired     string
	JSCopyTokenPrompt     string
	JSPasswordsNoMatch    string
	JSSetupFailed         string // 首次运行表单的兜底错误
	JSCopyManualPrompt    string
}
```

### 两个必须一起解决的技术点

1. **`// HTML` 字段不能用 `string`。** `APIDocsIntro`、`APIDocsTokenNote`、`APIDocsPlainHTTP`、
   `APIDocsExamplesIntro`、`APIDocsErrorBodyNote`、`APIDocsGotchaClassCase`、`APIDocsGotchaNotAtt`、
   `KeysEmptyBody` 这 8 条里含 `<code>` / `<strong>` / `<em>`。若声明为 `string` 并用
   `{{.T.KeysEmptyBody}}` 渲染，`html/template` 会把标签转义成字面量显示。三条出路：
   字段改 `template.HTML`（`i18n` 包要 import `html/template`，且要保证文案是我们自己写的、
   不含用户输入）；或者把标记拆回模板、消息表里只留纯文本；或者拆成多个字段由模板拼。
   **建议第一种**，并在 `i18n` 包里加一条注释说明为什么这里的 `template.HTML` 是安全的。
2. **`%s` 的传参方式。** 模板里要用 `{{printf .T.ChannelFormEditHeading .Editing}}` 这类写法。
   如果嫌啰嗦，可以在 `FuncMap` 里按语言注册一个 `T` 闭包（计划 §1 已经要为 `since`/`stamp`
   做同样的事），把 `{{T "ChannelFormEditHeading" .Editing}}` 暴露给模板。

---

## 3. 两张表

`en` 是当前代码里的原文，逐字照抄，没有润色。`zh` 是译文。

```go
// zh is the Simplified Chinese copy table. Default language.
var zh = Messages{
	// ------------------------------------------------------------ brand
	AppName: "信使中枢",

	// ------------------------------------------------------- navigation
	NavChannels:   "渠道",
	NavDeliveries: "投递",
	NavAudit:      "审计",
	NavKeys:       "密钥",
	NavAPI:        "API",
	NavSignOut:    "退出登录",

	// ----------------------------------------------------- page titles
	TitleSignIn:     "登录",
	TitleSetup:      "创建管理员",
	TitleChannels:   "渠道",
	TitleDeliveries: "投递",
	TitleDelivery:   "投递详情",
	TitleAudit:      "审计",
	TitleKeys:       "密钥",
	TitleAPI:        "API",

	// ---------------------------------------------------- shared copy
	CommonSave:        "保存",
	CommonCancel:      "取消",
	CommonDelete:      "删除",
	CommonTest:        "测试",
	CommonCopy:        "复制",
	CommonDone:        "完成",
	CommonCreate:      "创建",
	CommonEnable:      "启用",
	CommonDisable:     "禁用",
	CommonFilter:      "筛选",
	CommonClear:       "清除",
	CommonDetail:      "详情",
	CommonReplay:      "重投",
	CommonBack:        "返回",
	CommonNewer:       "更新",
	CommonOlder:       "更早",
	CommonAny:         "不限",
	CommonNever:       "从未",
	CommonEnabled:     "已启用",
	CommonDisabled:    "已禁用",
	CommonRequired:    "必填",
	CommonSetInConfig: "在配置文件中设置",

	// ------------------------------------------------------------ login
	LoginSubtitle: "登录后可管理渠道与投递。",
	LoginUsername: "用户名",
	LoginPassword: "密码",
	LoginSubmit:   "登录",

	// ------------------------------------------------------------ setup
	SetupIntro: "本部署还没有管理员。现在创建一个——创建后本页永久关闭，无法再次访问。",
	SetupUsername: "用户名",
	SetupPassword: "密码",
	SetupConfirm:  "确认密码",
	SetupSubmit:   "创建管理员",
	SetupPasswordRule: "至少 8 位。这个账号可以改掉整个系统里所有通知的去向，所以只卡长度——" +
		"一句口令比一个带符号的短密码更结实。",

	// ------------------------------------------- error page back links
	BackToChannels:   "返回渠道",
	BackToDeliveries: "返回投递",
	BackToAudit:      "返回审计",
	BackToKeys:       "返回密钥",
	BackToAPI:        "返回 API 参考",
	BackToSetup:      "返回初始化",

	// --------------------------------------------------------- channels
	ChannelsNew:          "新建渠道",
	ChannelsTableName:    "名称",
	ChannelsTableType:    "类型",
	ChannelsTableState:   "状态",
	ChannelsTableSecrets: "凭据",
	ChannelsTagDisabled:  "已禁用",
	ChannelsTagLive:      "已加载",
	ChannelsTagNotLoaded: "未加载",
	ChannelsResetBreaker: "重置熔断器",
	ChannelsEmptyTitle:   "还没有渠道",
	ChannelsEmptyBody:    "没有渠道之前，所有投递都会失败。",
	ChannelsEmptyAction:  "新建渠道",
	ChannelFormEditHeading: "编辑 %s",
	ChannelFormNameLabel:   "名称",
	ChannelFormNameDesc:    "API 目标与 SMTP 收件人本地部分所用的别名。",
	ChannelFormTypeLabel:   "类型",
	ChannelFormTypeHint:    "选择类型…",
	ChannelFormTypeDesc:    "本二进制中已注册的渠道类型。",
	ChannelFormEnabled:     "启用",
	ChannelFormConfigHead:  "配置",
	ChannelFormSecretHint:  "已存有值——留空则不修改",
	ChannelFormSecretClear: "清除此值",
	ChannelFormQuotaHead:   "配额",
	ChannelFormQuotaDesc:   "0 表示不限。取不到配额的投递会退回队列，而不是直接失败。",
	ChannelFormTestConn:    "测试连通性",

	// ------------------------------------------------------- deliveries
	DeliveriesStats:           "排队 %d · 发送中 %d · 已送达 %d · 失败 %d",
	DeliveriesFilterStatus:    "状态",
	DeliveriesFilterChannel:   "渠道",
	DeliveriesFilterRequestID: "请求 ID",
	DeliveriesFilterLimit:     "每页",
	DeliveriesTableCreated:    "创建时间",
	DeliveriesTableChannel:    "渠道",
	DeliveriesTableStatus:     "状态",
	DeliveriesTableClass:      "分类",
	DeliveriesTableAttempts:   "尝试",
	DeliveriesTableLastError:  "最近错误",
	DeliveriesEmptyTitle:      "还没有投递",
	DeliveriesEmptyBody:       "先创建一个渠道和一个 API 密钥，然后发一条通知。",
	DeliveriesEmptyAction:     "新建渠道",
	DeliveriesNoMatchTitle:    "没有匹配的投递",
	DeliveriesNoMatchBody:     "当前筛选条件下没有结果。",
	DeliveriesNoMatchAction:   "清除筛选",
	StatusQueued:              "排队中",
	StatusSending:             "发送中",
	StatusSent:                "已送达",
	StatusFailed:              "失败",

	// -------------------------------------------------- delivery detail
	DeliveryFactID:          "ID",
	DeliveryFactRequest:     "请求",
	DeliveryFactChannel:     "渠道",
	DeliveryFactStatus:      "状态",
	DeliveryFactAttempts:    "尝试",
	DeliveryFactCreated:     "创建时间",
	DeliveryFactSent:        "送达时间",
	DeliveryFactNextAttempt: "下次尝试",
	DeliveryFactLastError:   "最近错误",
	DeliveryBodyExpired:     "消息体已不在磁盘上，无法重投。",
	DeliveryNotDeadLetter:   "只有死信投递可以重投。",
	DeliveryAttemptsHeading: "尝试记录",
	DeliveryAttemptWhen:     "时间",
	DeliveryAttemptClass:    "分类",
	DeliveryAttemptSkipped:  "跳过",
	DeliveryAttemptDetail:   "详情",
	DeliveryAttemptError:    "错误",
	DeliveryEmptyTitle:      "没有尝试记录",
	DeliveryEmptyBody: "通道根本没有被调用。三种可能：熔断器打开、配额用尽、被限流。" +
		"具体是哪一种看 skip_reason，对照 API 参考里的说明。",

	// ------------------------------------------------------------ audit
	AuditIntro: "通过本界面做出的、有实际变更的操作。保留期长于投递记录——" +
		"「谁把它关了」往往在「这条消息怎么了」很久之后才被问起。",
	AuditTableWhen:    "时间",
	AuditTableWho:     "操作人",
	AuditTableAction:  "操作",
	AuditTableChannel: "渠道",
	AuditTableDetail:  "详情",
	AuditDisabledHead: "本部署未启用审计",
	AuditDisabledBody: "启动时没有审计存储，通过本界面所做的任何操作都没有被记录。" +
		"这与「审计为空」不是一回事——这里的空页意味着什么都没记，而不是什么都没发生。",
	AuditEmptyHead: "暂无记录",
	AuditEmptyBody: "通过本界面做出的每一项变更都会记在这里——新建、编辑、删除渠道，" +
		"重置熔断器，以及创建、禁用、删除 API 密钥。",

	// ------------------------------------------------------------- keys
	KeysIntro: "上游用它做鉴权。密钥只在创建时显示一次——库里只有摘要，" +
		"所以此页面无法再次显示它。丢失就删除重建。",
	KeysNewHeading:       "新建密钥",
	KeysNameLabel:        "名称",
	KeysNameHint:         "ci-pipeline",
	KeysCreateButton:     "创建",
	KeysNameDesc: "名称是给人看的，不是给服务用的：密钥被用来做变更时它出现在审计记录里，" +
		"也是半年后你判断它能不能删时要读的东西。",
	KeysConfiguredNotice: "有 %d 个密钥来自配置文件。它们照常工作，也列在下方，" +
		"但不能在这里修改——改配置文件才会改它们。",
	KeysTableName:     "名称",
	KeysTableSource:   "来源",
	KeysTableStatus:   "状态",
	KeysTableCreated:  "创建时间",
	KeysTableLastUsed: "最近使用",
	KeysEmptyTitle:    "还没有密钥",
	KeysEmptyBody:     "在创建密钥之前，通知 API 的所有请求都返回 <code>401</code>。",
	KeysDialogHeading: "立即复制",
	KeysDialogBody:    "这是 token 唯一一次显示。它不以任何可读回的形式存储。",
	KeysDialogCopy:    "复制",
	KeysDialogDone:    "完成",

	// --------------------------------------------------------- api docs
	APIDocsIntro: "上游只需接入一次。下面是发一条通知、以及查它到底送出去了没有的全部接口。" +
		"设计理由见项目 README，逐字段的完整参考见 <code>docs/07-api.md</code>。",
	APIDocsBaseURL: "服务地址",
	APIDocsAuth:    "鉴权",
	APIDocsBaseURLNote: "这里的地址取自你当前访问后台用的地址。它不一定就是上游该用的地址——" +
		"经过反向代理、或者从别的网段访问时会不一样，以实际能连通的那个为准。",
	APIDocsTokenNote: "token 的明文不存在任何地方，<code>auth.api_keys</code> 里只有它的 sha256 摘要，" +
		"所以这里也没法替你填。用 <code>notifyrelay --hash-key '&lt;token&gt;'</code> 生成摘要，" +
		"明文自己保管。",
	APIDocsPlainHTTP: "你正在用 <strong>明文 HTTP</strong> 访问这个后台。下面示例里的 token 会以明文经过网络，" +
		"任何能看到这条链路的人都能拿到它，并以此发送通知。生产环境请让 <code>server.addr</code> " +
		"只监听内网，并在前面放一层 TLS。",
	APIDocsExamplesHeading: "调用示例",
	APIDocsExamplesIntro: "每个示例都是完整的：发一条通知，然后按 ID 查它的结果。全部默认校验证书——" +
		"<strong>token 走的是请求头，关掉校验等于把它交给链路上的任何人</strong>，" +
		"所以这里没有任何一个示例提供关闭校验的写法。",
	APIDocsCopy:          "复制",
	APIDocsEndpointsHead: "端点",
	APIDocsTableMethod:   "方法",
	APIDocsTablePath:     "路径",
	APIDocsTableAuth:     "鉴权",
	APIDocsTablePurpose:  "说明",
	APIDocsAuthNone:      "无",
	APIDocsErrorsHeading: "错误码",
	APIDocsErrorBodyNote: "错误体固定是 <code>{\"error\": \"...\", \"message\": \"...\"}</code>。" +
		"<strong>按 <code>error</code> 分支，不要按 <code>message</code></strong>——" +
		"后者是写给人看的，措辞会变。",
	APIDocsTableStatus:  "状态码",
	APIDocsTableMeaning: "含义",
	APIDocsGotchasHeading: "两个容易踩的地方",
	APIDocsGotchaClassCase: "<strong><code>class</code> 有两种大小写。</strong>" +
		"<code>/api/v1/notify</code> 的 <code>results[].status</code> 是小写" +
		"（<code>sent</code>、<code>transient</code>…），尝试历史里的 <code>class</code> " +
		"是大写（<code>SENT</code>、<code>TRANSIENT</code>…）。同一个枚举，两个拼法。",
	APIDocsGotchaNotAtt: "<strong>看到 <code>NOT_ATTEMPTED</code> 不等于通道坏了。</strong>" +
		"它表示通道<em>根本没被调用</em>——被熔断、被配额或被限流挡下了。" +
		"具体是哪种看 <code>skip_reason</code>。这与「试过了但连不上」" +
		"（<code>CONNECT_ERROR</code>）是两回事，处理方式也不同。",
	APIDocsSampleNoteCurl: "无依赖",
	APIDocsSampleNoteGo:   "标准库",

	// ------------------------------------------------- relative time
	TimeJustNow:    "刚刚",
	TimeMinutesAgo: "%d 分钟前",
	TimeHoursAgo:   "%d 小时前",
	TimeDaysAgo:    "%d 天前",

	// ---------------------------------------------------- error copy
	ErrChannelsUnreadable:   "渠道列表读取失败",
	ErrChannelUnreadable:    "渠道读取失败",
	ErrChannelNotFound:      "没有这个渠道",
	ErrDeliveriesUnreadable: "投递列表读取失败",
	ErrNoDeliveryStore:      "本部署没有投递存储",
	ErrDeliveryUnreadable:   "投递读取失败",
	ErrDeliveryNotFound:     "没有这个投递 ID",
	ErrAttemptsUnreadable:   "尝试记录读取失败",
	ErrAuditUnreadable:      "审计记录读取失败",
	ErrInvalidJSON:          "请求体不是合法 JSON",
	ErrChannelNameRequired:  "必须填渠道名",
	ErrChannelNameTaken:     "已存在名为 %s 的渠道；保存会覆盖它",
	ErrChannelDeleteFailed:  "渠道删除失败",
	ErrBreakerDisabled:      "本部署未启用熔断器",
	ErrChannelBuildFailed:   "无法按该配置构造渠道",
	ErrInvalidIntParam:      "%s 必须是非负整数",
	ErrNotReplayableStatus:  "只有死信投递可以重投；当前状态是 %s",
	ErrBodyExpired:          "消息体已不在磁盘上，没有可发送的内容；它已被保留策略清理",
	ErrReplayFailed:         "投递重投失败",
	ErrNotReplayable:        "该投递已不在可重投的状态",
	ErrStatsUnreadable:      "队列统计读取失败",
	ErrKeysUnreadable:       "密钥列表读取失败",
	ErrNoKeyStore:           "本部署没有 API 密钥存储",
	ErrKeyNameRequired:      "必须填名称",
	ErrKeyCreateFailed:      "密钥创建失败",
	ErrKeyEnabledRequired:   "缺少 enabled 字段",
	ErrKeyUnreadable:        "密钥读取失败",
	ErrKeyNotFound:          "没有这个密钥 ID",
	ErrKeyUpdateFailed:      "密钥更新失败",
	ErrKeyDeleteFailed:      "密钥删除失败",
	ErrTokenOnce:            "这是 token 唯一一次显示；它不可恢复",
	ErrAdminUnreadable:      "管理员账号读取失败",
	ErrTooManyAttempts:      "尝试次数过多，请稍后再试",
	ErrUsernameRequired:     "必须填用户名",
	ErrPasswordTooShort:     "密码至少 %d 位",
	ErrNoCredentialStore:    "本部署没有管理员账号存储",
	ErrAccountCreateFailed:  "账号创建失败",
	ErrAlreadyConfigured:    "本部署已有管理员",
	ErrSessionCreateFailed:  "会话创建失败",
	ErrTooManySignIns:       "登录失败次数过多，请稍后再试",
	ErrBadCredentials:       "用户名或密码不正确",
	ErrSignInFirst:          "请先登录",
	ErrSessionExpired:       "会话已过期",
	ErrMissingCSRF:          "变更类请求必须带 %s 头",

	// ------------------------------------------- JavaScript-only copy
	JSSavedButNotLive:     "已保存，但运行中的服务拒绝加载：%s",
	JSSavedFlash:          "已保存 %s",
	JSConfirmReplace:      "确认覆盖？",
	JSChooseTypeFirst:     "请先选择渠道类型。",
	JSSaveBeforeTest:      "请先保存渠道再测试。",
	JSTestOKPrefix:        "正常——%s",
	JSConfirmResetBreaker: "重置渠道 %s 的熔断器？\n\n" +
		"这会清掉一层因为该渠道持续失败而打开的保护。它不会清空队列，也不会归还任何配额。",
	JSBreakerResetFlash: "%s：熔断器原状态 %s，现已闭合",
	JSConfirmDeleteChan: "删除渠道 %s？\n\n" +
		"已经排队的投递会失败。",
	JSDeletedFlash:     "已删除 %s",
	JSConfirmDeleteKey: "删除密钥 %s？\n\n" +
		"仍在使用它的调用会立刻失败。无法恢复——token 本身从未存储，" +
		"只能重新创建一个并放到原来用的地方。",
	JSConfirmReplay: "重投投递 %s？\n\n" +
		"它会带着全新的尝试预算回到队列，并再次投递。",
	JSReplayedFlash:     "已重投 %s",
	JSKeyNameRequired:   "请先给密钥起个名字——审计记录里显示的就是它。",
	JSCopyTokenPrompt:   "立即复制——不会再次显示：",
	JSPasswordsNoMatch:  "两次输入的密码不一致。",
	JSSetupFailed:       "账号创建失败",
	JSCopyManualPrompt:  "用 Ctrl+C / Cmd+C 复制：",
}
```

```go
// en is the English copy table.
//
// Every value below is the string as it exists in the code today, copied
// verbatim. Do not improve the wording while moving it: a translation pass that
// also edits the source language is two changes nobody can review.
var en = Messages{
	// ------------------------------------------------------------ brand
	AppName: "NotifyRelay",

	// ------------------------------------------------------- navigation
	NavChannels:   "Channels",
	NavDeliveries: "Deliveries",
	NavAudit:      "Audit",
	NavKeys:       "Keys",
	NavAPI:        "API",
	NavSignOut:    "Sign out",

	// ----------------------------------------------------- page titles
	TitleSignIn:     "Sign in",
	TitleSetup:      "Create administrator",
	TitleChannels:   "Channels",
	TitleDeliveries: "Deliveries",
	TitleDelivery:   "Delivery",
	TitleAudit:      "Audit",
	TitleKeys:       "Keys",
	TitleAPI:        "API",

	// ---------------------------------------------------- shared copy
	CommonSave:        "Save",
	CommonCancel:      "Cancel",
	CommonDelete:      "Delete",
	CommonTest:        "Test",
	CommonCopy:        "Copy",
	CommonDone:        "Done",
	CommonCreate:      "Create",
	CommonEnable:      "Enable",
	CommonDisable:     "Disable",
	CommonFilter:      "Filter",
	CommonClear:       "Clear",
	CommonDetail:      "Detail",
	CommonReplay:      "Replay",
	CommonBack:        "Back",
	CommonNewer:       "Newer",
	CommonOlder:       "Older",
	CommonAny:         "any",
	CommonNever:       "never",
	CommonEnabled:     "enabled",
	CommonDisabled:    "disabled",
	CommonRequired:    "required",
	CommonSetInConfig: "set in the configuration file",

	// ------------------------------------------------------------ login
	LoginSubtitle: "Sign in to manage channels and deliveries.",
	LoginUsername: "Username",
	LoginPassword: "Password",
	LoginSubmit:   "Sign in",

	// ------------------------------------------------------------ setup
	SetupIntro: "This deployment has no administrator yet. Create one now — this page " +
		"closes permanently once you do, and it is not reachable again.",
	SetupUsername: "Username",
	SetupPassword: "Password",
	SetupConfirm:  "Confirm password",
	SetupSubmit:   "Create administrator",
	SetupPasswordRule: "At least 8 characters. This account can reconfigure where every " +
		"notification in the estate is delivered, so length is the only rule — " +
		"a passphrase beats a short password with symbols in it.",

	// ------------------------------------------- error page back links
	BackToChannels:   "Back to channels",
	BackToDeliveries: "Back to deliveries",
	BackToAudit:      "Back to the audit trail",
	BackToKeys:       "Back to API keys",
	BackToAPI:        "Back to the API reference",
	BackToSetup:      "Back to setup",

	// --------------------------------------------------------- channels
	ChannelsNew:          "New channel",
	ChannelsTableName:    "Name",
	ChannelsTableType:    "Type",
	ChannelsTableState:   "State",
	ChannelsTableSecrets: "Secrets",
	ChannelsTagDisabled:  "disabled",
	ChannelsTagLive:      "live",
	ChannelsTagNotLoaded: "not loaded",
	ChannelsResetBreaker: "Reset breaker",
	ChannelsEmptyTitle:   "No channels configured",
	ChannelsEmptyBody:    "Deliveries will fail until there is one.",
	ChannelsEmptyAction:  "New channel",
	ChannelFormEditHeading: "Edit %s",
	ChannelFormNameLabel:   "Name",
	ChannelFormNameDesc:    "The alias used in API targets and SMTP recipient local parts.",
	ChannelFormTypeLabel:   "Type",
	ChannelFormTypeHint:    "Choose a type…",
	ChannelFormTypeDesc:    "A channel type registered in this binary.",
	ChannelFormEnabled:     "Enabled",
	ChannelFormConfigHead:  "Configuration",
	ChannelFormSecretHint:  "a value is stored — leave blank to keep it",
	ChannelFormSecretClear: "clear this value",
	ChannelFormQuotaHead:   "Allowance",
	ChannelFormQuotaDesc: "Zero means unlimited. A delivery that cannot get a slot " +
		"goes back to the queue rather than failing.",
	ChannelFormTestConn: "Test connectivity",

	// ------------------------------------------------------- deliveries
	DeliveriesStats:           "queued %d · in flight %d · sent %d · failed %d",
	DeliveriesFilterStatus:    "Status",
	DeliveriesFilterChannel:   "Channel",
	DeliveriesFilterRequestID: "Request ID",
	DeliveriesFilterLimit:     "Per page",
	DeliveriesTableCreated:    "Created",
	DeliveriesTableChannel:    "Channel",
	DeliveriesTableStatus:     "Status",
	DeliveriesTableClass:      "Class",
	DeliveriesTableAttempts:   "Attempts",
	DeliveriesTableLastError:  "Last error",
	DeliveriesEmptyTitle:      "Nothing sent yet",
	DeliveriesEmptyBody:       "Create a channel and an API key, then send one.",
	DeliveriesEmptyAction:     "New channel",
	DeliveriesNoMatchTitle:    "Nothing matches",
	DeliveriesNoMatchBody:     "No delivery matches the current filters.",
	DeliveriesNoMatchAction:   "Clear filters",
	StatusQueued:              "queued",
	StatusSending:             "sending",
	StatusSent:                "sent",
	StatusFailed:              "failed",

	// -------------------------------------------------- delivery detail
	DeliveryFactID:          "ID",
	DeliveryFactRequest:     "Request",
	DeliveryFactChannel:     "Channel",
	DeliveryFactStatus:      "Status",
	DeliveryFactAttempts:    "Attempts",
	DeliveryFactCreated:     "Created",
	DeliveryFactSent:        "Sent",
	DeliveryFactNextAttempt: "Next attempt",
	DeliveryFactLastError:   "Last error",
	DeliveryBodyExpired:     "The message body is no longer on disk, so this one cannot be replayed.",
	DeliveryNotDeadLetter:   "Only a dead-lettered delivery can be replayed.",
	DeliveryAttemptsHeading: "Attempts",
	DeliveryAttemptWhen:     "When",
	DeliveryAttemptClass:    "Class",
	DeliveryAttemptSkipped:  "Skipped",
	DeliveryAttemptDetail:   "Detail",
	DeliveryAttemptError:    "Error",
	DeliveryEmptyTitle:      "No attempts recorded",
	DeliveryEmptyBody: "The channel was never called. Three reasons: an open breaker, " +
		"a spent allowance, or a rate limit. skip_reason says which — see the API reference.",

	// ------------------------------------------------------------ audit
	AuditIntro: "Everything done through this interface that changed something. Kept beyond " +
		"the delivery retention window, because \"who turned this off\" is asked long " +
		"after \"what happened to this message\".",
	AuditTableWhen:    "When",
	AuditTableWho:     "Who",
	AuditTableAction:  "Action",
	AuditTableChannel: "Channel",
	AuditTableDetail:  "Detail",
	AuditDisabledHead: "This deployment keeps no record of operator actions",
	AuditDisabledBody: "It was started without an audit store, so nothing done through this " +
		"interface is being written down. This is not the same as an empty trail, and " +
		"the difference matters — an empty page here means nothing was recorded, not " +
		"that nothing happened.",
	AuditEmptyHead: "Nothing yet",
	AuditEmptyBody: "Every change made through this interface is recorded here — creating, " +
		"editing and deleting a channel, resetting a breaker, and creating, disabling " +
		"or deleting an API key.",

	// ------------------------------------------------------------- keys
	KeysIntro: "What producers authenticate with. A key is shown once, when it is created — " +
		"only its digest is stored, so nothing here can show it to you again. If one " +
		"is lost, delete it and make another.",
	KeysNewHeading:   "New key",
	KeysNameLabel:    "Name",
	KeysNameHint:     "ci-pipeline",
	KeysCreateButton: "Create",
	KeysNameDesc: "The name is for you, not for the service: it is what appears in the audit " +
		"trail when the key is used to change something, and what you will read in " +
		"six months when deciding whether it can be deleted.",
	KeysConfiguredNotice: "%d key(s) come from the configuration file. They work, and they " +
		"are listed below, but they cannot be changed here — editing the file is what " +
		"changes those.",
	KeysTableName:     "Name",
	KeysTableSource:   "Source",
	KeysTableStatus:   "Status",
	KeysTableCreated:  "Created",
	KeysTableLastUsed: "Last used",
	KeysEmptyTitle:    "No keys yet",
	KeysEmptyBody: "Until one exists, every request to the notification API is answered " +
		"<code>401</code>.",
	KeysDialogHeading: "Copy this now",
	KeysDialogBody: "This is the only time the token is shown. It is not stored anywhere in a " +
		"form that can be read back.",
	KeysDialogCopy: "Copy",
	KeysDialogDone: "Done",

	// --------------------------------------------------------- api docs
	APIDocsIntro: "Producers integrate once. Below is the whole surface for sending a " +
		"notification and for finding out whether it actually arrived. For the " +
		"reasoning behind the design see the project README; for the exhaustive " +
		"field-by-field reference see <code>docs/07-api.md</code>.",
	APIDocsBaseURL: "Base URL",
	APIDocsAuth:    "Authentication",
	APIDocsBaseURLNote: "This address is the one you reached this page on. It is not necessarily " +
		"the one a producer should use — behind a proxy, or from another network, " +
		"it will differ. Use whichever one actually connects.",
	APIDocsTokenNote: "The plaintext token is stored nowhere — <code>auth.api_keys</code> holds only " +
		"its sha256 digest, so this page cannot fill it in for you. Generate a digest " +
		"with <code>notifyrelay --hash-key '&lt;token&gt;'</code> and keep the plaintext yourself.",
	APIDocsPlainHTTP: "You are reaching this page over <strong>plain HTTP</strong>. The token in the " +
		"samples below would cross the network in the clear, and anyone who can see " +
		"that traffic can send notifications with it. In production, bind " +
		"<code>server.addr</code> to an internal address and terminate TLS in front " +
		"of it.",
	APIDocsExamplesHeading: "Worked examples",
	APIDocsExamplesIntro: "Each one is complete: send a notification, then read the result back by id. " +
		"All of them verify the certificate — <strong>the token travels in a request " +
		"header, so turning verification off hands it to anyone on the path</strong> — " +
		"which is why not one of them shows you how.",
	APIDocsCopy:          "Copy",
	APIDocsEndpointsHead: "Endpoints",
	APIDocsTableMethod:   "Method",
	APIDocsTablePath:     "Path",
	APIDocsTableAuth:     "Auth",
	APIDocsTablePurpose:  "Purpose",
	APIDocsAuthNone:      "none",
	APIDocsErrorsHeading: "Error codes",
	APIDocsErrorBodyNote: "Every error body is <code>{\"error\": \"...\", \"message\": \"...\"}</code>. " +
		"<strong>Branch on <code>error</code>, never on <code>message</code></strong> — " +
		"the latter is written for a human and its wording will change.",
	APIDocsTableStatus:  "Status",
	APIDocsTableMeaning: "Meaning",
	APIDocsGotchasHeading: "Two things that catch people out",
	APIDocsGotchaClassCase: "<strong><code>class</code> has two spellings.</strong> " +
		"<code>results[].status</code> from <code>/api/v1/notify</code> is lowercase " +
		"(<code>sent</code>, <code>transient</code>…); the <code>class</code> in an " +
		"attempt history is uppercase (<code>SENT</code>, <code>TRANSIENT</code>…). " +
		"The same enum, decided by which endpoint you are on.",
	APIDocsGotchaNotAtt: "<strong><code>NOT_ATTEMPTED</code> does not mean the channel is broken.</strong> " +
		"It means the channel was <em>never called</em> — held back by an open breaker, " +
		"a spent allowance or a rate limit. <code>skip_reason</code> says which. That is " +
		"a different problem from <code>CONNECT_ERROR</code>, where the call was made " +
		"and did not get there, and it is fixed differently.",
	APIDocsSampleNoteCurl: "no dependency",
	APIDocsSampleNoteGo:   "standard library",

	// ------------------------------------------------- relative time
	TimeJustNow:    "just now",
	TimeMinutesAgo: "%dm ago",
	TimeHoursAgo:   "%dh ago",
	TimeDaysAgo:    "%dd ago",

	// ---------------------------------------------------- error copy
	ErrChannelsUnreadable:   "the channels could not be read",
	ErrChannelUnreadable:    "the channel could not be read",
	ErrChannelNotFound:      "no channel with that name",
	ErrDeliveriesUnreadable: "the deliveries could not be read",
	ErrNoDeliveryStore:      "this deployment has no delivery store",
	ErrDeliveryUnreadable:   "the delivery could not be read",
	ErrDeliveryNotFound:     "no delivery with that id",
	ErrAttemptsUnreadable:   "the attempt history could not be read",
	ErrAuditUnreadable:      "the audit trail could not be read",
	ErrInvalidJSON:          "the request body is not valid JSON",
	ErrChannelNameRequired:  "a channel name is required",
	ErrChannelNameTaken:     "a channel named %s already exists; saving would replace it",
	ErrChannelDeleteFailed:  "the channel could not be deleted",
	ErrBreakerDisabled:      "the circuit breaker is not enabled in this deployment",
	ErrChannelBuildFailed:   "the channel could not be built from its configuration",
	ErrInvalidIntParam:      "%s must be a non-negative integer",
	ErrNotReplayableStatus:  "only a dead-lettered delivery can be replayed; this one is %s",
	ErrBodyExpired: "the message body is no longer on disk, so there is nothing to send; " +
		"it was removed by the retention policy",
	ErrReplayFailed:    "the delivery could not be replayed",
	ErrNotReplayable:   "the delivery is no longer in a state that can be replayed",
	ErrStatsUnreadable: "the queue statistics could not be read",
	ErrKeysUnreadable:  "the API keys could not be read",
	ErrNoKeyStore:      "this deployment has no store for API keys",
	ErrKeyNameRequired: "a name is required",
	ErrKeyCreateFailed: "the key could not be created",
	ErrKeyEnabledRequired: "enabled is required",
	ErrKeyUnreadable:      "the key could not be read",
	ErrKeyNotFound:        "no key with that id",
	ErrKeyUpdateFailed:    "the key could not be updated",
	ErrKeyDeleteFailed:    "the key could not be deleted",
	ErrTokenOnce:          "this is the only time the token is shown; it is not recoverable",
	ErrAdminUnreadable:    "the administrator account could not be read",
	ErrTooManyAttempts:    "too many attempts; try again later",
	ErrUsernameRequired:   "a username is required",
	ErrPasswordTooShort:   "the password must be at least %d characters",
	ErrNoCredentialStore:  "this deployment has no store for an administrator account",
	ErrAccountCreateFailed: "the account could not be created",
	ErrAlreadyConfigured:   "an administrator already exists for this deployment",
	ErrSessionCreateFailed: "the session could not be created",
	ErrTooManySignIns:      "too many failed sign-in attempts; try again later",
	ErrBadCredentials:      "the username or password is not correct",
	ErrSignInFirst:         "sign in first",
	ErrSessionExpired:      "the session has expired",
	ErrMissingCSRF:         "state-changing requests must carry the %s header",

	// ------------------------------------------- JavaScript-only copy
	JSSavedButNotLive: "Saved, but the running service refused it: %s",
	JSSavedFlash:      "Saved %s",
	JSConfirmReplace:  "Replace it?",
	JSChooseTypeFirst: "Choose a channel type first.",
	JSSaveBeforeTest:  "Save the channel before testing it.",
	JSTestOKPrefix:    "OK — %s",
	JSConfirmResetBreaker: "Reset the breaker for %s?\n\n" +
		"This clears a protection that was switched on because the channel " +
		"kept failing. It does not drain the queue or return any allowance.",
	JSBreakerResetFlash: "%s: breaker was %s, now closed",
	JSConfirmDeleteChan: "Delete channel %s?\n\n" +
		"Deliveries already queued for it will fail.",
	JSDeletedFlash: "Deleted %s",
	JSConfirmDeleteKey: "Delete the key %s?\n\n" +
		"Anything still using it stops working immediately. " +
		"There is no way to restore it — the token itself was " +
		"never stored, so a replacement has to be created and " +
		"put wherever this one was.",
	JSConfirmReplay: "Replay delivery %s?\n\n" +
		"It goes back to the queue with a fresh attempt budget and will be " +
		"delivered again.",
	JSReplayedFlash:    "Replayed %s",
	JSKeyNameRequired:  "Give the key a name first — it is what the audit trail will show.",
	JSCopyTokenPrompt:  "Copy this now — it is not shown again:",
	JSPasswordsNoMatch: "The two passwords do not match.",
	JSSetupFailed:      "the account could not be created",
	JSCopyManualPrompt: "Copy with Ctrl+C / Cmd+C:",
}
```

---

## 4. 刻意不翻译

### 4.1 审计详情（写进 `admin_audit.detail`，是记录不是界面文案）

| 字符串 | 位置 | 理由 |
|---|---|---|
| `created` | `channels.go:201`、`keys.go:141` | 审计记录值；翻译会让同一张表混两种语言，且历史记录无法统一 |
| `deleted` | `channels.go:243`、`keys.go:233` | 同上 |
| `enabled` / `disabled` | `keys.go:193-196` | 同上 |
| `saved with no changes` | `channels.go:434` | 同上 |
| `changed: ` + 参数名列表 | `channels.go:436` | 同上；参数名本身也不翻译 |
| `was %s; the channel will be tried again on the next delivery` | `channels.go:326` | 同上 |
| `replayed delivery %s (request %s)` | `deliveries.go:234` | 同上 |
| 动作名 `channel.create` / `channel.update` / `channel.delete` / `breaker.reset` / `key.create` / `key.update` / `key.delete` / `delivery.replay` | 各处 `h.record(...)` | 机器可读的动作标识，`apidocs` 里也没有对应文案 |

### 4.2 技术标识符

| 类别 | 值 |
|---|---|
| 渠道类型名 | `webhook`、`email`、`dingtalk`、`feishu`、`slack`、`wecom`（`channels.html` 的类型下拉用 `typeForm.Label`，是 `strings.ToUpper(d.Type[:1]) + d.Type[1:]` 生成的 `Webhook`/`Email`/`Dingtalk`/…，同样不翻译） |
| 参数名 | `host`、`port`、`tls`、`require_tls`、`username`、`password`、`auth_type`、`from`、`to`、`helo`、`timeout`、`ca_file`、`subject_template`、`webhook_url`、`per_second`、`per_minute`、`per_hour`、`per_day`、`per_month` 等（`channels.html:126-131` 的配额输入框标签就是这五个名字本身） |
| HTTP 头名 | `Authorization`、`Content-Type`、`Idempotency-Key`、`X-NotifyRelay-Admin`、`Retry-After` |
| `skip_reason` 值 | `breaker_open`、`quota_exhausted`、`rate_limited`（`delivery.html:49` 直接渲染） |
| 结果分类 | `SENT`、`PERMANENT`、`TRANSIENT`、`CONNECT_ERROR`、`NOT_ATTEMPTED`（`delivery.html:48` 直接渲染，`lower` 只用于 CSS class） |
| 投递状态作为**查询参数和 CSS class** | `queued`、`sending`、`sent`、`failed`——`<option value="...">`、`?status=` 和 `.tag.queued` 必须保持英文；只有可见标签走 `StatusQueued` 等四个字段。**这意味着 `deliveries.html:21-23` 必须改成 value 用英文枚举、label 用消息表** |
| `<code>` 里的标识符 | `/api/v1/notify`、`/api/v1/messages/{id}`、`/healthz`、`/readyz`、`/metrics`、`auth.api_keys`、`notifyrelay --hash-key`、`server.addr`、`docs/07-api.md`、`results[].status`、`class`、`error`、`message`、`sync`、`401`、`202`、`CONNECT_ERROR`、`NOT_ATTEMPTED`、`skip_reason`、`Idempotent-Replay`、`CURLOPT_SSL_VERIFYPEER`、`HttpClientHandler`、`ssl._create_unverified_context()` 等 |
| 表头符号与单位 | `delivery.html:41` 的 `#` 和 `ms` |
| 语言切换标签 | `apidocs.html:9-11` 的 `中文` / `EN` |
| 示例页签标签 | `apidocs.go:51-57` 的 `curl`、`Go`、`Python`、`Java`、`C#`、`C`、`C++` |
| 示例依赖说明（技术型） | `11+, java.net.http`、`.NET 5+, System.Net.Http`、`libcurl`、`cpp-httplib, header-only`（`apidocs.go:54-57`） |
| 复制完成的反馈字符 | `app.js:565` 的 `"✓"` |
| `ci-pipeline` | `keys.html:17` 的 placeholder——是示例值不是提示语；若要本地化，应换成 `ci-流水线` 之类，但它是标识符风格，建议保留 |
| 时间格式 | `ui.go:43` 的 `"2006-01-02 15:04:05"` 是数字格式，不需要翻译；但**中文界面下可能需要改成 `2006-01-02 15:04:05`（不变）或本地化格式，这是一个待定项** |

---

## 5. 示例代码里的注释（`samples/*.txt`）

7 个文件共 **63 个注释块**（把连续多行注释里的一段话算一块，含 `---- send one` / `---- the result`
这类分隔标题）。去掉在多个文件里重复出现的块之后，**约 37 条互不相同的字符串**。

| 文件 | 注释块 |
|---|---:|
| `curl.txt` | 7 |
| `go.txt` | 9 |
| `python.txt` | 9 |
| `java.txt` | 8 |
| `csharp.txt` | 8 |
| `c.txt` | 10 |
| `cpp.txt` | 12 |
| **合计** | **63** |

**需要翻译。** 计划 §2.4 明确把「`samples/*.txt` 里的英文注释没有中文版」列为 `apidocs.html`
的两个问题之一。这些注释是解释性的散文（"A repeat with the same key replays the first
response and the channel still sees one message."），不是代码，中文界面上留着英文注释与
整页翻译不一致。

**但不要逐条塞进 `Messages`。** 理由：

1. 每个语言一份完整示例文件（`samples/zh/go.txt`）比把注释抽成 37 个字段更可读——注释和它
   解释的那行代码必须一起看，抽出去就失去了上下文。
2. 重复率高：`A repeat with the same key…` 出现在全部 7 个文件，`202 means queued…` 出现在 6 个，
   `A 4xx carries {"error": …}` 出现在 4 个，`---- send one` / `---- the result` 两个分隔标题
   出现在 6-7 个。做成字段会产生大量近似重复的键。
3. 代码本身（URL、header、JSON 字段、`panic(err)`）一行都不翻译，只有注释翻。

**建议做法**：`samples/` 下按语言分目录，`loadSamples()` 按当前语言读
`samples/<lang>/<id>.txt`，缺失时回退到 `en`。`apidocs_test.go` 里那条「任何示例都不得包含
关闭证书校验的写法」的断言要对**两个语言目录都跑**——这是这条断言最容易被翻译工作绕过去的地方。

---

## 6. 清点中发现的问题（不在本次交付范围，但会影响 i18n 落地）

1. **`internal/channel/*/config.go` 里有 110 条用户可见文案**（60 个 `Label` + 50 个 `Desc`，
   分布在 8 个文件）。它们经 `fieldView.Label` / `.Description` 渲染进渠道表单
   （`channels.html:88-91, 117`），是**渠道表单里占比最大的文案来源**，但不在 `internal/admin`
   下，本次没有进 `Messages`。落地时必须单独决策：是在 `internal/channel` 里加一套平行字段，
   还是把 schema 的展示文案也收进 `i18n` 包（后者会让 `internal/channel` 依赖 `i18n`，
   方向可能不对）。**这是整个 i18n 工作里最大的一块未决范围。**
2. **渠道校验错误也是用户可见的。** `channels.go:191` 把 `validateChannel` 的
   `err.Error()` 直接当 `writeError` 的消息发给前端，`app.js` 弹出来。这些错误来自
   `internal/channel` 的 `parseConfig`，例如
   `parameter "to" must list at least one recipient`（`email/config.go:91`）、
   `"require_tls" cannot be true when "tls" is "none"`（`email/config.go:171`）。
   它们**用 schema 名而不是界面标签**——这正是计划 §3.4 要修的问题，和翻译是同一处代码。
3. **`channels.go:290` 的 `channel.Permanent(err, "the channel could not be built from its configuration")`**
   已经收进 `ErrChannelBuildFailed`，但各渠道 `Test()` 实现返回的 `Detail` 文案同样会经
   `app.js:313` 显示给用户，那些字符串在 `internal/channel/*` 下，同样未清点。
4. **`<html lang="en">` 写死在 `layout.html:3`**，需要按语言动态化（计划 §1 已列）。
5. **`since` / `stamp` 在包级 `FuncMap` 注册**（`ui.go:34-36`），拿不到每次请求的语言。
   `TimeJustNow` 等 4 条要生效，`FuncMap` 必须按语言重建（计划 §1 已列）。
6. **`apidocs.html` 现有的中文译文建议原样复用**，不要重新翻译——它是作者自己写的，
   措辞已经定过。上面 §3 的 `zh` 表里，`APIDocs*` 系列的取值就是照抄现有文件的。
7. **`{{.Configured}} key(s)` 的英文用了 `key(s)` 这种规避单复数的写法**（`keys.html:30`）。
   中文没有单复数问题，`%d 个密钥` 直接解决；英文侧保留原样。
