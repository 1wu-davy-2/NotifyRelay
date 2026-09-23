// Package webhook delivers notifications to a generic HTTP endpoint.
//
// It is the escape hatch of the channel set: anything that can receive a JSON
// POST can be a notification target without waiting for a bespoke channel to
// be written. It is also the second channel implementation, and exists to
// prove that adding one touches nothing outside this package.
package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/channel/httpauth"
	"notifyrelay/internal/channel/httpx"
	"notifyrelay/internal/message"
)

func init() {
	channel.Register(channel.Descriptor{
		Type:        "webhook",
		ParamSchema: paramSchema(),
		Capability: channel.Capability{
			// A generic endpoint carries anything; the operator declares the
			// receiver's real limits through the config.
			SupportedFormats: []message.Format{message.FormatText, message.FormatMarkdown, message.FormatHTML},
			OverflowMode:     channel.OverflowError,
		},
		Factory: New,
	})
}

// Channel posts a JSON payload to one endpoint.
type Channel struct {
	instance string
	cfg      Config
	auth     httpauth.Authenticator
	client   *httpx.Client
}

// New builds a webhook channel instance from a configuration block.
func New(instance string, raw map[string]any) (channel.Channel, error) {
	cfg, err := parseConfig(raw)
	if err != nil {
		return nil, err
	}

	auth, err := httpauth.FromParams(cfg.Auth)
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
		auth:     auth,
		client:   client,
	}, nil
}

// Type implements channel.Channel.
func (c *Channel) Type() string { return "webhook" }

// payloadOverheadBytes covers the JSON envelope's keys and punctuation, and
// the room that escaping a quote or a control character can add.
const (
	payloadOverheadRunes = 64
	payloadOverheadBytes = 256
)

// Capability implements channel.Channel, honouring the operator's overrides.
func (c *Channel) Capability() channel.Capability {
	return channel.Capability{
		BodyMaxLen:        c.cfg.BodyMaxLen,
		TitleMaxLen:       c.cfg.TitleMaxLen,
		SupportedFormats:  []message.Format{message.FormatText, message.FormatMarkdown, message.FormatHTML},
		RatePerSec:        c.cfg.RatePerSec,
		OverflowMode:      c.cfg.OverflowMode,
		SupportAttachment: false,

		PayloadOverheadRunes: payloadOverheadRunes,
		PayloadOverheadBytes: payloadOverheadBytes,
	}
}

// ParamSchema implements channel.Channel.
func (c *Channel) ParamSchema() []channel.ParamSpec { return paramSchema() }

// Send implements channel.Channel.
func (c *Channel) Send(ctx context.Context, msg *message.Message, _ channel.Target) channel.Result {
	body, contentType, err := c.payload(msg)
	if err != nil {
		return channel.Permanent(err, "the payload could not be rendered")
	}

	return c.client.Post(ctx, c.cfg.URL, contentType, body, nil, c.auth)
}

// Test reports whether the configuration is usable.
//
// A webhook endpoint has no side-effect-free probe: the only way to test one
// is to post a real payload to it, which would show up as a notification. So
// this validates what can be validated locally and says so, rather than
// firing a test message at somebody's production endpoint.
func (c *Channel) Test(context.Context) channel.Result {
	if c.auth == nil {
		return channel.Permanent(fmt.Errorf("no authenticator"), "the authentication could not be built")
	}
	return channel.Sent("configuration is valid (a webhook has no side-effect-free probe)")
}

type payloadBody struct {
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	Format    string         `json:"format"`
	Type      string         `json:"type"`
	Priority  int            `json:"priority"`
	Tags      []string       `json:"tags,omitempty"`
	Links     []message.Link `json:"links,omitempty"`
	At        []string       `json:"at,omitempty"`
	Source    string         `json:"source,omitempty"`
	Timestamp string         `json:"timestamp"`
}

func (c *Channel) payload(msg *message.Message) ([]byte, string, error) {
	if c.cfg.PayloadTemplate != "" {
		rendered := message.RenderJSON(c.cfg.PayloadTemplate, msg.Vars())
		// Reject a template that does not produce JSON: a receiver would
		// otherwise report a parse error with no hint of where it came from.
		if !json.Valid([]byte(rendered)) {
			return nil, "", fmt.Errorf("payload_template did not render to valid JSON: %s", truncate(rendered, 200))
		}
		return []byte(rendered), c.cfg.ContentType, nil
	}

	body, err := json.Marshal(payloadBody{
		Title:     msg.Title,
		Body:      msg.Body,
		Format:    string(msg.Format),
		Type:      string(msg.Type),
		Priority:  int(msg.Priority),
		Tags:      msg.Tags,
		Links:     msg.Links,
		At:        msg.At,
		Source:    c.instance,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, "", err
	}
	return body, c.cfg.ContentType, nil
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
