package webhook

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/channel/httpauth"
	"notifyrelay/internal/channel/httpx"
)

const (
	defaultTimeout   = 10 * time.Second
	defaultRateLimit = 0 // unlimited unless the operator says otherwise
)

// Config is the parsed configuration of one webhook channel instance.
type Config struct {
	URL             string
	Method          string
	ContentType     string
	PayloadTemplate string
	Timeout         time.Duration

	// Capability overrides. A generic HTTP endpoint has no inherent limits,
	// so the operator declares them when the receiver does.
	BodyMaxLen   int
	TitleMaxLen  int
	OverflowMode channel.OverflowMode
	RatePerSec   float64
	CAFile       string

	Auth httpauth.Params
}

func parseConfig(raw map[string]any) (Config, error) {
	var cfg Config
	var err error

	if cfg.URL, err = channel.StringParam(raw, "url"); err != nil {
		return cfg, err
	}
	parsed, err := url.Parse(cfg.URL)
	if err != nil {
		return cfg, fmt.Errorf("parameter \"url\": %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return cfg, fmt.Errorf("parameter \"url\": scheme must be http or https, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return cfg, fmt.Errorf("parameter \"url\": no host in %q", cfg.URL)
	}

	if cfg.Method, err = channel.StringParamOr(raw, "method", "POST"); err != nil {
		return cfg, err
	}
	cfg.Method = strings.ToUpper(cfg.Method)
	if cfg.Method != "POST" && cfg.Method != "PUT" {
		return cfg, fmt.Errorf("parameter \"method\": %q is not supported (want POST or PUT)", cfg.Method)
	}

	if cfg.ContentType, err = channel.StringParamOr(raw, "content_type", "application/json; charset=utf-8"); err != nil {
		return cfg, err
	}

	if cfg.PayloadTemplate, err = channel.StringParamOr(raw, "payload_template", ""); err != nil {
		return cfg, err
	}

	if cfg.Timeout, err = channel.DurationParamOr(raw, "timeout", defaultTimeout); err != nil {
		return cfg, err
	}
	if cfg.Timeout <= 0 {
		return cfg, fmt.Errorf("parameter \"timeout\" must be greater than zero")
	}

	if cfg.BodyMaxLen, err = channel.IntParamOr(raw, "body_max_len", 0); err != nil {
		return cfg, err
	}
	if cfg.BodyMaxLen < 0 {
		return cfg, fmt.Errorf("parameter \"body_max_len\" must not be negative")
	}

	if cfg.TitleMaxLen, err = channel.IntParamOr(raw, "title_max_len", 0); err != nil {
		return cfg, err
	}
	if cfg.TitleMaxLen < 0 {
		return cfg, fmt.Errorf("parameter \"title_max_len\" must not be negative")
	}

	overflowMode, err := channel.StringParamOr(raw, "overflow_mode", "error")
	if err != nil {
		return cfg, err
	}
	if cfg.OverflowMode, err = parseOverflowMode(overflowMode); err != nil {
		return cfg, err
	}

	if cfg.RatePerSec, err = channel.FloatParamOr(raw, "rate_per_sec", defaultRateLimit); err != nil {
		return cfg, err
	}
	if cfg.RatePerSec < 0 {
		return cfg, fmt.Errorf("parameter \"rate_per_sec\" must not be negative")
	}

	if cfg.CAFile, err = channel.StringParamOr(raw, "ca_file", ""); err != nil {
		return cfg, err
	}

	if cfg.Auth, err = parseAuthParams(raw); err != nil {
		return cfg, err
	}

	return cfg, nil
}

func parseAuthParams(raw map[string]any) (httpauth.Params, error) {
	var p httpauth.Params
	var err error

	if p.Type, err = channel.StringParamOr(raw, "auth_type", "none"); err != nil {
		return p, err
	}
	if p.Token, err = channel.StringParamOr(raw, "token", ""); err != nil {
		return p, err
	}
	if p.Username, err = channel.StringParamOr(raw, "username", ""); err != nil {
		return p, err
	}
	if p.Password, err = channel.StringParamOr(raw, "password", ""); err != nil {
		return p, err
	}
	if p.HeaderName, err = channel.StringParamOr(raw, "header_name", ""); err != nil {
		return p, err
	}
	if p.HeaderValue, err = channel.StringParamOr(raw, "header_value", ""); err != nil {
		return p, err
	}
	if p.Secret, err = channel.StringParamOr(raw, "secret", ""); err != nil {
		return p, err
	}
	if p.SignatureHeader, err = channel.StringParamOr(raw, "signature_header", "X-Signature"); err != nil {
		return p, err
	}
	if p.SignaturePrefix, err = channel.StringParamOr(raw, "signature_prefix", ""); err != nil {
		return p, err
	}
	if p.SignatureBase64, err = channel.BoolParamOr(raw, "signature_base64", false); err != nil {
		return p, err
	}

	return p, nil
}

func parseOverflowMode(s string) (channel.OverflowMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "error":
		return channel.OverflowError, nil
	case "truncate":
		return channel.OverflowTruncate, nil
	case "split":
		return channel.OverflowSplit, nil
	default:
		return 0, fmt.Errorf("parameter \"overflow_mode\": %q is not one of error, truncate, split", s)
	}
}

func paramSchema() []channel.ParamSpec {
	specs := []channel.ParamSpec{
		{
			Name: "url", Type: channel.ParamString, Required: true,
			Label: "Endpoint URL", Desc: "Where the JSON payload is posted.",
		},
		{
			Name: "method", Type: channel.ParamEnum, Values: []string{"POST", "PUT"}, Default: "POST",
			Label: "HTTP method",
		},
		{
			Name: "content_type", Type: channel.ParamString, Default: "application/json; charset=utf-8",
			Label: "Content type",
		},
		{
			Name: "payload_template", Type: channel.ParamString,
			Label: "Payload template",
			Desc:  "Optional JSON body with {placeholders}. Values are JSON-escaped, so placeholders must sit inside quotes.",
		},
		{
			Name: "timeout", Type: channel.ParamDuration, Default: "10s",
			Label: "Request timeout",
		},
		{
			Name: "body_max_len", Type: channel.ParamInt, Default: 0,
			Label: "Body limit", Desc: "Characters. 0 means no limit.",
		},
		{
			Name: "title_max_len", Type: channel.ParamInt, Default: 0,
			Label: "Title limit", Desc: "Characters. 0 means no limit.",
		},
		{
			Name: "overflow_mode", Type: channel.ParamEnum,
			Values: []string{"error", "truncate", "split"}, Default: "error",
			Label: "Overflow behaviour", Desc: "What to do when the body exceeds body_max_len.",
		},
		{
			Name: "rate_per_sec", Type: channel.ParamFloat, Default: 0,
			Label: "Rate limit", Desc: "Messages per second. 0 means unlimited.",
		},
	}

	specs = append(specs, httpauth.ParamSpecs()...)
	return append(specs, httpx.ParamSpec())
}
