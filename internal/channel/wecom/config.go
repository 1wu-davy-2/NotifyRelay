package wecom

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/channel/httpx"
)

// Mode is how the channel reaches WeCom.
type Mode string

const (
	// ModeWebhook posts to a group robot, with no application behind it.
	ModeWebhook Mode = "webhook"
	// ModeApp posts through an application, which can address users and
	// departments and needs an access token.
	ModeApp Mode = "app"
)

type msgType string

const (
	msgTypeMarkdown msgType = "markdown"
	msgTypeText     msgType = "text"
)

// Config is the parsed configuration of one WeCom channel instance.
type Config struct {
	Mode       Mode
	WebhookURL string
	MsgType    msgType

	// Application mode.
	CorpID     string
	CorpSecret string
	AgentID    int
	ToUser     string // "@all", a user list, or empty when ToParty is set
	ToParty    string

	Timeout    time.Duration
	RatePerSec float64
	CAFile     string
}

func parseConfig(raw map[string]any) (Config, error) {
	// The schema is the source of the bounds applied below; parseConfig does
	// not carry a second copy of them.
	specs := paramSchema()

	var cfg Config
	var err error

	mode, err := channel.StringParamOr(raw, "mode", string(ModeWebhook))
	if err != nil {
		return cfg, err
	}
	switch Mode(strings.ToLower(strings.TrimSpace(mode))) {
	case ModeWebhook:
		cfg.Mode = ModeWebhook
	case ModeApp:
		cfg.Mode = ModeApp
	default:
		return cfg, fmt.Errorf("parameter \"mode\": %q is not one of webhook, app", mode)
	}

	if cfg.MsgType, err = parseMsgType(raw); err != nil {
		return cfg, err
	}

	if cfg.Mode == ModeWebhook {
		if err := cfg.parseWebhook(raw); err != nil {
			return cfg, err
		}
	} else {
		if err := cfg.parseApp(raw); err != nil {
			return cfg, err
		}
	}

	if cfg.Timeout, err = channel.DurationParamOr(raw, "timeout", defaultTimeout); err != nil {
		return cfg, err
	}

	if cfg.RatePerSec, err = channel.FloatParamBounded(raw, specs, "rate_per_sec", defaultRatePerSec); err != nil {
		return cfg, err
	}

	if cfg.CAFile, err = channel.StringParamOr(raw, "ca_file", ""); err != nil {
		return cfg, err
	}

	return cfg, nil
}

func parseMsgType(raw map[string]any) (msgType, error) {
	value, err := channel.StringParamOr(raw, "msg_type", string(msgTypeMarkdown))
	if err != nil {
		return "", err
	}
	switch msgType(strings.ToLower(strings.TrimSpace(value))) {
	case msgTypeMarkdown:
		return msgTypeMarkdown, nil
	case msgTypeText:
		return msgTypeText, nil
	default:
		return "", fmt.Errorf("parameter \"msg_type\": %q is not one of markdown, text", value)
	}
}

func (c *Config) parseWebhook(raw map[string]any) error {
	var err error
	if c.WebhookURL, err = channel.StringParam(raw, "webhook_url"); err != nil {
		return err
	}
	if u, err := url.Parse(c.WebhookURL); err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("parameter \"webhook_url\" is not a usable https URL")
	}
	return nil
}

func (c *Config) parseApp(raw map[string]any) error {
	var err error

	if c.CorpID, err = channel.StringParam(raw, "corp_id"); err != nil {
		return err
	}
	if c.CorpSecret, err = channel.StringParam(raw, "corp_secret"); err != nil {
		return err
	}
	if c.AgentID, err = channel.IntParamOr(raw, "agent_id", 0); err != nil {
		return err
	}
	if c.AgentID <= 0 {
		return fmt.Errorf("parameter \"agent_id\" is required and must be positive in app mode")
	}
	if c.ToUser, err = channel.StringParamOr(raw, "to_user", ""); err != nil {
		return err
	}
	if c.ToParty, err = channel.StringParamOr(raw, "to_party", ""); err != nil {
		return err
	}
	if c.ToUser == "" && c.ToParty == "" {
		return fmt.Errorf("app mode needs \"to_user\" or \"to_party\" (use \"@all\" to reach everyone)")
	}
	return nil
}

func paramSchema() []channel.ParamSpec {
	zero := 0.0
	when := func(mode string) *channel.Condition {
		return &channel.Condition{Field: "mode", Equals: mode}
	}

	specs := []channel.ParamSpec{
		{
			Name: "mode", Type: channel.ParamEnum, Values: []string{"webhook", "app"}, Default: "webhook",
			Label: "Mode",
			Desc:  "webhook posts to a group robot; app posts through an application and needs the credentials below.",
		},
		{
			// The two modes need disjoint sets of fields, which is the case
			// ShowIf exists for: without it a generated form shows seven
			// optional boxes and marks none of them required, and the operator
			// finds out which ones mattered when the service refuses to start.
			Name: "webhook_url", Type: channel.ParamString, Private: true, ShowIf: when("webhook"),
			Label: "Group robot webhook",
			Desc:  "Required in webhook mode. https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...",
		},
		{
			Name: "corp_id", Type: channel.ParamString, ShowIf: when("app"),
			Label: "Corp ID", Desc: "Required in app mode.",
		},
		{
			Name: "corp_secret", Type: channel.ParamString, Private: true, ShowIf: when("app"),
			Label: "Corp secret", Desc: "Required in app mode. Write as `!env WECOM_SECRET`.",
		},
		{
			// No Min: the "must be positive" rule holds in app mode only, and a
			// bound is not conditional. Declaring Min 1 would reject the absent
			// value in webhook mode, where 0 is the right answer. The check
			// stays in parseConfig, next to the mode that makes it true.
			Name: "agent_id", Type: channel.ParamInt, ShowIf: when("app"),
			Label: "Agent ID", Desc: "Required in app mode.",
		},
		{
			Name: "to_user", Type: channel.ParamString, Default: "", ShowIf: when("app"),
			Label: "To users",
			Desc:  "App mode. \"@all\", or a '|'-separated user list. One of this and To departments is required; the empty default is what says neither is required on its own.",
		},
		{
			Name: "to_party", Type: channel.ParamString, Default: "", ShowIf: when("app"),
			Label: "To departments",
			Desc:  "App mode. A '|'-separated department list. One of this and To users is required.",
		},
		{
			Name: "msg_type", Type: channel.ParamEnum, Values: []string{"markdown", "text"}, Default: "markdown",
			Label: "Message type",
		},
		{
			Name: "timeout", Type: channel.ParamDuration, Default: "10s",
			Label: "Request timeout",
		},
		{
			Name: "rate_per_sec", Type: channel.ParamFloat, Default: defaultRatePerSec, Min: &zero,
			Label: "Rate limit", Desc: "Messages per second. A group robot allows 20 per minute.",
		},
	}

	return append(specs, httpx.ParamSpec())
}
