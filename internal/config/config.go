// Package config loads, expands and validates the NotifyRelay configuration.
//
// Secrets never appear in the file: any string value may be written as
// `!env NAME` and is resolved from the environment at load time.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"notifyrelay/internal/auth"
	"notifyrelay/internal/secret"
)

// Duration is a time.Duration that unmarshals from a YAML string such as "30s".
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("expected a duration string such as \"30s\": %w", err)
	}

	s = strings.TrimSpace(s)
	if s == "" {
		*d = 0
		return nil
	}

	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// String implements fmt.Stringer.
func (d Duration) String() string { return time.Duration(d).String() }

// Config is the root configuration document.
type Config struct {
	Log      LogConfig       `yaml:"log"`
	Server   ServerConfig    `yaml:"server"`
	Timeouts TimeoutConfig   `yaml:"timeouts"`
	Auth     AuthConfig      `yaml:"auth"`
	Storage  StorageConfig   `yaml:"storage"`
	Queue    QueueConfig     `yaml:"queue"`
	Retry    RetryConfig     `yaml:"retry"`
	Breaker  BreakerConfig   `yaml:"circuit_breaker"`
	SMTPIn   SMTPInConfig    `yaml:"smtp_in"`
	Admin    AdminConfig     `yaml:"admin"`

	// SecretKey seals credential values in the channel configuration stored in
	// the database. Write it as `!env NOTIFYRELAY_SECRET_KEY`.
	//
	// It is optional, and required the moment you store a credential: a
	// deployment with no secrets needs no key, and one that has a secret and no
	// key is refused rather than written in the clear. Generate one with
	// `notifyrelay --gen-key`.
	SecretKey string `yaml:"secret_key"`

	// Channels seeds the database on first boot. See ChannelSource: the file is
	// read only while the database is empty, and the database is authoritative
	// from then on.
	Channels []ChannelConfig `yaml:"channels"`
}

// Cipher builds the value cipher from secret_key, or returns nil when no key is
// configured.
//
// Nil is a legitimate answer, not a failure: a deployment with no credentials
// to store needs no key, and the refusal happens later, at the moment a
// credential would actually be written in the clear. Failing at startup instead
// would force a key on every deployment whether or not it had a secret to keep.
func (c *Config) Cipher() (*secret.Cipher, error) {
	if strings.TrimSpace(c.SecretKey) == "" {
		return nil, nil
	}
	key, err := secret.ParseKey(c.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("secret_key: %w", err)
	}
	return secret.NewCipher(key)
}

// AdminConfig enables the operator API and UI.
type AdminConfig struct {
	Enabled bool `yaml:"enabled"`
	// Username names the single operator account.
	Username string `yaml:"username"`
	// PasswordHash is an argon2id hash, produced by `notifyrelay --hash-password`.
	// A plaintext password here is not accepted; there is no field for one.
	PasswordHash string `yaml:"password_hash"`
	// SessionKey signs session cookies. Write it as `!env NOTIFYRELAY_SESSION_KEY`.
	//
	// It must be a different key from SecretKey, and from every API key digest.
	// One key serving two purposes means a weakness in the weaker use is a
	// weakness in both — and the two uses here have nothing in common except
	// being "a secret".
	SessionKey string `yaml:"session_key"`
	// SessionTTL is how long a login lasts. Defaults to 12 hours.
	SessionTTL Duration `yaml:"session_ttl"`
}

// BreakerConfig tunes the per-channel circuit breaker.
type BreakerConfig struct {
	Enabled bool `yaml:"enabled"`
	// FailureThreshold is how many consecutive TRANSIENT or CONNECT_ERROR
	// results open the breaker. A PERMANENT rejection never counts: the
	// channel answered, so the problem is the message.
	FailureThreshold int `yaml:"failure_threshold"`
	// SuccessThreshold is how many half-open probes must succeed to close it.
	SuccessThreshold int `yaml:"success_threshold"`
	// OpenTimeout is how long to stay open before admitting probes.
	OpenTimeout Duration `yaml:"open_timeout"`
	// HalfOpenProbes is how many probes may be in flight at once.
	HalfOpenProbes int `yaml:"half_open_probes"`
}

