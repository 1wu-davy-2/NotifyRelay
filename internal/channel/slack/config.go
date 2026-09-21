package slack

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/channel/httpx"
)

const (
	defaultTimeout = 10 * time.Second
	// Slack rate-limits incoming webhooks to about one message per second,
	// and chat.postMessage to roughly the same per channel. Being slower than
	// the limit costs latency; being faster costs a 429 and a lost message.
	defaultRatePerSec = 1.0
	// Slack truncates text beyond this rather than rejecting it, which would
	// silently cut a notification in half. The router splits instead.
	defaultBodyMaxLen = 4000
)

// Mode is how the channel reaches Slack.
type Mode string

const (
	// ModeWebhook posts to an incoming webhook URL.
	ModeWebhook Mode = "webhook"
	// ModeBotToken posts to chat.postMessage with a bot token.
	ModeBotToken Mode = "bot_token"
)

// Config is the parsed configuration of one Slack channel instance.
type Config struct {
	Mode       Mode
	WebhookURL string
	Token      string
	Channel    string
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

	if cfg.WebhookURL, err = channel.StringParamOr(raw, "webhook_url", ""); err != nil {
		return cfg, err
	}
	if cfg.Token, err = channel.StringParamOr(raw, "token", ""); err != nil {
		return cfg, err
	}
	if cfg.Channel, err = channel.StringParamOr(raw, "channel", ""); err != nil {
		return cfg, err
	}

	// Exactly one mode. Accepting both would leave it ambiguous which one is
	// live, and the operator would find out by watching the wrong place.
	switch {
	case cfg.WebhookURL != "" && cfg.Token != "":
		return cfg, fmt.Errorf("set either \"webhook_url\" or \"token\", not both")
	case cfg.WebhookURL != "":
		cfg.Mode = ModeWebhook
		if u, err := url.Parse(cfg.WebhookURL); err != nil || u.Scheme != "https" || u.Host == "" {
			return cfg, fmt.Errorf("parameter \"webhook_url\" is not a usable https URL")
		}
	case cfg.Token != "":
		cfg.Mode = ModeBotToken
		if cfg.Channel == "" {
			return cfg, fmt.Errorf("parameter \"channel\" is required when using \"token\"")
		}
	default:
		return cfg, fmt.Errorf("one of \"webhook_url\" or \"token\" is required")
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

func paramSchema() []channel.ParamSpec {
	zero := 0.0

	specs := []channel.ParamSpec{
		{
			Name: "webhook_url", Type: channel.ParamString, Private: true,
			Label: "Incoming webhook URL",
			Desc:  "The secret lives in the URL. Mutually exclusive with token.",
		},
		{
			Name: "token", Type: channel.ParamString, Private: true,
			Label: "Bot token",
			Desc:  "Post with chat.postMessage instead. Requires \"channel\". Write as `!env SLACK_TOKEN`.",
		},
		{
			Name: "channel", Type: channel.ParamString,
			Label: "Channel", Desc: "Channel ID or name, used with a bot token.",
		},
		{
			Name: "timeout", Type: channel.ParamDuration, Default: "10s",
			Label: "Request timeout",
		},
		{
			Name: "rate_per_sec", Type: channel.ParamFloat, Default: defaultRatePerSec, Min: &zero,
			Label: "Rate limit",
			Desc:  "Messages per second. Slack allows about one; 0 disables the limit.",
		},
	}

	return append(specs, httpx.ParamSpec())
}

// normaliseChannel accepts a channel name with or without its leading '#'.
func normaliseChannel(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), "#")
}
