// Package dingtalk delivers notifications to DingTalk group robots.
//
// The signature algorithm here is DingTalk's, which differs from Feishu's; see
// sign.go. It is implemented from the published specification and exercised by
// tests, but only a real robot can confirm the platform accepts it.
package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/channel/httpx"
	"notifyrelay/internal/message"
	"notifyrelay/internal/render"
)

const (
	defaultTimeout = 10 * time.Second
	// DingTalk's documented robot limit is 20 messages per minute. Exceeding
	// it gets the robot muted for ten minutes, which is a far worse outcome
	// than a notification arriving a few seconds late.
	defaultRatePerSec = 20.0 / 60.0

	// A robot message is capped at 20000 bytes. The character limit is set
	// conservatively so that a body of four-byte characters also stays inside
	// the byte cap.
	defaultBodyMaxBytes = 20000
	defaultBodyMaxLen   = 5000

	// What the channel wraps around the body: the severity marker, the blank
	// line after the title and the separator before the links.
	payloadOverheadRunes = 32
	payloadOverheadBytes = 32
)

func init() {
	channel.Register(channel.Descriptor{
		Type:        "dingtalk",
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
		MarkdownDialect:  render.DialectDingTalk,
		RatePerSec:       rate,
		OverflowMode:     channel.OverflowSplit,

		PayloadOverheadRunes: payloadOverheadRunes,
		PayloadOverheadBytes: payloadOverheadBytes,
	}
}

// Channel posts messages to a DingTalk robot.
type Channel struct {
	instance string
	cfg      Config
	client   *httpx.Client
	now      func() time.Time // injectable for signature tests
}

// New builds a DingTalk channel instance from a configuration block.
func New(instance string, raw map[string]any) (channel.Channel, error) {
	cfg, err := parseConfig(raw)
	if err != nil {
		return nil, err
	}

	client, err := httpx.NewClientFromConfig(cfg.Timeout, cfg.CAFile)
	if err != nil {
		return nil, err
	}

	return &Channel{
		instance: instance,
		cfg:      cfg,
		client:   client,
		now:      time.Now,
	}, nil
}

// Type implements channel.Channel.
func (c *Channel) Type() string { return "dingtalk" }

// Capability implements channel.Channel.
func (c *Channel) Capability() channel.Capability { return capability(c.cfg.RatePerSec) }

// ParamSchema implements channel.Channel.
func (c *Channel) ParamSchema() []channel.ParamSpec { return paramSchema() }

// Send implements channel.Channel.
func (c *Channel) Send(ctx context.Context, msg *message.Message, _ channel.Target) channel.Result {
	endpoint, err := signedURL(c.cfg.WebhookURL, c.cfg.Secret, c.now().UnixMilli())
	if err != nil {
		return channel.Permanent(err, "the webhook URL could not be signed")
	}

	body, err := json.Marshal(c.payload(msg))
	if err != nil {
		return channel.Permanent(err, "the payload could not be rendered")
	}

	res, raw := c.client.PostRaw(ctx, endpoint, "application/json; charset=utf-8", body, nil, nil)
	if res.Class != channel.ClassSent {
		return res
	}
	return checkAPIResponse(raw, res)
}

// Test verifies that the webhook URL is usable, without posting a message.
//
// DingTalk has no side-effect-free probe for a robot webhook, so this checks
// only what can be checked locally and says so.
func (c *Channel) Test(context.Context) channel.Result {
	if _, err := url.Parse(c.cfg.WebhookURL); err != nil {
		return channel.Permanent(err, "the webhook URL is not usable")
	}
	return channel.Sent("configuration is valid (a robot webhook has no side-effect-free probe)")
}

type markdownBody struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

type textBody struct {
	Content string `json:"content"`
}

type atBody struct {
	AtMobiles []string `json:"atMobiles,omitempty"`
	IsAtAll   bool     `json:"isAtAll,omitempty"`
}

type payloadBody struct {
	MsgType  string        `json:"msgtype"`
	Markdown *markdownBody `json:"markdown,omitempty"`
	Text     *textBody     `json:"text,omitempty"`
	At       *atBody       `json:"at,omitempty"`
}

func (c *Channel) payload(msg *message.Message) payloadBody {
	body := payloadBody{MsgType: string(c.cfg.MsgType)}

	if c.cfg.MsgType == msgTypeText {
		body.Text = &textBody{Content: plainText(msg)}
	} else {
		// DingTalk shows the title in the notification preview and the text in
		// the conversation, so both carry the severity marker.
		body.Markdown = &markdownBody{
			Title: severityTitle(msg),
			Text:  markdownText(msg),
		}
	}

	if len(c.cfg.AtMobiles) > 0 || c.cfg.AtAll {
		body.At = &atBody{AtMobiles: c.cfg.AtMobiles, IsAtAll: c.cfg.AtAll}
	}
	return body
}

func severityTitle(msg *message.Message) string {
	return severityPrefix(msg) + msg.Title
}

func markdownText(msg *message.Message) string {
	var b strings.Builder
	b.WriteString(severityPrefix(msg))
	b.WriteString(msg.Title)
	b.WriteString("\n\n")
	b.WriteString(msg.Body)
	writeLinks(&b, msg)
	return b.String()
}

func plainText(msg *message.Message) string {
	var b strings.Builder
	b.WriteString(severityTitle(msg))
	b.WriteString("\n\n")
	b.WriteString(msg.Body)
	writeLinks(&b, msg)
	return b.String()
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

type apiResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// checkAPIResponse turns a 200 carrying a non-zero errcode into a failure.
//
// DingTalk answers HTTP 200 for application errors, so the transport result
// alone cannot be trusted.
func checkAPIResponse(raw []byte, res channel.Result) channel.Result {
	var api apiResponse
	if err := json.Unmarshal(raw, &api); err != nil {
		return res
	}
	if api.ErrCode == 0 {
		return res
	}

	detail := fmt.Sprintf("dingtalk errcode=%d errmsg=%s", api.ErrCode, api.ErrMsg)
	err := fmt.Errorf("dingtalk error %d: %s", api.ErrCode, api.ErrMsg)

	// 130101 is the documented "send too fast" code; a negative code is a
	// server-side fault, which is equally worth retrying.
	if api.ErrCode < 0 || api.ErrCode == 130101 || isRateLimited(api.ErrMsg) {
		return channel.Transient(err, detail)
	}
	return channel.Permanent(err, detail)
}

func isRateLimited(errmsg string) bool {
	lower := strings.ToLower(errmsg)
	return strings.Contains(lower, "too fast") ||
		strings.Contains(lower, "frequent") ||
		strings.Contains(errmsg, "频繁")
}