// QuotaConfig is one channel instance's send allowance. Zero means unlimited.
//
// It lives on the instance rather than in the channel's own config block
// because it is an operational limit on this deployment's use of the channel,
// not a property of the channel type.
type QuotaConfig struct {
	PerSecond int `yaml:"per_second"`
	PerMinute int `yaml:"per_minute"`
	PerHour   int `yaml:"per_hour"`
	PerDay    int `yaml:"per_day"`
	PerMonth  int `yaml:"per_month"`
}

// StorageConfig says where deliveries are persisted.
//
// Driver is named explicitly even though only sqlite exists today: the
// configuration file is where a later MySQL or PostgreSQL deployment will
// announce itself, and the field is the seam the store package is built
// around.
type StorageConfig struct {
	Driver   string `yaml:"driver"` // sqlite
	Path     string `yaml:"path"`
	SpoolDir string `yaml:"spool_dir"`
}

// QueueConfig tunes the delivery workers.
type QueueConfig struct {
	Workers      int      `yaml:"workers"`
	Batch        int      `yaml:"batch"`
	PollEvery    Duration `yaml:"poll_every"`
	DeliverTimeout Duration `yaml:"deliver_timeout"`
	// ReleaseDelay is how long a delivery waits after being returned to the
	// queue because no channel could accept it.
	ReleaseDelay Duration `yaml:"release_delay"`
	// ClaimTimeout is how long a claim is good for. A delivery still in flight
	// after that is returned to the queue.
	//
	// This must exceed deliver_timeout, or a slow delivery is mistaken for an
	// abandoned one and sent twice.
	ClaimTimeout Duration `yaml:"claim_timeout"`
	// RecoverEvery is how often the sweep for expired claims runs.
	RecoverEvery Duration `yaml:"recover_every"`

	PruneEvery           Duration `yaml:"prune_every"`
	SentRetention        Duration `yaml:"sent_retention"`
	FailedRetention      Duration `yaml:"failed_retention"`
	IdempotencyRetention Duration `yaml:"idempotency_retention"`
}

// LogConfig controls the structured logger.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // json | text
}

// ServerConfig configures the HTTP listener.
type ServerConfig struct {
	Addr string `yaml:"addr"`
}

// TimeoutConfig holds the three layers of timeouts.
//
// The layering matters: Handler bounds a whole request including fan-out to
// every target, Deliver bounds one target's Channel.Send. Handler must exceed
// Deliver or fan-out gets cut off mid-flight — Validate enforces this.
type TimeoutConfig struct {
	Read       Duration `yaml:"read"`
	ReadHeader Duration `yaml:"read_header"`
	Write      Duration `yaml:"write"`
	Idle       Duration `yaml:"idle"`

	Handler Duration `yaml:"handler"`
	Deliver Duration `yaml:"deliver"`
}

// AuthConfig holds inbound credentials.
type AuthConfig struct {
	APIKeys []APIKeyConfig `yaml:"api_keys"`
}

// APIKeyConfig is one inbound API key. Only the digest is stored.
type APIKeyConfig struct {
	Name    string `yaml:"name"`
	KeyHash string `yaml:"key_hash"` // "sha256:<hex>", generate with --hash-key
	Enabled *bool  `yaml:"enabled"`
}

// IsEnabled reports whether the key is active. Omitted means enabled.
func (k APIKeyConfig) IsEnabled() bool { return k.Enabled == nil || *k.Enabled }

// RetryConfig bounds redelivery attempts.
type RetryConfig struct {
	MaxAttempts int        `yaml:"max_attempts"`
	Backoff     []Duration `yaml:"backoff"`
	MaxAge      Duration   `yaml:"max_age"`
}

// SMTPInConfig configures the SMTP inbound listener.
type SMTPInConfig struct {
	Enabled  bool       `yaml:"enabled"`
	Addr     string     `yaml:"addr"`
	Hostname string     `yaml:"hostname"`    // announced in the EHLO banner
	AllowedIPs []string `yaml:"allowed_ips"` // CIDRs; empty allows every source (development only)

	Auth SMTPAuthConfig `yaml:"auth"`
}

