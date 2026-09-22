package i18n

// The two tables.
//
// Field for field, in the same order, because the compiler only checks that
// both have every field — not that they are laid out the same way. Reading them
// side by side is how a wrong translation gets caught, and that only works if
// the two are aligned.

var zh = Messages{
	Script: Script{
		ConfirmResetBreaker: "重置 %s 的熔断器？\n\n" +
			"这会清除因为该渠道持续失败而开启的保护。它不会清空队列，也不会返还配额。",
		ConfirmDeleteChan: "删除渠道 %s？\n\n" +
			"已经排队的投递会失败。",
		ConfirmDeleteKey: "删除密钥 %s？\n\n" +
			"还在使用它的调用会立即失效。无法恢复——令牌本身从未被存储，" +
			"只能新建一个再替换到原来的地方。",
		ConfirmReplay: "重放投递 %s？\n\n" +
			"它会带着新的重试预算回到队列，并再次投递。",

		SavedButNotLive: "已保存，但运行中的服务拒绝加载：%s",
		SaveBeforeTest:  "请先保存渠道，再测试连通。",
		ChooseTypeFirst: "请先选择渠道类型。",
		GiveKeyAName:    "先给密钥起个名字——审计记录里显示的就是它。",
		PasswordsDiffer: "两次输入的密码不一致。",

		PromptCopyToken: "现在复制——它不会再显示：",
		PromptCopyText:  "用 Ctrl+C / Cmd+C 复制：",

		TestOK:     "连通正常——%s",
		TestFailed: "%s——%s",
	},

	Brand:      "信使中枢",
	SignOut:    "退出登录",
	LangSwitch: "语言",

	NavChannels:   "渠道",
	NavDeliveries: "投递",
	NavAudit:      "审计",
	NavKeys:       "密钥",
	NavAPI:        "API",

	TitleChannels:   "渠道",
	TitleDeliveries: "投递",
	TitleAudit:      "操作审计",
	TitleKeys:       "API 密钥",
	TitleAPI:        "API 文档",

	TimeJustNow:    "刚刚",
	TimeMinutesAgo: "%d 分钟前",
	TimeHoursAgo:   "%d 小时前",
	TimeDaysAgo:    "%d 天前",

	CommonSave:   "保存",
	CommonCancel: "取消",
	CommonDelete: "删除",
	CommonCreate: "创建",
	CommonCopy:   "复制",
	CommonDone:   "完成",
	CommonNever:  "从未",
	CommonTest:   "测试",

	TitleLogin:    "登录",
	LoginIntro:    "登录后可以管理渠道与投递。",
	LoginUsername: "用户名",
	LoginPassword: "密码",
	LoginSubmit:   "登录",

	TitleSetup:  "创建管理员",
	SetupIntro: "本部署还没有管理员。现在创建一个——创建之后这个页面会永久关闭，" +
		"无法再次访问。",
	SetupUsername: "用户名",
	SetupPassword: "密码",
	SetupConfirm:  "确认密码",
	SetupSubmit:   "创建管理员",
	SetupPasswordWhy: "至少 8 位。这个账号可以改动所有通知的去向，所以唯一的规则是长度——" +
		"长口令比带符号的短密码更可靠。",
	SetupMismatch: "两次输入的密码不一致。",

	TitleError: "出错了",

	BackChannels:   "返回渠道",
	BackDeliveries: "返回投递",
	BackAudit:      "返回审计",
	BackKeys:       "返回密钥",
	BackAPI:        "返回 API 文档",
	BackSetup:      "返回设置",

	ErrAdminUnreadable: "读不到管理员账号",
}

var en = Messages{
	Script: Script{
		ConfirmResetBreaker: "Reset the breaker for %s?\n\n" +
			"This clears a protection that was switched on because the channel " +
			"kept failing. It does not drain the queue or return any allowance.",
		ConfirmDeleteChan: "Delete channel %s?\n\n" +
			"Deliveries already queued for it will fail.",
		ConfirmDeleteKey: "Delete the key %s?\n\n" +
			"Anything still using it stops working immediately. " +
			"There is no way to restore it — the token itself was " +
			"never stored, so a replacement has to be created and " +
			"put wherever this one was.",
		ConfirmReplay: "Replay delivery %s?\n\n" +
			"It goes back to the queue with a fresh attempt budget and will be " +
			"delivered again.",

		SavedButNotLive: "Saved, but the running service refused it: %s",
		SaveBeforeTest:  "Save the channel before testing it.",
		ChooseTypeFirst: "Choose a channel type first.",
		GiveKeyAName:    "Give the key a name first — it is what the audit trail will show.",
		PasswordsDiffer: "The two passwords do not match.",

		PromptCopyToken: "Copy this now — it is not shown again:",
		PromptCopyText:  "Copy with Ctrl+C / Cmd+C:",

		TestOK:     "OK — %s",
		TestFailed: "%s — %s",
	},

	Brand:      "NotifyRelay",
	SignOut:    "Sign out",
	LangSwitch: "Language",

	NavChannels:   "Channels",
	NavDeliveries: "Deliveries",
	NavAudit:      "Audit",
	NavKeys:       "Keys",
	NavAPI:        "API",

	TitleChannels:   "Channels",
	TitleDeliveries: "Deliveries",
	TitleAudit:      "Operator actions",
	TitleKeys:       "API keys",
	TitleAPI:        "API reference",

	TimeJustNow:    "just now",
	TimeMinutesAgo: "%dm ago",
	TimeHoursAgo:   "%dh ago",
	TimeDaysAgo:    "%dd ago",

	CommonSave:   "Save",
	CommonCancel: "Cancel",
	CommonDelete: "Delete",
	CommonCreate: "Create",
	CommonCopy:   "Copy",
	CommonDone:   "Done",
	CommonNever:  "never",
	CommonTest:   "Test",

	TitleLogin:    "Sign in",
	LoginIntro:    "Sign in to manage channels and deliveries.",
	LoginUsername: "Username",
	LoginPassword: "Password",
	LoginSubmit:   "Sign in",

	TitleSetup: "Create administrator",
	SetupIntro: "This deployment has no administrator yet. Create one now — this page " +
		"closes permanently once you do, and it is not reachable again.",
	SetupUsername:    "Username",
	SetupPassword:    "Password",
	SetupConfirm:     "Confirm password",
	SetupSubmit:      "Create administrator",
	SetupPasswordWhy: "At least 8 characters. This account can reconfigure where every notification in the estate is delivered, so length is the only rule — a passphrase beats a short password with symbols in it.",
	SetupMismatch:    "The two passwords do not match.",

	TitleError: "Something went wrong",

	BackChannels:   "Back to channels",
	BackDeliveries: "Back to deliveries",
	BackAudit:      "Back to the audit trail",
	BackKeys:       "Back to API keys",
	BackAPI:        "Back to the API reference",
	BackSetup:      "Back to setup",

	ErrAdminUnreadable: "the administrator account could not be read",
}
