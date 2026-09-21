// Package slack delivers notifications to Slack.
//
// Slack renders mrkdwn, which is not markdown: bold is *one star*, links are
// <angle|brackets>, and there are no headings. The channel declares the
// dialect and the core converts CommonMark into it, so this package contains
// no converter of its own.
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/channel/httpauth"
	"notifyrelay/internal/channel/httpx"
	"notifyrelay/internal/message"
	"notifyrelay/internal/render"
)

const (
	postMessageURL = "https://slack.com/api/chat.postMessage"
	authTestURL    = "https://slack.com/api/auth.test"
)

func init() {
	channel.Register(channel.Descriptor{
		Type:        "slack",
		ParamSchema: paramSchema(),
		Capability:  capability(defaultRatePerSec),
		Factory:     New,
	})
}

// payloadOverheadRunes is what the channel wraps around the body: the severity
// marker, the newline after the title and the separator before the links.
const payloadOverheadRunes = 32

func capability(rate float64) channel.Capability {
	return channel.Capability{
		BodyMaxLen:       defaultBodyMaxLen,
		SupportedFormats: []message.Format{message.FormatText, message.FormatMarkdown},
		MarkdownDialect:  render.DialectSlack,
		RatePerSec:       rate,
		OverflowMode:     channel.OverflowSplit,

		PayloadOverheadRunes: payloadOverheadRunes,
		PayloadOverheadBytes: payloadOverheadRunes,
	}
}

// Channel posts messages to Slack.
type Channel struct {
	instance string
	cfg      Config
	client   *httpx.Client

	// API endpoints. Held as fields rather than used as constants so a test
	// can point them at a local server; a hardcoded host would make the
	// channel's real request path untestable.
	postURL string
	authURL string
}

// New builds a Slack channel instance from a configuration block.
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
		postURL:  postMessageURL,
		authURL:  authTestURL,
	}, nil
}

// Type implements channel.Channel.
func (c *Channel) Type() string { return "slack" }

// Capability implements channel.Channel.
func (c *Channel) Capability() channel.Capability { return capability(c.cfg.RatePerSec) }

// ParamSchema implements channel.Channel.
func (c *Channel) ParamSchema() []channel.ParamSpec { return paramSchema() }

// Send implements channel.Channel.
func (c *Channel) Send(ctx context.Context, msg *message.Message) channel.Result {
	body, url, auth, err := c.request(msg)
	if err != nil {
		return channel.Permanent(err, "the payload could not be rendered")
	}

	res, raw := c.client.PostRaw(ctx, url, "application/json; charset=utf-8", body, nil, auth)
	if res.Class != channel.ClassSent || c.cfg.Mode != ModeBotToken {
		return res
	}

	// chat.postMessage reports application errors inside a 200 response, so a
	// 200 alone does not mean the message was delivered.
	return checkAPIResponse(raw, res)
}

// Test implements channel.Channel. Slack's auth.test requires a bot token, so
// webhook mode has nothing safe to probe.
func (c *Channel) Test(ctx context.Context) channel.Result {
	if c.cfg.Mode != ModeBotToken {
		return channel.Sent("configuration is valid (an incoming webhook has no side-effect-free probe)")
	}

	res, raw := c.client.PostRaw(ctx,
		c.authURL,
		"application/json; charset=utf-8",
		nil, nil,
		httpauth.Bearer(c.cfg.Token),
	)
	if res.Class != channel.ClassSent {
		return res
	}
	return checkAPIResponse(raw, res)
}

func (c *Channel) request(msg *message.Message) ([]byte, string, httpauth.Authenticator, error) {
	text := renderText(msg)

	payload := map[string]string{"text": text}
	url := c.cfg.WebhookURL
	auth := httpauth.None()

	if c.cfg.Mode == ModeBotToken {
		payload["channel"] = normaliseChannel(c.cfg.Channel)
		url = c.postURL
		auth = httpauth.Bearer(c.cfg.Token)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", nil, err
	}
	return body, url, auth, nil
}

// renderText flattens a message into Slack's single text field.
//
// Slack has no separate title, so the title becomes the first line with the
// severity marker that mail puts in the subject line.
func renderText(msg *message.Message) string {
	var b strings.Builder

	if prefix := severityPrefix(msg); prefix != "" {
		b.WriteString(prefix)
	}
	b.WriteString(msg.Title)
	b.WriteString("\n")
	b.WriteString(msg.Body)

	for _, link := range msg.Links {
		b.WriteString("\n")
		if link.Text != "" {
			fmt.Fprintf(&b, "%s: %s", link.Text, link.URL)
		} else {
			b.WriteString(link.URL)
		}
	}

	return b.String()
}

// severityPrefix marks the severity, since plain text carries no styling.
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
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// checkAPIResponse turns a well-formed but unsuccessful API reply into a
// failure, instead of leaving it to look like a delivered message.
func checkAPIResponse(raw []byte, res channel.Result) channel.Result {
	var api apiResponse
	if err := json.Unmarshal(raw, &api); err != nil {
		// Not a Slack API envelope. The webhook form answers with bare text on
		// success, so an unparseable body here is not itself a failure.
		return res
	}
	if api.OK {
		return res
	}

	detail := "slack rejected the message: " + api.Error
	err := fmt.Errorf("slack api error: %s", api.Error)

	// Being rate limited is the one API error that a retry can fix.
	if strings.Contains(strings.ToLower(api.Error), "ratelimit") {
		return channel.Transient(err, detail)
	}
	return channel.Permanent(err, detail)
}