// SMTPAuthConfig configures optional SMTP AUTH on the inbound listener.
type SMTPAuthConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"` // write as `!env SMTP_IN_PASSWORD`
}

// ChannelConfig declares one channel instance.
type ChannelConfig struct {
	// Name is the operator-chosen alias used in API targets ("oncall") and in
	// SMTP recipient local parts ("oncall@relay.local").
	Name    string         `yaml:"name"`
	Type    string         `yaml:"type"` // must match a registered channel type
	Enabled *bool          `yaml:"enabled"`
	Quota   QuotaConfig    `yaml:"quota"`
	Config  map[string]any `yaml:"config"`
}

// IsEnabled reports whether the instance is active. Omitted means enabled.
func (c ChannelConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// Defaults applied when a field is omitted. They are non-zero on purpose:
// a zero http.Server timeout means "no timeout", which is the failure mode
// this project refuses to ship.
func defaultConfig() Config {
	return Config{
		Log: LogConfig{Level: "info", Format: "json"},
		Timeouts: TimeoutConfig{
			Read:       15 * Duration(time.Second),
			ReadHeader: 5 * Duration(time.Second),
			Write:      30 * Duration(time.Second),
			Idle:       60 * Duration(time.Second),
			Handler:    20 * Duration(time.Second),
			Deliver:    15 * Duration(time.Second),
		},
		Retry: RetryConfig{
			MaxAttempts: 5,
			// Fixed increasing intervals, not exponential backoff: mail failures
			// are dominated by greylisting and temporary 4xx, where the first
			// few exponential steps are too dense to be useful.
			Backoff: []Duration{
				Duration(30 * time.Second),
				Duration(2 * time.Minute),
				Duration(10 * time.Minute),
				Duration(time.Hour),
				Duration(4 * time.Hour),
			},
			MaxAge: Duration(24 * time.Hour),
		},
		SMTPIn: SMTPInConfig{
			Addr:     ":2525",
			Hostname: "relay.local",
		},
		Admin: AdminConfig{
			// Twelve hours: a working day. Long enough not to interrupt
			// somebody mid-incident, short enough that a session cookie lifted
			// from a shared machine is not useful the next morning.
			SessionTTL: Duration(12 * time.Hour),
		},
		Storage: StorageConfig{
			Driver:   "sqlite",
			Path:     "data/notifyrelay.db",
			SpoolDir: "data/spool",
		},
		Breaker: BreakerConfig{
			Enabled:          true,
			FailureThreshold: 5,
			SuccessThreshold: 2,
			OpenTimeout:      Duration(60 * time.Second),
			HalfOpenProbes:   1,
		},
		Queue: QueueConfig{
			Workers:              4,
			Batch:                16,
			PollEvery:            Duration(time.Second),
			DeliverTimeout:       Duration(30 * time.Second),
			ReleaseDelay:         Duration(30 * time.Second),
			ClaimTimeout:         Duration(5 * time.Minute),
			RecoverEvery:         Duration(100 * time.Second),
			PruneEvery:           Duration(time.Hour),
			SentRetention:        Duration(7 * 24 * time.Hour),
			FailedRetention:      Duration(30 * 24 * time.Hour),
			IdempotencyRetention: Duration(24 * time.Hour),
		},
	}
}

// Load reads, expands and validates a configuration file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return Parse(raw)
}

