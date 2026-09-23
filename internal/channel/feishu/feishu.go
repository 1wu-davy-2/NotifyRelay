// Package feishu delivers notifications to Feishu (Lark) custom bots.
//
// The signature algorithm differs from DingTalk's; see sign.go. It is
// implemented from the published specification and exercised by tests, but
// only a real bot can confirm the platform accepts it.
package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/channel/httpx"
	"notifyrelay/internal/message"
	"notifyrelay/internal/render"
)

const (
	defaultTimeout = 10 * time.Second
	// Feishu allows roughly 100 custom-bot messages per minute.
	defaultRatePerSec = 100.0 / 60.0

	// Cards hold far more than a plain text message, so the limits are set by
	// what stays readable rather than by a hard platform cap.
	defaultBodyMaxBytes = 20000
	defaultBodyMaxLen   = 8000

	// The card wraps the body in "**title**\n" plus the link separator.
	payloadOverheadRunes = 32
	payloadOverheadBytes = 32
)

func init() {
	channel.Register(channel.Descriptor{
		Type:        "feishu",
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
		// lark_md has no heading syntax, so the core converts '#' headings to
		// bold rather than leaving literal hashes in the card.
		MarkdownDialect: render.DialectFeishu,
		RatePerSec:      rate,
		OverflowMode:    channel.OverflowSplit,

		PayloadOverheadRunes: payloadOverheadRunes,
		PayloadOverheadBytes: payloadOverheadBytes,
	}
}

// Channel posts messages to a Feishu custom bot.
type Channel struct {
	instance string
	cfg      Config
	client   *httpx.Client
	now      func() time.Time
}

// New builds a Feishu channel instance from a configuration block.
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
func (c *Channel) Type() string { return "feishu" }

// Capability implements channel.Channel.
func (c *Channel) Capability() channel.Capability { return capability(c.cfg.RatePerSec) }

// ParamSchema implements channel.Channel.
func (c *Channel) ParamSchema() []channel.ParamSpec { return paramSchema() }

// Send implements channel.Channel.
func (c *Channel) Send(ctx context.Context, msg *message.Message, _ channel.Target) channel.Result {
	body, err := json.Marshal(c.payload(msg))
	if err != nil {
		return channel.Permanent(err, "the payload could not be rendered")
	}

	res, raw := c.client.PostRaw(ctx, c.cfg.WebhookURL, "application/json; charset=utf-8", body, nil, nil)
	if res.Class != channel.ClassSent {
		return res
	}
	return checkAPIResponse(raw, res)
}

// Test reports whether the configuration is usable. A custom bot has no
// side-effect-free probe.
func (c *Channel) Test(context.Context) channel.Result {
	return channel.Sent("configuration is valid (a custom bot has no side-effect-free probe)")
}

type cardText struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

type cardHeader struct {
	Template string   `json:"template"`
	Title    cardText `json:"title"`
}

type cardElement struct {
	Tag  string   `json:"tag"`
	Text cardText `json:"text"`
}

type card struct {
	Config   map[string]any `json:"config"`
	Header   cardHeader     `json:"header,omitempty"`
	Elements []cardElement  `json:"elements"`
}

type payloadBody struct {
	MsgType   string `json:"msg_type"`
	Timestamp string `json:"timestamp,omitempty"`
	Sign      string `json:"sign,omitempty"`
	Card      *card  `json:"card,omitempty"`
	Content   *struct {
		Text string `json:"text"`
	} `json:"content,omitempty"`
}

func (c *Channel) payload(msg *message.Message) payloadBody {
	body := payloadBody{MsgType: string(c.cfg.MsgType)}

	if c.cfg.Secret != "" {
		// Feishu takes the timestamp as a string, in seconds.
		ts := c.now().Unix()
		body.Timestamp = strconv.FormatInt(ts, 10)
		body.Sign = sign(c.cfg.Secret, ts)
	}

	if c.cfg.MsgType == msgTypeText {
		body.Content = &struct {
			Text string `json:"text"`
		}{Text: textContent(msg)}
		return body
	}

	body.Card = &card{
		Config: map[string]any{"wide_screen_mode": true},
		Header: cardHeader{
			Template: headerColour(msg.Type),
			Title:    cardText{Tag: "plain_text", Content: title(msg)},
		},
		Elements: []cardElement{{
			Tag:  "div",
			Text: cardText{Tag: "lark_md", Content: markdownContent(msg)},
		}},
	}
	return body
}

// headerColour carries the severity, which is the one thing a card shows
// before anyone reads it.
func headerColour(t message.Type) string {
	switch t {
	case message.TypeSuccess:
		return "green"
	case message.TypeWarning:
		return "orange"
	case message.TypeFailure:
		return "red"
	default:
		return "blue"
	}
}

func title(msg *message.Message) string {
	return severityPrefix(msg) + msg.Title
}

func markdownContent(msg *message.Message) string {
	var b strings.Builder
	b.WriteString("**")
	b.WriteString(msg.Title)
	b.WriteString("**\n")
	b.WriteString(msg.Body)
	writeLinks(&b, msg)
	return b.String()
}

func textContent(msg *message.Message) string {
	var b strings.Builder
	b.WriteString(title(msg))
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

// apiResponse covers both envelopes Feishu uses: the older StatusCode form and
// the current code/msg form. A bot can answer with either.
type apiResponse struct {
	Code          int    `json:"code"`
	Msg           string `json:"msg"`
	StatusCode    int    `json:"StatusCode"`
	StatusMessage string `json:"StatusMessage"`
}

// checkAPIResponse turns a 200 carrying an error code into a failure.
func checkAPIResponse(raw []byte, res channel.Result) channel.Result {
	var api apiResponse
	if err := json.Unmarshal(raw, &api); err != nil {
		return res
	}

	code, msg := api.Code, api.Msg
	if code == 0 && api.StatusCode != 0 {
		code, msg = api.StatusCode, api.StatusMessage
	}
	if code == 0 {
		return res
	}

	detail := fmt.Sprintf("feishu code=%d msg=%s", code, msg)
	err := fmt.Errorf("feishu error %d: %s", code, msg)

	// A rate-limited bot is told to slow down; the request itself was fine.
	if isRateLimited(msg) {
		return channel.Transient(err, detail)
	}
	return channel.Permanent(err, detail)
}

func isRateLimited(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "too many") ||
		strings.Contains(lower, "rate") ||
		strings.Contains(lower, "frequency") ||
		strings.Contains(msg, "频繁")
}
