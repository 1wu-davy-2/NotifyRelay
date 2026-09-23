// Package wecom delivers notifications to WeCom (企业微信).
//
// Two modes, because they are used for different things: a group robot posts
// into a chat with no application behind it, while an application can address
// individual users and departments.
package wecom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/channel/httpx"
	"notifyrelay/internal/channel/token"
	"notifyrelay/internal/message"
	"notifyrelay/internal/render"
)

const (
	defaultTimeout = 10 * time.Second
	// A group robot accepts 20 messages per minute.
	defaultRatePerSec = 20.0 / 60.0

	// WeCom caps a markdown message at 4096 BYTES, not characters — about
	// 1300 Chinese characters. The rune limit is set so the byte cap is the
	// binding constraint for Chinese and the rune cap for ASCII.
	defaultBodyMaxBytes = 4096
	defaultBodyMaxLen   = 1300

	// The colour tag alone is 29 characters, and it is only present when the
	// severity has a colour — which is most of the time.
	payloadOverheadRunes = 64
	payloadOverheadBytes = 64
)

// API endpoints for application mode. Fields on the Channel rather than
// constants so tests can point them at a local server.
const (
	getTokenURL     = "https://qyapi.weixin.qq.com/cgi-bin/gettoken"
	sendMessageURL  = "https://qyapi.weixin.qq.com/cgi-bin/message/send"
)

func init() {
	channel.Register(channel.Descriptor{
		Type:        "wecom",
		ParamSchema: paramSchema(),
		Capability:  capability(defaultRatePerSec),
		Factory:     New,
	})
}

func capability(rate float64) channel.Capability {
	return channel.Capability{
		BodyMaxLen:       defaultBodyMaxLen,
		BodyMaxBytes:     defaultBodyMaxBytes,
		SupportedFormats: []message.Format{message.FormatText, message.FormatMarkdown},
		// WeCom markdown has no headings.
		MarkdownDialect: render.DialectWeCom,
		RatePerSec:      rate,
		OverflowMode:    channel.OverflowSplit,

		PayloadOverheadRunes: payloadOverheadRunes,
		PayloadOverheadBytes: payloadOverheadBytes,
	}
}

// Channel posts messages to WeCom.
type Channel struct {
	instance string
	cfg      Config
	client   *httpx.Client
	tokens   *token.Cache

	getTokenURL string
	sendURL     string
}

// New builds a WeCom channel instance from a configuration block.
func New(instance string, raw map[string]any) (channel.Channel, error) {
	cfg, err := parseConfig(raw)
	if err != nil {
		return nil, err
	}

	client, err := httpx.NewClientFromConfig(cfg.Timeout, cfg.CAFile)
	if err != nil {
		return nil, err
	}

	c := &Channel{
		instance:    instance,
		cfg:         cfg,
		client:      client,
		getTokenURL: getTokenURL,
		sendURL:     sendMessageURL,
	}
	if cfg.Mode == ModeApp {
		c.tokens = token.New(c.fetchToken)
	}
	return c, nil
}

// Type implements channel.Channel.
func (c *Channel) Type() string { return "wecom" }

// Capability implements channel.Channel.
func (c *Channel) Capability() channel.Capability { return capability(c.cfg.RatePerSec) }

// ParamSchema implements channel.Channel.
func (c *Channel) ParamSchema() []channel.ParamSpec { return paramSchema() }

// Send implements channel.Channel.
func (c *Channel) Send(ctx context.Context, msg *message.Message, _ channel.Target) channel.Result {
	if c.cfg.Mode == ModeWebhook {
		return c.sendWebhook(ctx, msg)
	}
	return c.sendApp(ctx, msg)
}

// Test verifies the application credentials, or reports that a group robot has
// nothing safe to probe.
func (c *Channel) Test(ctx context.Context) channel.Result {
	if c.cfg.Mode == ModeWebhook {
		return channel.Sent("configuration is valid (a group robot has no side-effect-free probe)")
	}

	c.tokens.Invalidate()
	if _, err := c.tokens.Get(ctx); err != nil {
		return channel.Permanent(err, "the application credentials were rejected")
	}
	return channel.Sent("application credentials accepted")
}

// ------------------------------------------------------------------ webhook

type webhookPayload struct {
	MsgType  string `json:"msgtype"`
	Markdown *struct {
		Content string `json:"content"`
	} `json:"markdown,omitempty"`
	Text *struct {
		Content string `json:"content"`
	} `json:"text,omitempty"`
}