// Parse expands `!env` tags, decodes and validates a configuration document.
func Parse(raw []byte) (*Config, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("config: parse YAML: %w", err)
	}

	if err := expandEnv(&root); err != nil {
		return nil, err
	}

	cfg := defaultConfig()
	if root.Kind != 0 {
		if err := root.Decode(&cfg); err != nil {
			return nil, fmt.Errorf("config: decode: %w", err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate reports every structural problem at once instead of failing on the
// first one, so an operator can fix the file in a single pass.
func (c *Config) Validate() error {
	var errs []error

	if strings.TrimSpace(c.Server.Addr) == "" {
		errs = append(errs, errors.New("server.addr is required"))
	}

	timeouts := []struct {
		name string
		val  Duration
	}{
		{"timeouts.read", c.Timeouts.Read},
		{"timeouts.read_header", c.Timeouts.ReadHeader},
		{"timeouts.write", c.Timeouts.Write},
		{"timeouts.idle", c.Timeouts.Idle},
		{"timeouts.handler", c.Timeouts.Handler},
		{"timeouts.deliver", c.Timeouts.Deliver},
	}
	for _, t := range timeouts {
		if t.val <= 0 {
			errs = append(errs, fmt.Errorf("%s must be greater than zero (a zero timeout means no timeout)", t.name))
		}
	}
	if c.Timeouts.Handler > 0 && c.Timeouts.Deliver > 0 && c.Timeouts.Handler <= c.Timeouts.Deliver {
		errs = append(errs, fmt.Errorf(
			"timeouts.handler (%s) must be greater than timeouts.deliver (%s), otherwise fan-out is cut off mid-flight",
			c.Timeouts.Handler, c.Timeouts.Deliver))
	}

	if len(c.Auth.APIKeys) == 0 {
		errs = append(errs, errors.New("auth.api_keys must define at least one key"))
	}
	seenKeys := make(map[string]bool, len(c.Auth.APIKeys))
	for i, k := range c.Auth.APIKeys {
		where := fmt.Sprintf("auth.api_keys[%d]", i)
		if strings.TrimSpace(k.Name) == "" {
			errs = append(errs, fmt.Errorf("%s.name is required", where))
		} else {
			if seenKeys[k.Name] {
				errs = append(errs, fmt.Errorf("%s: duplicate key name %q", where, k.Name))
			}
			seenKeys[k.Name] = true
		}
		if _, err := auth.ParseHash(k.KeyHash); err != nil {
			errs = append(errs, fmt.Errorf("%s (%s): %w", where, k.Name, err))
		}
	}

	if c.Retry.MaxAttempts < 1 {
		errs = append(errs, errors.New("retry.max_attempts must be at least 1"))
	}
	if c.Retry.MaxAge <= 0 {
		errs = append(errs, errors.New("retry.max_age must be greater than zero"))
	}

	seenChannels := make(map[string]bool, len(c.Channels))
	for i, ch := range c.Channels {
		where := fmt.Sprintf("channels[%d]", i)
		if strings.TrimSpace(ch.Name) == "" {
			errs = append(errs, fmt.Errorf("%s.name is required", where))
		} else {
			if seenChannels[ch.Name] {
				errs = append(errs, fmt.Errorf("%s: duplicate channel name %q", where, ch.Name))
			}
			seenChannels[ch.Name] = true
		}
		if strings.TrimSpace(ch.Type) == "" {
			errs = append(errs, fmt.Errorf("%s (%s): type is required", where, ch.Name))
		}
	}

	switch c.Storage.Driver {
	case "sqlite":
		if strings.TrimSpace(c.Storage.Path) == "" {
			errs = append(errs, errors.New("storage.path is required for the sqlite driver"))
		}
	case "":
		errs = append(errs, errors.New("storage.driver is required"))
	default:
		errs = append(errs, fmt.Errorf("storage.driver %q is not supported (want sqlite)", c.Storage.Driver))
	}
	if strings.TrimSpace(c.Storage.SpoolDir) == "" {
		errs = append(errs, errors.New("storage.spool_dir is required"))
	}

	// The admin surface is the one place a mistake here turns into a remote
	// takeover of the notification system, so a half-configured admin block is
	// refused rather than started with a hole in it.
	if c.Admin.Enabled {
		if strings.TrimSpace(c.Admin.Username) == "" {
			errs = append(errs, errors.New("admin.username is required when admin is enabled"))
		}
		if strings.TrimSpace(c.Admin.PasswordHash) == "" {
			errs = append(errs, errors.New(
				"admin.password_hash is required when admin is enabled "+
					"(generate one with `notifyrelay --hash-password`)"))
		} else if _, err := auth.ParsePasswordHash(c.Admin.PasswordHash); err != nil {
			errs = append(errs, fmt.Errorf("admin.password_hash: %w", err))
		}
		if strings.TrimSpace(c.Admin.SessionKey) == "" {
			errs = append(errs, errors.New(
				"admin.session_key is required when admin is enabled "+
					"(write `session_key: !env NOTIFYRELAY_SESSION_KEY`)"))
		}
		if c.Admin.SessionTTL <= 0 {
			errs = append(errs, errors.New("admin.session_ttl must be greater than zero"))
		}

		// Two purposes, two keys. Deriving both from one value is how a
		// weakness in whichever use is weaker becomes a weakness in both, and
		// these two have nothing in common except being secret.
		if c.SecretKey != "" && c.Admin.SessionKey == c.SecretKey {
			errs = append(errs, errors.New(
				"admin.session_key must not be the same value as secret_key: "+
					"one key must not serve two purposes"))
		}
	}

	if c.SecretKey != "" {
		if _, err := secret.ParseKey(c.SecretKey); err != nil {
			errs = append(errs, fmt.Errorf("secret_key: %w", err))
		}
	}

	if c.Queue.Workers < 1 {
		errs = append(errs, errors.New("queue.workers must be at least 1"))
	}
	if c.Queue.Batch < 1 {
		errs = append(errs, errors.New("queue.batch must be at least 1"))
	}
	queueDurations := []struct {
		name string
		val  Duration
	}{
		{"queue.poll_every", c.Queue.PollEvery},
		{"queue.deliver_timeout", c.Queue.DeliverTimeout},
		{"queue.release_delay", c.Queue.ReleaseDelay},
		{"queue.claim_timeout", c.Queue.ClaimTimeout},
		{"queue.recover_every", c.Queue.RecoverEvery},
		{"queue.sent_retention", c.Queue.SentRetention},
		{"queue.failed_retention", c.Queue.FailedRetention},
		{"queue.idempotency_retention", c.Queue.IdempotencyRetention},
	}
	for _, d := range queueDurations {
		if d.val <= 0 {
			errs = append(errs, fmt.Errorf("%s must be greater than zero", d.name))
		}
	}
	if c.Queue.ClaimTimeout > 0 && c.Queue.DeliverTimeout > 0 && c.Queue.ClaimTimeout <= c.Queue.DeliverTimeout {
		errs = append(errs, fmt.Errorf(
			"queue.claim_timeout (%s) must exceed queue.deliver_timeout (%s), or a slow delivery is mistaken for an abandoned one and sent twice",
			c.Queue.ClaimTimeout, c.Queue.DeliverTimeout))
	}
	if c.Queue.RecoverEvery > 0 && c.Queue.ClaimTimeout > 0 && c.Queue.RecoverEvery > c.Queue.ClaimTimeout {
		errs = append(errs, fmt.Errorf(
			"queue.recover_every (%s) must not exceed queue.claim_timeout (%s), or an expired claim sits unnoticed for longer than the timeout it just passed",
			c.Queue.RecoverEvery, c.Queue.ClaimTimeout))
	}

	if c.SMTPIn.Enabled {
		if strings.TrimSpace(c.SMTPIn.Addr) == "" {
			errs = append(errs, errors.New("smtp_in.addr is required when smtp_in.enabled is true"))
		}
		if strings.TrimSpace(c.SMTPIn.Hostname) == "" {
			errs = append(errs, errors.New("smtp_in.hostname is required when smtp_in.enabled is true"))
		}
		if c.SMTPIn.Auth.Enabled && strings.TrimSpace(c.SMTPIn.Auth.Username) == "" {
			errs = append(errs, errors.New("smtp_in.auth.username is required when smtp_in.auth.enabled is true"))
		}
	}

	return errors.Join(errs...)
}

// Keys parses the configured API keys for the auth middleware.
func (c *Config) Keys() ([]auth.Key, error) {
	keys := make([]auth.Key, 0, len(c.Auth.APIKeys))
	for _, k := range c.Auth.APIKeys {
		sum, err := auth.ParseHash(k.KeyHash)
		if err != nil {
			return nil, fmt.Errorf("auth key %q: %w", k.Name, err)
		}
		keys = append(keys, auth.Key{Name: k.Name, Hash: sum, Enabled: k.IsEnabled()})
	}
	return keys, nil
}
