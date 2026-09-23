package i18n

// The two tables.
//
// Field for field, in the same order, because the compiler only checks that both
// have every field — not that they are laid out the same way. Reading them side
// by side is how a wrong translation gets caught, and that only works while the
// two are aligned.
//
// The English column is the string as it was in the code, copied verbatim. It
// was not improved on the way here: a translation pass that also rewrites the
// original leaves nobody able to tell a change of language from a change of
// wording, and the diff is where that gets reviewed.

// zh is the Simplified Chinese copy table. Default language.
var zh = Messages{
	Script: Script{
		SavedButNotLive: "已保存，但运行中的服务拒绝加载：%s",
		SavedFlash:      "已保存 %s",
		ConfirmReplace:  "确认覆盖？",
		ChooseTypeFirst: "请先选择渠道类型。",
		SaveBeforeTest:  "请先保存渠道再测试。",
		TestOK:          "正常——%s",
		ConfirmResetBreaker: "重置渠道 %s 的熔断器？\n\n" +
			"这会清掉一层因为该渠道持续失败而打开的保护。它不会清空队列，也不会归还任何配额。",
		BreakerResetFlash: "%s：熔断器原状态 %s，现已闭合",
		ConfirmDeleteChan: "删除渠道 %s？\n\n" +
			"已经排队的投递会失败。",
		DeletedFlash: "已删除 %s",
		ConfirmDeleteKey: "删除密钥 %s？\n\n" +
			"仍在使用它的调用会立刻失败。无法恢复——token 本身从未存储，" +
			"只能重新创建一个并放到原来用的地方。",
		ConfirmReplay: "重投投递 %s？\n\n" +
			"它会带着全新的尝试预算回到队列，并再次投递。",
		ReplayedFlash:      "已重投 %s",
		KeyNameRequired:    "请先给密钥起个名字——审计记录里显示的就是它。",
		CopyTokenPrompt:    "立即复制——不会再次显示：",
		PasswordsNoMatch:   "两次输入的密码不一致。",
		SetupFailed:        "账号创建失败",
		CopyManualPrompt:   "用 Ctrl+C / Cmd+C 复制：",
		TestFailed:         "%s——%s",
		TestNotifyQueued:   "测试通知已入队",
		KeyStateEnabled:    "%s 已启用",
		KeyStateDisabled:   "%s 已禁用",
		KeyRecipientsSaved: "%s 的可发往收件人已更新",
		DiscardChanges:     "这个表单有未保存的改动，离开会丢掉它们。",
		FixMarkedFields:    "请修正标出的字段。",
		PasswordSet:        "密码已修改。",
	},
	// ------------------------------------------------------------ brand
	AppName: "信使中枢",

	// ------------------------------------------------------- navigation
	NavChannels:   "渠道",
	NavDeliveries: "投递",
	NavAudit:      "审计",
	NavKeys:       "密钥",
	NavAPI:        "API",
	NavSignOut:    "退出登录",
	NavStart:      "上手",
	NavLangSwitch: "语言",

	// ----------------------------------------------------- page titles
	TitleSignIn:     "登录",
	TitleSetup:      "创建管理员",
	TitleChannels:   "渠道",
	TitleDeliveries: "投递",
	TitleDelivery:   "投递详情",
	TitleAudit:      "审计",
	TitleKeys:       "密钥",
	TitleAPI:        "API 参考",
	TitleError:      "出错了",
	TitleStart:      "上手",

	// ------------------------------------------------ the first-run checklist
	StartIntro: "从零到一条真的送达的通知，四步。每一步都是查出来的，不是记下来的——" +
		"所以删掉渠道之后，它不会还说这一步已完成。",
	StartStepChannel:     "创建一个渠道",
	StartStepChannelHint: "渠道就是通知的去向：邮件、webhook、钉钉、飞书、Slack、企微。",
	StartStepKey:         "创建一个 API Key",
	StartStepKeyHint:     "上游用它调用通知 API。明文只显示一次，库里只有摘要。",
	StartStepTest:        "发一条测试通知",
	StartStepTestHint:    "按钮在渠道那一行。它走的是完整投递链路，不是只检查配置格式。",
	StartStepResult:      "查看投递结果",
	StartStepResultHint: "投递详情页有完整的尝试记录。失败也是结果——" +
		"它告诉你是哪一步不对。",
	StartActionGo: "前往",
	StartStepDone: "已完成",
	StartDoneHead: "四步都完成了",
	StartDoneBody: "通知链路已经跑通。这个入口会从左侧消失，下次直接进渠道页。",

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
	SetupIntro:    "本部署还没有管理员。现在创建一个——创建后本页永久关闭，无法再次访问。",
	SetupUsername: "用户名",
	SetupPassword: "密码",
	SetupConfirm:  "确认密码",
	SetupSubmit:   "创建管理员",
	SetupPasswordRule: "至少 8 位。这个账号可以改掉整个系统里所有通知的去向，所以只卡长度——" +
		"一句口令比一个带符号的短密码更结实。",

	// ------------------------------------------------------- change password
	NavPassword:     "修改密码",
	TitlePassword:   "修改密码",
	PasswordIntro:   "改掉这个后台账号的密码。",
	PasswordCurrent: "当前密码",
	PasswordNew:     "新密码",
	PasswordConfirm: "确认新密码",
	PasswordSubmit:  "修改密码",
	PasswordRule:    "至少 8 位。",
	PasswordChanged: "密码已修改，另外 %d 个会话已被登出。",

	// ------------------------------------------- error page back links
	BackToChannels:   "返回渠道",
	BackToDeliveries: "返回投递",
	BackToAudit:      "返回审计",
	BackToKeys:       "返回密钥",
	BackToAPI:        "返回 API 参考",
	BackToSetup:      "返回初始化",

	// --------------------------------------------------------- channels
	ChannelsNew:            "新建渠道",
	ChannelsTableName:      "名称",
	ChannelsTableType:      "类型",
	ChannelsTableState:     "状态",
	ChannelsTableSecrets:   "凭据",
	ChannelsTagDisabled:    "已禁用",
	ChannelsTagLive:        "已加载",
	ChannelsTagNotLoaded:   "未加载",
	ChannelsResetBreaker:   "重置熔断器",
	ChannelsEmptyTitle:     "还没有渠道",
	ChannelsEmptyBody:      "没有渠道之前，所有投递都会失败。",
	ChannelsEmptyAction:    "新建渠道",
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
	ChannelFormListHint:    "多个值用逗号分隔。",
	ChannelGoneHead:        "这个渠道不存在",
	ChannelGoneBody: "名为 %s 的渠道已经被删除，或者从来没有存在过。" +
		"保存这个表单会新建它——如果你要的是新建，请用「新建渠道」。",
	TestNotifyButton:         "发送测试通知",
	TestNotifyNeedsRecipient: "该渠道没有固定收件人，测试通知无处可发。先给它填上收件人，或改用通知 API 并带上 to 字段。",
	TestNotifyHeading:        "发送测试通知",
	TestNotifyIntro: "发一条真实的通知，走完整的投递链路：入队 → worker → 渠道 → 尝试记录。" +
		"这是唯一能证明「通知真的会到」的操作——「测试连通性」只能证明配置能被解析，" +
		"而且对其中五种渠道类型它连网络都不会碰。",
	TestNotifyTitle:     "标题",
	TestNotifyTitleHint: "测试通知",
	TestNotifyBody:      "正文",
	TestNotifyBodyHint:  "来自信使中枢的测试通知，收到即说明这条渠道通了。",
	TestNotifySubmit:    "发送",
	TestNotifyCancel:    "取消",

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
	KeysNewHeading:   "新建密钥",
	KeysNameLabel:    "名称",
	KeysNameHint:     "ci-pipeline",
	KeysCreateButton: "创建",
	KeysNameDesc: "名称是给人看的，不是给服务用的：密钥被用来做变更时它出现在审计记录里，" +
		"也是半年后你判断它能不能删时要读的东西。",
	KeysConfiguredNotice: "有 %d 个密钥来自配置文件。它们照常工作，也列在下方，" +
		"但不能在这里修改——改配置文件才会改它们。",
	KeysTableName:       "名称",
	KeysTableSource:     "来源",
	KeysTableStatus:     "状态",
	KeysTableCreated:    "创建时间",
	KeysTableLastUsed:   "最近使用",
	KeysTableRecipients: "可发往",
	KeysRecipientsLabel: "可发往的收件人",
	KeysRecipientsHint:  "@example.com",
	KeysRecipientsDesc: "留空表示一个都不许：这个密钥只能发到渠道自己配置好的目的地。" +
		"要让它指定收件人（注册邮件、密码重置这类），在这里写允许的地址模式，" +
		"用逗号分隔。三种写法：*（任意地址）、@example.com（该域名，不含子域）、" +
		"user@example.com（单个地址）。",
	KeysRecipientsNone:    "仅渠道目的地",
	KeysRecipientsEdit:    "改收件人",
	KeysRecipientsHeading: "可发往的收件人",
	KeysEmptyTitle:        "还没有密钥",
	KeysEmptyBody:         "在创建密钥之前，通知 API 的所有请求都返回 <code>401</code>。",
	KeysDialogHeading:     "立即复制",
	KeysDialogBody:        "这是 token 唯一一次显示。它不以任何可读回的形式存储。",
	KeysDialogCopy:        "复制",
	KeysDialogDone:        "完成",

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
	APIDocsCopy:              "复制",
	APIDocsRecipientsHeading: "收件人由请求指定",
	APIDocsRecipientsIntro: "上面的示例都发往渠道自己配置好的目的地——告警要的就是这个。" +
		"注册邮件、密码重置这类事务邮件的收件人只存在于请求里，在通知体里加一个 <code>to</code> 字段：",
	APIDocsRecipientsSame: "下面这种写法完全等价：地址写在目标 URL 里，" +
		"<code>?via=</code> 指定借用哪个邮件实例的服务器与凭据。",
	APIDocsRecipientsReplace: "请求给的收件人<strong>替换</strong>渠道配置里的，不是追加。" +
		"否则一个固定收件人（合规抄送、公共邮箱）会收到每封发给别人的密码重置邮件。",
	APIDocsRecipientsAllowlist: "收件人要过 API 密钥的 <code>allowed_recipients</code> 白名单，" +
		"两种写法都要过。空白名单表示一个都不许——只发告警的密钥不用配，要发事务邮件就得在密钥页显式授权。",
	APIDocsRecipientsExclusive: "<code>to</code> 与 <code>mailto://</code> 目标不能同时出现，" +
		"那是同一个问题的两个答案，合并会发到你没写过的地址。",
	APIDocsRecipientsExampleTo: `{
  "targets": ["email:tx"],
  "to":      ["user@example.com"],
  "title":   "重置密码",
  "body":    "https://example.com/reset/abc"
}`,
	APIDocsRecipientsExampleURL: `{
  "targets": ["mailto://user@example.com?via=tx"],
  "title":   "重置密码",
  "body":    "https://example.com/reset/abc"
}`,
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
	APIDocsTableStatus:    "状态码",
	APIDocsTableMeaning:   "含义",
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
	ErrChannelsUnreadable:    "渠道列表读取失败",
	ErrChannelUnreadable:     "渠道读取失败",
	ErrChannelNotFound:       "没有这个渠道",
	ErrDeliveriesUnreadable:  "投递列表读取失败",
	ErrNoDeliveryStore:       "本部署没有投递存储",
	ErrDeliveryUnreadable:    "投递读取失败",
	ErrDeliveryNotFound:      "没有这个投递 ID",
	ErrAttemptsUnreadable:    "尝试记录读取失败",
	ErrAuditUnreadable:       "审计记录读取失败",
	ErrInvalidJSON:           "请求体不是合法 JSON",
	ErrChannelNameRequired:   "必须填渠道名",
	ErrChannelNameTaken:      "已存在名为 %s 的渠道；保存会覆盖它",
	ErrChannelDeleteFailed:   "渠道删除失败",
	ErrBreakerDisabled:       "本部署未启用熔断器",
	ErrChannelBuildFailed:    "无法按该配置构造渠道",
	ErrChannelDisabled:       "该渠道已禁用；先启用再发送",
	ErrChannelNeedsRecipient: "该渠道没有配置固定收件人，测试通知无处可发；先给它填上收件人，或改用通知 API 并带上 to 字段",
	ErrKeyRecipientPattern:   "%s 不是合法的收件人模式；只支持 *、@域名、完整地址三种写法",
	ErrNoQueue:               "本部署没有投递队列",
	ErrEnqueueFailed:         "通知入队失败",
	ErrTestMessageIncomplete: "标题和正文都必须填",
	ErrInvalidIntParam:       "%s 必须是非负整数",
	ErrNotReplayableStatus:   "只有死信投递可以重投；当前状态是 %s",
	ErrBodyExpired:           "消息体已不在磁盘上，没有可发送的内容；它已被保留策略清理",
	ErrReplayFailed:          "投递重投失败",
	ErrNotReplayable:         "该投递已不在可重投的状态",
	ErrStatsUnreadable:       "队列统计读取失败",
	ErrKeysUnreadable:        "密钥列表读取失败",
	ErrNoKeyStore:            "本部署没有 API 密钥存储",
	ErrKeyNameRequired:       "必须填名称",
	ErrKeyCreateFailed:       "密钥创建失败",
	ErrKeyUnreadable:         "密钥读取失败",
	ErrKeyNotFound:           "没有这个密钥 ID",
	ErrKeyUpdateFailed:       "密钥更新失败",
	ErrKeyDeleteFailed:       "密钥删除失败",
	ErrTokenOnce:             "这是 token 唯一一次显示；它不可恢复",
	ErrAdminUnreadable:       "管理员账号读取失败",
	ErrTooManyAttempts:       "尝试次数过多，请稍后再试",
	ErrUsernameRequired:      "必须填用户名",
	ErrPasswordTooShort:      "密码至少 %d 位",
	ErrNoCredentialStore:     "本部署没有管理员账号存储",
	ErrAccountCreateFailed:   "账号创建失败",
	ErrAlreadyConfigured:     "本部署已有管理员",
	ErrSessionCreateFailed:   "会话创建失败",
	ErrTooManySignIns:        "登录失败次数过多，请稍后再试",
	ErrBadCredentials:        "用户名或密码不正确",
	ErrSignInFirst:           "请先登录",
	ErrSessionExpired:        "会话已过期",
	ErrPasswordInConfig:      "这个账号的密码写在配置文件里，不在这里改。改掉配置里的 admin.password_hash 并重启。",
	ErrPasswordChangeFailed:  "密码修改失败",
	ErrMissingCSRF:           "变更类请求必须带 %s 头",
}