func (c *Channel) sendWebhook(ctx context.Context, msg *message.Message) channel.Result {
	body, err := json.Marshal(payloadFor(c.cfg.MsgType, msg))
	if err != nil {
		return channel.Permanent(err, "the payload could not be rendered")
	}

	res, raw := c.client.PostRaw(ctx, c.cfg.WebhookURL, "application/json; charset=utf-8", body, nil, nil)
	if res.Class != channel.ClassSent {
		return res
	}

	res, _ = checkAPIResponse(raw, res)
	return res
}

// ---------------------------------------------------------------------- app

type tokenResponse struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

func (c *Channel) fetchToken(ctx context.Context) (string, time.Duration, error) {
	endpoint, err := url.Parse(c.getTokenURL)
	if err != nil {
		return "", 0, err
	}
	query := endpoint.Query()
	query.Set("corpid", c.cfg.CorpID)
	query.Set("corpsecret", c.cfg.CorpSecret)
	endpoint.RawQuery = query.Encode()

	res, raw := c.client.PostRaw(ctx, endpoint.String(), "", nil, nil, nil)
	if res.Class != channel.ClassSent {
		return "", 0, fmt.Errorf("gettoken: %s", res.Detail)
	}

	var tok tokenResponse
	if err := json.Unmarshal(raw, &tok); err != nil {
		return "", 0, fmt.Errorf("gettoken: unreadable response: %w", err)
	}
	if tok.ErrCode != 0 || tok.AccessToken == "" {
		return "", 0, fmt.Errorf("gettoken: errcode=%d errmsg=%s", tok.ErrCode, tok.ErrMsg)
	}

	ttl := time.Duration(tok.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	return tok.AccessToken, ttl, nil
}

type appPayload struct {
	ToUser   string `json:"touser"`
	ToParty  string `json:"toparty,omitempty"`
	MsgType  string `json:"msgtype"`
	AgentID  int    `json:"agentid"`
	Markdown *struct {
		Content string `json:"content"`
	} `json:"markdown,omitempty"`
	Text *struct {
		Content string `json:"content"`
	} `json:"text,omitempty"`
}

// sendApp posts through the application API, refreshing an expired token once.
//
// A cached token being rejected is the normal lifecycle of a WeCom credential,
// not an error: the platform retires tokens on its own schedule and a process
// that has been up for two hours will hold one that no longer works. Dropping
// it and retrying is the correct response; reporting a failure would take
// notifications down for a reason that fixes itself.
func (c *Channel) sendApp(ctx context.Context, msg *message.Message) channel.Result {
	res, tokenRejected := c.sendAppOnce(ctx, msg)
	if !tokenRejected {
		return res
	}

	c.tokens.Invalidate()

	res, _ = c.sendAppOnce(ctx, msg)
	return res
}

func (c *Channel) sendAppOnce(ctx context.Context, msg *message.Message) (channel.Result, bool) {
	accessToken, err := c.tokens.Get(ctx)
	if err != nil {
		// No token means no request reached the message endpoint, so nothing
		// was rejected: this is a connection-level failure.
		return channel.ConnectError(err, "could not obtain an access token"), false
	}

	endpoint, err := url.Parse(c.sendURL)
	if err != nil {
		return channel.Permanent(err, "the API URL is not usable"), false
	}
	query := endpoint.Query()
	query.Set("access_token", accessToken)
	endpoint.RawQuery = query.Encode()

	payload := appPayload{
		ToUser:  c.cfg.ToUser,
		ToParty: c.cfg.ToParty,
		MsgType: string(c.cfg.MsgType),
		AgentID: c.cfg.AgentID,
	}
	applyContent(&payload, c.cfg.MsgType, msg)

	body, err := json.Marshal(payload)
	if err != nil {
		return channel.Permanent(err, "the payload could not be rendered"), false
	}

	res, raw := c.client.PostRaw(ctx, endpoint.String(), "application/json; charset=utf-8", body, nil, nil)
	if res.Class != channel.ClassSent {
		return res, false
	}

	res, rejected := checkAPIResponse(raw, res)
	return res, rejected
}

// ------------------------------------------------------------------ payload

func payloadFor(kind msgType, msg *message.Message) webhookPayload {
	p := webhookPayload{MsgType: string(kind)}
	if kind == msgTypeText {
		p.Text = &struct {
			Content string `json:"content"`
		}{Content: textContent(msg)}
		return p
	}
	p.Markdown = &struct {
		Content string `json:"content"`
	}{Content: markdownContent(msg)}
	return p
}

func applyContent(p *appPayload, kind msgType, msg *message.Message) {
	if kind == msgTypeText {
		p.Text = &struct {
			Content string `json:"content"`
		}{Content: textContent(msg)}
		return
	}
	p.Markdown = &struct {
		Content string `json:"content"`
	}{Content: markdownContent(msg)}
}

func severityPrefix(msg *message.Message) string {
	switch msg.Type {
	case message.TypeWarning:
		return "[WARN] "
	case message.TypeFailure:
		return "[ALERT] "
	case message.TypeSuccess:
		return "[OK] "
	default:
		return ""
	}
}

// markdownContent renders the message as WeCom markdown.
//
// WeCom's markdown supports a colour tag, which is the only way to carry
// severity in a message body; the text prefix is kept as well so the severity
// survives being copied into another system.
func markdownContent(msg *message.Message) string {
	var b strings.Builder
	if colour := severityColour(msg.Type); colour != "" {
		fmt.Fprintf(&b, "<font color=\"%s\">%s%s</font>\n", colour, severityPrefix(msg), msg.Title)
	} else {
		b.WriteString(severityPrefix(msg))
		b.WriteString(msg.Title)
		b.WriteString("\n")
	}
	b.WriteString(msg.Body)
	writeLinks(&b, msg)
	return b.String()
}

func textContent(msg *message.Message) string {
	var b strings.Builder
	b.WriteString(severityPrefix(msg))
	b.WriteString(msg.Title)
	b.WriteString("\n\n")
	b.WriteString(msg.Body)
	writeLinks(&b, msg)
	return b.String()
}

// severityColour picks a WeCom font colour.
//
// WeCom's palette is info (green), comment (gray) and warning (orange) — there
// is no red, so a failure gets the most alarming colour that exists rather
// than a colour that does not.
func severityColour(t message.Type) string {
	switch t {
	case message.TypeSuccess:
		return "info"
	case message.TypeWarning, message.TypeFailure:
		return "warning"
	default:
		return ""
	}
}

func writeLinks(b *strings.Builder, msg *message.Message) {
	for _, link := range msg.Links {
		b.WriteString("\n")
		if link.Text != "" {
			fmt.Fprintf(b, "[%s](%s)", link.Text, link.URL)
		} else {
			b.WriteString(link.URL)
		}
	}
}

type apiResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

const (
	// errCodeInvalidToken is returned when the cached access token is no
	// longer accepted.
	errCodeInvalidToken = 42001
	// Rate-limit codes. Matching on the code rather than on the message text
	// is deliberate: the text is "api freq out of limit", which contains
	// neither "frequency" nor any other word a text matcher would expect.
	errCodeFreqLimit       = 45009
	errCodeConcurrentLimit = 45033
)

func isRateLimitedCode(code int) bool {
	return code == errCodeFreqLimit || code == errCodeConcurrentLimit
}

// checkAPIResponse turns a 200 carrying a non-zero errcode into a failure.
//
// The second return value reports whether the failure was an expired token,
// which the caller handles by refreshing and trying once more rather than by
// surfacing an error.
func checkAPIResponse(raw []byte, res channel.Result) (channel.Result, bool) {
	var api apiResponse
	if err := json.Unmarshal(raw, &api); err != nil {
		return res, false
	}
	if api.ErrCode == 0 {
		return res, false
	}

	detail := fmt.Sprintf("wecom errcode=%d errmsg=%s", api.ErrCode, api.ErrMsg)
	err := fmt.Errorf("wecom error %d: %s", api.ErrCode, api.ErrMsg)

	if api.ErrCode == errCodeInvalidToken {
		return channel.Transient(err, detail), true
	}
	// A rate-limited or server-side failure is worth retrying.
	if isRateLimitedCode(api.ErrCode) || isRateLimited(api.ErrMsg) || api.ErrCode < 0 {
		return channel.Transient(err, detail), false
	}
	return channel.Permanent(err, detail), false
}

func isRateLimited(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "too many") ||
		strings.Contains(lower, "freq") ||
		strings.Contains(lower, "frequency") ||
		strings.Contains(msg, "频繁")
}
