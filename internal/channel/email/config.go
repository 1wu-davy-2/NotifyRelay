package email

import (
	"fmt"
	"strings"
	"time"

	"notifyrelay/internal/channel"
)

// TLSMode selects how the SMTP connection is secured.
type TLSMode string

const (
	// TLSImplicit wraps the connection in TLS from the first byte (port 465).
	TLSImplicit TLSMode = "implicit"
	// TLSStartTLS connects in the clear, then upgrades via STARTTLS (port 587).
	TLSStartTLS TLSMode = "starttls"
	// TLSNone sends in the clear. Only sane towards a localhost relay.
	TLSNone TLSMode = "none"
)

// AuthType selects the SMTP authentication mechanism.
type AuthType string

const (
	AuthAuto    AuthType = "auto"
	AuthPlain   AuthType = "plain"
	AuthLogin   AuthType = "login"
	AuthCramMD5 AuthType = "cram-md5"
)

const (
	defaultPort    = 587
	defaultTimeout = 10 * time.Second
)

// Config is the parsed configuration of one email channel instance.
type Config struct {
	Host       string
	Port       int
	TLS        TLSMode
	RequireTLS bool
	Username   string
	Password   string
	AuthType   AuthType
	From       string
	To         []string
	Helo       string
	Timeout    time.Duration
	// SubjectTemplate is an optional placeholder template for the subject line.
	// Empty means the title, prefixed with a severity marker.
	SubjectTemplate string
	// CAFile is an optional PEM bundle of extra trusted roots, for a relay
	// using a private CA. There is deliberately no "skip verification"
	// option: silently accepting any certificate is how a relay ends up
	// handing credentials to whatever answers on the port.
	CAFile string
}

func parseConfig(raw map[string]any) (Config, error) {
	var cfg Config
	var err error

	if cfg.Host, err = channel.StringParam(raw, "host"); err != nil {
		return cfg, err
	}

	if cfg.Port, err = channel.IntParamOr(raw, "port", defaultPort); err != nil {
		return cfg, err
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return cfg, fmt.Errorf("parameter \"port\": %d is out of range 1-65535", cfg.Port)
	}

	if cfg.From, err = channel.StringParam(raw, "from"); err != nil {
		return cfg, err
	}

	if cfg.To, err = channel.StringSliceParam(raw, "to"); err != nil {
		return cfg, err
	}
	if len(cfg.To) == 0 {
		return cfg, fmt.Errorf("parameter \"to\" must list at least one recipient")
	}

	if err := cfg.parseTLS(raw); err != nil {
		return cfg, err
	}

	if cfg.Username, err = channel.StringParamOr(raw, "username", ""); err != nil {
		return cfg, err
	}
	if cfg.Password, err = channel.StringParamOr(raw, "password", ""); err != nil {
		return cfg, err
	}
	if cfg.Username == "" && cfg.Password != "" {
		return cfg, fmt.Errorf("parameter \"password\" is set but \"username\" is not")
	}
	if cfg.Username != "" && cfg.Password == "" {
		return cfg, fmt.Errorf("parameter \"username\" is set but \"password\" is not (write `password: !env SMTP_PASSWORD`)")
	}

	if err := cfg.parseAuthType(raw); err != nil {
		return cfg, err
	}

	if cfg.Helo, err = channel.StringParamOr(raw, "helo", ""); err != nil {
		return cfg, err
	}

	if cfg.SubjectTemplate, err = channel.StringParamOr(raw, "subject_template", ""); err != nil {
		return cfg, err
	}

	if cfg.CAFile, err = channel.StringParamOr(raw, "ca_file", ""); err != nil {
		return cfg, err
	}

	if cfg.Timeout, err = channel.DurationParamOr(raw, "timeout", defaultTimeout); err != nil {
		return cfg, err
	}
	if cfg.Timeout <= 0 {
		return cfg, fmt.Errorf("parameter \"timeout\" must be greater than zero")
	}

	return cfg, nil
}

