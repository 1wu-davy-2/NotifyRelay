package dingtalk

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
	msgTypeMarkdown msgType = "markdown"
	msgTypeText     msgType = "text"
)

// Config is the parsed configuration of one DingTalk channel instance.
type Config struct {
	WebhookURL string
	Secret     string // empty disables signing
	MsgType    msgType
	AtMobiles  []string
	AtAll      bool
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

	rawType, err := channel.StringParamOr(raw, "msg_type", string(msgTypeMarkdown))
	if err != nil {
		return cfg, err
	}
	switch msgType(strings.ToLower(strings.TrimSpace(rawType))) {
	case msgTypeMarkdown:
		cfg.MsgType = msgTypeMarkdown
	case msgTypeText:
		cfg.MsgType = msgTypeText
	default:
		return cfg, fmt.Errorf("parameter \"msg_type\": %q is not one of markdown, text", rawType)
	}

	if cfg.AtMobiles, err = channel.StringSliceParam(raw, "at_mobiles"); err != nil {
		return cfg, err
	}
	if cfg.AtAll, err = channel.BoolParamOr(raw, "at_all", false); err != nil {
		return cfg, err
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
			Label: "Robot webhook URL",
			Desc:  "https://oapi.dingtalk.com/robot/send?access_token=... The token lives in the URL.",
		},
		{
			Name: "secret", Type: channel.ParamString, Private: true,
			Label: "Signing secret",
			Desc:  "Optional. When set, requests are signed. Write as `!env DINGTALK_SECRET`.",
		},
		{
			Name: "msg_type", Type: channel.ParamEnum, Values: []string{"markdown", "text"}, Default: "markdown",
			Label: "Message type",
		},
		{
			Name: "at_mobiles", Type: channel.ParamStringList,
			Label: "@ phone numbers", Desc: "Phone numbers to mention.",
		},
		{
			Name: "at_all", Type: channel.ParamBool,
			Label: "@ everyone", Desc: "Mention the whole group. Use sparingly.",
		},
		{
			Name: "timeout", Type: channel.ParamDuration, Default: "10s",
			Label: "Request timeout",
		},
		{
			Name: "rate_per_sec", Type: channel.ParamFloat, Default: defaultRatePerSec,
			Label: "Rate limit",
			Desc:  "Messages per second. DingTalk allows 20 per minute; exceeding it mutes the robot for ten minutes.",
		},
	}

	return append(specs, httpx.ParamSpec())
}