// en is the English copy table.
//
// Every value below is the string as it exists in the code today, copied
// verbatim. Do not improve the wording while moving it: a translation pass that
// also edits the source language is two changes nobody can review.
var en = Messages{
	Script: Script{
		SavedButNotLive: "Saved, but the running service refused it: %s",
		SavedFlash:      "Saved %s",
		ConfirmReplace:  "Replace it?",
		ChooseTypeFirst: "Choose a channel type first.",
		SaveBeforeTest:  "Save the channel before testing it.",
		TestOK:          "OK — %s",
		ConfirmResetBreaker: "Reset the breaker for %s?\n\n" +
			"This clears a protection that was switched on because the channel " +
			"kept failing. It does not drain the queue or return any allowance.",
		BreakerResetFlash: "%s: breaker was %s, now closed",
		ConfirmDeleteChan: "Delete channel %s?\n\n" +
			"Deliveries already queued for it will fail.",
		DeletedFlash: "Deleted %s",
		ConfirmDeleteKey: "Delete the key %s?\n\n" +
			"Anything still using it stops working immediately. " +
			"There is no way to restore it — the token itself was " +
			"never stored, so a replacement has to be created and " +
			"put wherever this one was.",
		ConfirmReplay: "Replay delivery %s?\n\n" +
			"It goes back to the queue with a fresh attempt budget and will be " +
			"delivered again.",
		ReplayedFlash:      "Replayed %s",
		KeyNameRequired:    "Give the key a name first — it is what the audit trail will show.",
		CopyTokenPrompt:    "Copy this now — it is not shown again:",
		PasswordsNoMatch:   "The two passwords do not match.",
		SetupFailed:        "the account could not be created",
		CopyManualPrompt:   "Copy with Ctrl+C / Cmd+C:",
		TestFailed:         "%s — %s",
		TestNotifyQueued:   "The test notification was queued",
		KeyStateEnabled:    "%s is enabled",
		KeyStateDisabled:   "%s is disabled",
		KeyRecipientsSaved: "%s: allowed recipients updated",
		DiscardChanges:     "This form has unsaved changes. Leaving discards them.",
		FixMarkedFields:    "Correct the marked fields.",
		PasswordSet:        "The password was changed.",
	},
	// ------------------------------------------------------------ brand
	AppName: "NotifyRelay",

	// ------------------------------------------------------- navigation
	NavChannels:   "Channels",
	NavDeliveries: "Deliveries",
	NavAudit:      "Audit",
	NavKeys:       "Keys",
	NavAPI:        "API",
	NavSignOut:    "Sign out",
	NavStart:      "Get started",
	NavLangSwitch: "Language",

	// ----------------------------------------------------- page titles
	TitleSignIn:     "Sign in",
	TitleSetup:      "Create administrator",
	TitleChannels:   "Channels",
	TitleDeliveries: "Deliveries",
	TitleDelivery:   "Delivery",
	TitleAudit:      "Audit",
	TitleKeys:       "Keys",
	TitleAPI:        "API reference",
	TitleError:      "Something went wrong",
	TitleStart:      "Get started",

	// ------------------------------------------------ the first-run checklist
	StartIntro: "Four steps from nothing to a notification that has actually arrived. Each " +
		"one is looked up rather than remembered, so the page cannot still claim a step is " +
		"done after you delete the thing that finished it.",
	StartStepChannel:     "Create a channel",
	StartStepChannelHint: "A channel is where notifications go: mail, a webhook, DingTalk, Feishu, Slack, WeCom.",
	StartStepKey:         "Create an API key",
	StartStepKeyHint:     "Producers authenticate with it. The plaintext is shown once; only its digest is stored.",
	StartStepTest:        "Send a test notification",
	StartStepTestHint:    "The button is on the channel's row. It goes through the whole delivery path — it does not just check that the configuration parses.",
	StartStepResult:      "Look at the result",
	StartStepResultHint:  "The delivery page has the full attempt history. A failure is a result too — it says which part is wrong.",
	StartActionGo:        "Go",
	StartStepDone:        "Done",
	StartDoneHead:        "All four are done",
	StartDoneBody:        "The notification path works end to end. This entry disappears from the sidebar.",

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

	// ------------------------------------------------------- change password
	NavPassword:     "Change password",
	TitlePassword:   "Change password",
	PasswordIntro:   "Change the password for this operator account.",
	PasswordCurrent: "Current password",
	PasswordNew:     "New password",
	PasswordConfirm: "Confirm new password",
	PasswordSubmit:  "Change password",
	PasswordRule:    "At least 8 characters.",
	PasswordChanged: "The password was changed, and %d other sessions were signed out.",

	// ------------------------------------------- error page back links
	BackToChannels:   "Back to channels",
	BackToDeliveries: "Back to deliveries",
	BackToAudit:      "Back to the audit trail",
	BackToKeys:       "Back to API keys",
	BackToAPI:        "Back to the API reference",
	BackToSetup:      "Back to setup",

	// --------------------------------------------------------- channels
	ChannelsNew:            "New channel",
	ChannelsTableName:      "Name",
	ChannelsTableType:      "Type",
	ChannelsTableState:     "State",
	ChannelsTableSecrets:   "Secrets",
	ChannelsTagDisabled:    "disabled",
	ChannelsTagLive:        "live",
	ChannelsTagNotLoaded:   "not loaded",
	ChannelsResetBreaker:   "Reset breaker",
	ChannelsEmptyTitle:     "No channels configured",
	ChannelsEmptyBody:      "Deliveries will fail until there is one.",
	ChannelsEmptyAction:    "New channel",
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
	ChannelFormListHint: "Separate multiple values with commas.",
	ChannelGoneHead:     "That channel is not there",
	ChannelGoneBody: "A channel named %s has been deleted, or never existed. " +
		"Saving this form would create it — if that is what you want, use New channel.",
	TestNotifyButton:         "Send a test notification",
	TestNotifyNeedsRecipient: "This channel has no recipient of its own, so a test notification has nowhere to go. Give it one, or send through the notify API with a \"to\" field.",
	TestNotifyHeading:        "Send a test notification",
	TestNotifyIntro: "Sends a real notification through the whole delivery path: queue, worker, " +
		"channel, attempt history. This is the only thing that shows a notification actually " +
		"arrives — \"test connectivity\" only shows the configuration parses, and for five of " +
		"the six channel types it does not touch the network at all.",
	TestNotifyTitle:     "Title",
	TestNotifyTitleHint: "Test notification",
	TestNotifyBody:      "Body",
	TestNotifyBodyHint:  "A test notification from NotifyRelay. If you can read this, the channel works.",
	TestNotifySubmit:    "Send",
	TestNotifyCancel:    "Cancel",

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
	KeysTableName:       "Name",
	KeysTableSource:     "Source",
	KeysTableStatus:     "Status",
	KeysTableCreated:    "Created",
	KeysTableLastUsed:   "Last used",
	KeysTableRecipients: "May send to",
	KeysRecipientsLabel: "Recipients this key may name",
	KeysRecipientsHint:  "@example.com",
	KeysRecipientsDesc: "Empty means none: the key can only reach the destinations a " +
		"channel was configured with. To let it name its own recipients — registration mail, " +
		"password resets — list the address patterns it may use, comma separated. Three forms: " +
		"* (any address), @example.com (that domain, not subdomains), user@example.com (one address).",
	KeysRecipientsNone:    "channel destinations only",
	KeysRecipientsEdit:    "Recipients",
	KeysRecipientsHeading: "Recipients this key may name",
	KeysEmptyTitle:        "No keys yet",
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
	APIDocsCopy:              "Copy",
	APIDocsRecipientsHeading: "Recipients named by the request",
	APIDocsRecipientsIntro: "Every sample above sends to the destination a channel " +
		"was configured with — which is what an alert needs. Registration mail and password resets " +
		"have a recipient that exists only in the request, so they add a <code>to</code> field:",
	APIDocsRecipientsSame: "This spelling means exactly the same thing: the address is " +
		"in the target URL, and <code>?via=</code> names the mail instance whose server and credentials " +
		"are borrowed.",
	APIDocsRecipientsReplace: "Recipients from the request <strong>replace</strong> the " +
		"ones in the channel configuration rather than adding to them. Appending would mean a fixed " +
		"recipient — a compliance copy, a shared mailbox — receives every password-reset link addressed " +
		"to somebody else.",
	APIDocsRecipientsAllowlist: "Recipients are checked against the API key's " +
		"<code>allowed_recipients</code>, both spellings alike. An empty list means none: a key that only " +
		"fires alerts needs nothing, and one that sends transactional mail is granted the addresses on " +
		"the keys page.",
	APIDocsRecipientsExclusive: "<code>to</code> and a <code>mailto://</code> target " +
		"cannot appear together — they are two answers to one question, and merging them would send to " +
		"an address you never wrote.",
	APIDocsRecipientsExampleTo: `{
  "targets": ["email:tx"],
  "to":      ["user@example.com"],
  "title":   "Reset your password",
  "body":    "https://example.com/reset/abc"
}`,
	APIDocsRecipientsExampleURL: `{
  "targets": ["mailto://user@example.com?via=tx"],
  "title":   "Reset your password",
  "body":    "https://example.com/reset/abc"
}`,
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
	APIDocsTableStatus:    "Status",
	APIDocsTableMeaning:   "Meaning",
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
	ErrChannelsUnreadable:    "the channels could not be read",
	ErrChannelUnreadable:     "the channel could not be read",
	ErrChannelNotFound:       "no channel with that name",
	ErrDeliveriesUnreadable:  "the deliveries could not be read",
	ErrNoDeliveryStore:       "this deployment has no delivery store",
	ErrDeliveryUnreadable:    "the delivery could not be read",
	ErrDeliveryNotFound:      "no delivery with that id",
	ErrAttemptsUnreadable:    "the attempt history could not be read",
	ErrAuditUnreadable:       "the audit trail could not be read",
	ErrInvalidJSON:           "the request body is not valid JSON",
	ErrChannelNameRequired:   "a channel name is required",
	ErrChannelNameTaken:      "a channel named %s already exists; saving would replace it",
	ErrChannelDeleteFailed:   "the channel could not be deleted",
	ErrBreakerDisabled:       "the circuit breaker is not enabled in this deployment",
	ErrChannelBuildFailed:    "the channel could not be built from its configuration",
	ErrChannelDisabled:       "the channel is disabled; enable it before sending",
	ErrChannelNeedsRecipient: "this channel has no recipient of its own, so a test notification has nowhere to go; give it one, or send through the notify API with a \"to\" field",
	ErrKeyRecipientPattern:   "%s is not a valid recipient pattern; use \"*\", \"@domain\" or a full address",
	ErrNoQueue:               "this deployment has no delivery queue",
	ErrEnqueueFailed:         "the notification could not be queued",
	ErrTestMessageIncomplete: "both a title and a body are required",
	ErrInvalidIntParam:       "%s must be a non-negative integer",
	ErrNotReplayableStatus:   "only a dead-lettered delivery can be replayed; this one is %s",
	ErrBodyExpired: "the message body is no longer on disk, so there is nothing to send; " +
		"it was removed by the retention policy",
	ErrReplayFailed:         "the delivery could not be replayed",
	ErrNotReplayable:        "the delivery is no longer in a state that can be replayed",
	ErrStatsUnreadable:      "the queue statistics could not be read",
	ErrKeysUnreadable:       "the API keys could not be read",
	ErrNoKeyStore:           "this deployment has no store for API keys",
	ErrKeyNameRequired:      "a name is required",
	ErrKeyCreateFailed:      "the key could not be created",
	ErrKeyUnreadable:        "the key could not be read",
	ErrKeyNotFound:          "no key with that id",
	ErrKeyUpdateFailed:      "the key could not be updated",
	ErrKeyDeleteFailed:      "the key could not be deleted",
	ErrTokenOnce:            "this is the only time the token is shown; it is not recoverable",
	ErrAdminUnreadable:      "the administrator account could not be read",
	ErrTooManyAttempts:      "too many attempts; try again later",
	ErrUsernameRequired:     "a username is required",
	ErrPasswordTooShort:     "the password must be at least %d characters",
	ErrNoCredentialStore:    "this deployment has no store for an administrator account",
	ErrAccountCreateFailed:  "the account could not be created",
	ErrAlreadyConfigured:    "an administrator already exists for this deployment",
	ErrSessionCreateFailed:  "the session could not be created",
	ErrTooManySignIns:       "too many failed sign-in attempts; try again later",
	ErrBadCredentials:       "the username or password is not correct",
	ErrSignInFirst:          "sign in first",
	ErrSessionExpired:       "the session has expired",
	ErrPasswordInConfig:     "this account's password comes from the configuration file. Change admin.password_hash there and restart.",
	ErrPasswordChangeFailed: "the password could not be changed",
	ErrMissingCSRF:          "state-changing requests must carry the %s header",
}