// parseTLS resolves the encryption mode.
//
// When "tls" is omitted the mode is inferred from the port: 465 means implicit
// TLS, anything else means STARTTLS. The docs of several similar projects tell
// operators to use port 465 with STARTTLS, which cannot work; inferring from
// the port makes that mistake impossible to express by accident.
func (c *Config) parseTLS(raw map[string]any) error {
	value, err := channel.StringParamOr(raw, "tls", "")
	if err != nil {
		return err
	}

	if value == "" {
		if c.Port == 465 {
			c.TLS = TLSImplicit
		} else {
			c.TLS = TLSStartTLS
		}
	} else {
		switch TLSMode(strings.ToLower(strings.TrimSpace(value))) {
		case TLSImplicit, TLSStartTLS, TLSNone:
			c.TLS = TLSMode(strings.ToLower(strings.TrimSpace(value)))
		default:
			return fmt.Errorf("parameter \"tls\": unknown mode %q (want implicit, starttls or none)", value)
		}
	}

	// Requiring TLS is the sensible default everywhere except an explicit
	// plaintext mode.
	c.RequireTLS, err = channel.BoolParamOr(raw, "require_tls", c.TLS != TLSNone)
	if err != nil {
		return err
	}
	if c.TLS == TLSNone && c.RequireTLS {
		return fmt.Errorf("\"require_tls\" cannot be true when \"tls\" is \"none\"")
	}
	return nil
}

func (c *Config) parseAuthType(raw map[string]any) error {
	value, err := channel.StringParamOr(raw, "auth_type", string(AuthAuto))
	if err != nil {
		return err
	}
	switch AuthType(strings.ToLower(strings.TrimSpace(value))) {
	case AuthAuto, AuthPlain, AuthLogin, AuthCramMD5:
		c.AuthType = AuthType(strings.ToLower(strings.TrimSpace(value)))
	default:
		return fmt.Errorf("parameter \"auth_type\": unknown type %q (want auto, plain, login or cram-md5)", value)
	}
	return nil
}

// paramSchema is the single declaration of this channel's configuration.
//
// It is the source of truth for the operator form (M5) and the /channels
// documentation endpoint (M2); keep it in step with parseConfig.
func paramSchema() []channel.ParamSpec {
	return []channel.ParamSpec{
		{
			Name: "host", Type: channel.ParamString, Required: true,
			Label: "SMTP server", Desc: "Upstream relay hostname.",
		},
		{
			Name: "port", Type: channel.ParamInt, Default: defaultPort,
			Label: "Port", Desc: "Defaults to 587.",
		},
		{
			Name: "tls", Type: channel.ParamEnum, Values: []string{"implicit", "starttls", "none"},
			Label: "Encryption",
			Desc:  "Omit to infer from the port: 465 implicit TLS, anything else STARTTLS.",
		},
		{
			Name: "require_tls", Type: channel.ParamBool,
			Label: "Require TLS", Desc: "Defaults to true unless tls is none.",
		},
		{
			Name: "username", Type: channel.ParamString, Label: "Username",
		},
		{
			Name: "password", Type: channel.ParamString, Private: true,
			Label: "Password", Desc: "Write as `!env SMTP_PASSWORD`; never inline.",
		},
		{
			Name: "auth_type", Type: channel.ParamEnum,
			Values: []string{"auto", "plain", "login", "cram-md5"}, Default: "auto",
			Label: "Authentication", Desc: "auto lets the server advertise its mechanisms.",
		},
		{
			Name: "from", Type: channel.ParamString, Required: true,
			Label: "From", Desc: "Envelope and header sender.",
		},
		{
			Name: "to", Type: channel.ParamStringList, Required: true,
			Label: "Recipients",
			Desc:  "One or more addresses. Each recipient is delivered independently.",
		},
		{
			Name: "helo", Type: channel.ParamString,
			Label: "EHLO name", Desc: "Omit to let the client derive it.",
		},
		{
			Name: "timeout", Type: channel.ParamDuration, Default: "10s",
			Label: "Delivery timeout", Desc: "Bounds one recipient's SMTP session.",
		},
		{
			Name: "ca_file", Type: channel.ParamString,
			Label: "CA bundle",
			Desc:  "PEM file of extra trusted roots, for a relay using a private CA.",
		},
		{
			Name: "subject_template", Type: channel.ParamString,
			Label: "Subject template",
			Desc:  "Placeholder template such as \"[{type}] {title}\". Defaults to the title with a severity prefix.",
		},
	}
}
