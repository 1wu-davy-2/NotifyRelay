package feishu

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/channel/httpx"
)

type msgType string

const (
	msgTypeInteractive msgType = "interactive"
	msgTypeText        msgType = "text"
)

// Config is the parsed configuration of one Feishu channel instance.
type Config struct {
	WebhookURL string
	Secret     string // empty disables signing
	MsgType    msgType
	Timeout    time.Duration
	RatePerSec float64
	CAFile     string
}

func parseConfig(raw map[string]any) (Config, error) {
	var cfg Config
	var err error

	if cfg.WebhookURL, err = channel.StringParam(raw, "webhook_url"); err != nil {
		return cfg, err
	}
	if u, err := url.Parse(cfg.WebhookURL); err != nil || u.Scheme != "https" || u.Host == "" {
		return cfg, fmt.Errorf("parameter \"webhook_url\" is not a usable https URL")
	}

	if cfg.Secret, err = channel.StringParamOr(raw, "secret", ""); err != nil {
		return cfg, err
	}

	rawType, err := channel.StringParamOr(raw, "msg_type", string(msgTypeInteractive))
	if err != nil {
		return cfg, err
	}
	switch msgType(strings.ToLower(strings.TrimSpace(rawType))) {
	case msgTypeInteractive:
		cfg.MsgType = msgTypeInteractive
	case msgTypeText:
		cfg.MsgType = msgTypeText
	default:
		return cfg, fmt.Errorf("parameter \"msg_type\": %q is not one of interactive, text", rawType)
	}

	if cfg.Timeout, err = channel.DurationParamOr(raw, "timeout", defaultTimeout); err != nil {
		return cfg, err
	}
	if cfg.Timeout <= 0 {
		return cfg, fmt.Errorf("parameter \"timeout\" must be greater than zero")
	}

	if cfg.RatePerSec, err = channel.FloatParamOr(raw, "rate_per_sec", defaultRatePerSec); err != nil {
		return cfg, err
	}
	if cfg.RatePerSec < 0 {
		return cfg, fmt.Errorf("parameter \"rate_per_sec\" must not be negative")
	}

	if cfg.CAFile, err = channel.StringParamOr(raw, "ca_file", ""); err != nil {
		return cfg, err
	}

	return cfg, nil
}

func paramSchema() []channel.ParamSpec {
	specs := []channel.ParamSpec{
		{
			Name: "webhook_url", Type: channel.ParamString, Required: true, Private: true,
			Label: "Bot webhook URL",
			Desc:  "https://open.feishu.cn/open-apis/bot/v2/hook/... The secret lives in the URL.",
		},
		{
			Name: "secret", Type: channel.ParamString, Private: true,
			Label: "Signing secret",
			Desc:  "Optional. When set, requests are signed. Write as `!env FEISHU_SECRET`.",
		},
		{
			Name: "msg_type", Type: channel.ParamEnum, Values: []string{"interactive", "text"}, Default: "interactive",
			Label: "Message type",
			Desc:  "interactive sends a card, whose header colour carries the severity.",
		},
		{
			Name: "timeout", Type: channel.ParamDuration, Default: "10s",
			Label: "Request timeout",
		},
		{
			Name: "rate_per_sec", Type: channel.ParamFloat, Default: defaultRatePerSec,
			Label: "Rate limit", Desc: "Messages per second.",
		},
	}

	return append(specs, httpx.ParamSpec())
}
