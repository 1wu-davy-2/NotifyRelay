package i18n

// zhParams is the Chinese copy for every channel parameter this build declares.
//
// The key is "<channel type>.<parameter name>". Keyed by type as well as by
// name because the same name does not mean the same thing twice: `timeout` is a
// request timeout for a webhook and a delivery timeout for SMTP, and `token` is
// a Slack bot token in one channel and a bearer token in another.
//
// Only the Chinese is here. The English is the schema's own Label and Desc,
// which are the declaration rather than a copy of it — putting them here too
// would be a second place to update, and the second place is the one that goes
// stale.
var zhParams = map[string]ParamCopy{
	// --- dingtalk ---
	"dingtalk.webhook_url": {Label: "机器人 webhook 地址", Desc: "https://oapi.dingtalk.com/robot/send?access_token=... token 就在 URL 里。"},
	"dingtalk.secret":      {Label: "签名密钥", Desc: "可选。设置后请求会加签。写成 `!env DINGTALK_SECRET`。"},
	"dingtalk.msg_type":    {Label: "消息类型", Desc: ""},
	"dingtalk.at_mobiles":  {Label: "@ 手机号", Desc: "要 @ 的手机号。"},
	"dingtalk.at_all":      {Label: "@所有人", Desc: "提醒整个群。慎用。"},
	"dingtalk.timeout":     {Label: "请求超时", Desc: ""},
	"dingtalk.rate_per_sec": {Label: "限流",
		Desc: "每秒消息数。钉钉限制每分钟 20 条，超出后机器人会被静音十分钟。"},
	"dingtalk.ca_file": {Label: "CA 证书包",
		Desc: "额外受信根的 PEM 文件，用于私有 CA 或做 TLS 检查的代理。"},

	// --- email ---
	"email.host":        {Label: "SMTP 服务器", Desc: "上游中继主机名。"},
	"email.port":        {Label: "端口", Desc: "默认 587。"},
	"email.tls":         {Label: "加密方式", Desc: "留空则按端口推断：465 为隐式 TLS，其余为 STARTTLS。"},
	"email.require_tls": {Label: "强制 TLS", Desc: "除非 tls 为 none，否则默认为开。"},
	"email.username":    {Label: "用户名", Desc: ""},
	"email.password":    {Label: "密码", Desc: "写成 `!env SMTP_PASSWORD`；不要直接写在配置里。"},
	"email.auth_type":   {Label: "认证方式", Desc: "auto 表示由服务器通告其支持的机制。"},
	"email.from":        {Label: "发件人", Desc: "信封与头部的发件人。"},
	"email.to":          {Label: "收件人", Desc: "一个或多个地址。每个收件人独立投递。"},
	"email.helo":        {Label: "EHLO 名称", Desc: "留空则由客户端自行推导。"},
	"email.timeout":     {Label: "投递超时", Desc: "限制单个收件人的 SMTP 会话时长。"},
	"email.ca_file":     {Label: "CA 证书包", Desc: "额外受信根的 PEM 文件，用于使用私有 CA 的中继。"},
	"email.subject_template": {Label: "主题模板",
		Desc: "占位符模板，例如 `[{type}] {title}`。默认为标题加严重级别前缀。"},

	// --- feishu ---
	"feishu.webhook_url":  {Label: "机器人 webhook 地址", Desc: "https://open.feishu.cn/open-apis/bot/v2/hook/... 密钥就在 URL 里。"},
	"feishu.secret":       {Label: "签名密钥", Desc: "可选。设置后请求会加签。写成 `!env FEISHU_SECRET`。"},
	"feishu.msg_type":     {Label: "消息类型", Desc: "interactive 发送卡片，卡片头部颜色表示严重级别。"},
	"feishu.timeout":      {Label: "请求超时", Desc: ""},
	"feishu.rate_per_sec": {Label: "限流", Desc: "每秒消息数。"},
	"feishu.ca_file": {Label: "CA 证书包",
		Desc: "额外受信根的 PEM 文件，用于私有 CA 或做 TLS 检查的代理。"},

	// --- slack ---
	"slack.webhook_url": {Label: "Incoming webhook 地址", Desc: "密钥就在 URL 里。与 token 互斥。"},
	"slack.token":       {Label: "机器人 token", Desc: "改用 chat.postMessage 发送。需要 `channel`。写成 `!env SLACK_TOKEN`。"},
	"slack.channel":     {Label: "频道", Desc: "频道 ID 或名称，配合机器人 token 使用。"},
	"slack.timeout":     {Label: "请求超时", Desc: ""},
	"slack.rate_per_sec": {Label: "限流",
		Desc: "每秒消息数。Slack 大约只允许 1 条；0 表示不限。"},
	"slack.ca_file": {Label: "CA 证书包",
		Desc: "额外受信根的 PEM 文件，用于私有 CA 或做 TLS 检查的代理。"},

	// --- webhook ---
	"webhook.url":              {Label: "端点地址", Desc: "JSON 载荷的投递地址。通常带有 token；写成 `!env ...`。"},
	"webhook.method":           {Label: "HTTP 方法", Desc: ""},
	"webhook.content_type":     {Label: "内容类型", Desc: ""},
	"webhook.payload_template": {Label: "载荷模板", Desc: "可选的 JSON 正文，可含 {placeholders}。值会被 JSON 转义，因此占位符必须放在引号内。"},
	"webhook.timeout":          {Label: "请求超时", Desc: ""},
	"webhook.body_max_len":     {Label: "正文上限", Desc: "按字符计。0 表示不限。"},
	"webhook.title_max_len":    {Label: "标题上限", Desc: "按字符计。0 表示不限。"},
	"webhook.overflow_mode":    {Label: "超限处理", Desc: "正文超过 body_max_len 时的处理方式。"},
	"webhook.rate_per_sec":     {Label: "限流", Desc: "每秒消息数。0 表示不限。"},
	"webhook.auth_type":        {Label: "认证方式", Desc: "向端点认证的方式。"},
	"webhook.token":            {Label: "Bearer token", Desc: "auth_type 为 bearer 时使用。写成 `!env ...`。"},
	"webhook.username":         {Label: "用户名", Desc: "auth_type 为 basic 时使用。"},
	"webhook.password":         {Label: "密码", Desc: "auth_type 为 basic 时使用。写成 `!env ...`。"},
	"webhook.header_name":      {Label: "请求头名称", Desc: "auth_type 为 header 时使用。"},
	"webhook.header_value":     {Label: "请求头值", Desc: "auth_type 为 header 时使用。写成 `!env ...`。"},
	"webhook.secret":           {Label: "签名密钥", Desc: "auth_type 为 hmac 时使用。写成 `!env ...`。"},
	"webhook.signature_header": {Label: "签名请求头", Desc: "auth_type 为 hmac 时使用。"},
	"webhook.signature_prefix": {Label: "签名前缀", Desc: "加在摘要前面，例如 `sha256=`。"},
	"webhook.signature_base64": {Label: "Base64 签名", Desc: "摘要以 base64 输出，而不是 hex。"},
	"webhook.ca_file": {Label: "CA 证书包",
		Desc: "额外受信根的 PEM 文件，用于私有 CA 或做 TLS 检查的代理。"},

	// --- wecom ---
	"wecom.mode":        {Label: "模式", Desc: "webhook 发到群机器人；app 走应用发送，需要下面的凭据。"},
	"wecom.webhook_url": {Label: "群机器人 webhook", Desc: "webhook 模式下必填。https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=..."},
	"wecom.corp_id":     {Label: "企业 ID", Desc: "app 模式下必填。"},
	"wecom.corp_secret": {Label: "企业密钥", Desc: "app 模式下必填。写成 `!env WECOM_SECRET`。"},
	"wecom.agent_id":    {Label: "Agent ID", Desc: "app 模式下必填。"},
	"wecom.to_user":     {Label: "接收用户", Desc: "app 模式。`@all`，或 `|` 隔的用户列表。本项与接收部门至少填一个；默认留空正说明两者都不单独必填。"},
	"wecom.to_party":    {Label: "接收部门", Desc: "app 模式。用 `|` 隔的部门列表。本项与接收用户至少填一个。"},
	"wecom.msg_type":    {Label: "消息类型", Desc: ""},
	"wecom.timeout":     {Label: "请求超时", Desc: ""},
	"wecom.rate_per_sec": {Label: "限流",
		Desc: "每秒消息数。群机器人限制每分钟 20 条。"},
	"wecom.ca_file": {Label: "CA 证书包",
		Desc: "额外受信根的 PEM 文件，用于私有 CA 或做 TLS 检查的代理。"},
}
