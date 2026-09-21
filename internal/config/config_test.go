package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

const validHash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func base(extra string) string {
	return `
server:
  addr: ":8080"
auth:
  api_keys:
    - name: test
      key_hash: "` + validHash + `"
` + extra
}

func TestParse_ExpandsEnvInTypedAndFreeFormFields(t *testing.T) {
	t.Setenv("NOTIFYRELAY_TEST_PASSWORD", "s3cret")

	cfg, err := Parse([]byte(base(`
smtp_in:
  enabled: true
  addr: ":2525"
  hostname: relay.local
  auth:
    enabled: true
    username: relay
    password: !env NOTIFYRELAY_TEST_PASSWORD
channels:
  - name: oncall
    type: email
    config:
      password: !env NOTIFYRELAY_TEST_PASSWORD
`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got := cfg.SMTPIn.Auth.Password; got != "s3cret" {
		t.Errorf("smtp_in.auth.password = %q, want %q", got, "s3cret")
	}

	// The free-form channel config must expand too: that block is a
	// map[string]any, so nothing can opt in via a special field type.
	if got := cfg.Channels[0].Config["password"]; got != "s3cret" {
		t.Errorf("channels[0].config.password = %v, want %q", got, "s3cret")
	}
}

func TestParse_MissingEnvVarFailsLoudly(t *testing.T) {
	const unset = "NOTIFYRELAY_TEST_DEFINITELY_UNSET"
	os.Unsetenv(unset)

	_, err := Parse([]byte(base(`
channels:
  - name: oncall
    type: email
    config:
      password: !env ` + unset + `
`)))
	if err == nil {
		t.Fatal("expected an error when an !env variable is not set")
	}
	if !strings.Contains(err.Error(), unset) {
		t.Errorf("error should name the missing variable, got: %v", err)
	}
}

func TestParse_DefaultsAreNonZeroTimeouts(t *testing.T) {
	cfg, err := Parse([]byte(base("")))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// A zero http.Server timeout means "no timeout at all", which is the
	// failure mode this project refuses to ship. Defaults must never be zero.
	got := map[string]Duration{
		"timeouts.read":        cfg.Timeouts.Read,
		"timeouts.read_header": cfg.Timeouts.ReadHeader,
		"timeouts.write":       cfg.Timeouts.Write,
		"timeouts.idle":        cfg.Timeouts.Idle,
		"timeouts.handler":     cfg.Timeouts.Handler,
		"timeouts.deliver":     cfg.Timeouts.Deliver,
	}
	for name, d := range got {
		if d <= 0 {
			t.Errorf("%s = %v, must be greater than zero", name, d)
		}
	}
	if cfg.Timeouts.Handler <= cfg.Timeouts.Deliver {
		t.Errorf("default handler (%v) must exceed default deliver (%v)",
			cfg.Timeouts.Handler, cfg.Timeouts.Deliver)
	}
}

func TestValidate_RejectsHandlerTimeoutNotExceedingDeliver(t *testing.T) {
	_, err := Parse([]byte(base(`
timeouts:
  read: 15s
  read_header: 5s
  write: 30s
  idle: 60s
  handler: 10s
  deliver: 20s
`)))
	if err == nil {
		t.Fatal("expected validation to reject handler <= deliver")
	}
	if !strings.Contains(err.Error(), "timeouts.handler") {
		t.Errorf("error should name timeouts.handler, got: %v", err)
	}
}

func TestValidate_RejectsMalformedKeyHash(t *testing.T) {
	_, err := Parse([]byte(`
server:
  addr: ":8080"
auth:
  api_keys:
    - name: test
      key_hash: "md5:abc"
`))
	if err == nil {
		t.Fatal("expected validation to reject a non-sha256 key hash")
	}
	if !strings.Contains(err.Error(), "sha256") {
		t.Errorf("error should mention the expected format, got: %v", err)
	}
}

func TestValidate_CollectsEveryProblemAtOnce(t *testing.T) {
	_, err := Parse([]byte(`
auth:
  api_keys:
    - name: ""
      key_hash: "nope"
channels:
  - name: dup
    type: email
  - name: dup
    type: email
`))
	if err == nil {
		t.Fatal("expected validation errors")
	}

	msg := err.Error()
	// One pass should surface all of these rather than stopping at the first.
	for _, want := range []string{"server.addr", "auth.api_keys", "duplicate channel name"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q, got:\n%s", want, msg)
		}
	}
}

func TestParse_DurationsFromStrings(t *testing.T) {
	cfg, err := Parse([]byte(base(`
retry:
  max_attempts: 3
  backoff: [30s, 2m, 1h]
  max_age: 48h
`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := []time.Duration{30 * time.Second, 2 * time.Minute, time.Hour}
	if len(cfg.Retry.Backoff) != len(want) {
		t.Fatalf("backoff length = %d, want %d", len(cfg.Retry.Backoff), len(want))
	}
	for i, w := range want {
		if cfg.Retry.Backoff[i].Std() != w {
			t.Errorf("backoff[%d] = %v, want %v", i, cfg.Retry.Backoff[i], w)
		}
	}
	if cfg.Retry.MaxAge.Std() != 48*time.Hour {
		t.Errorf("max_age = %v, want 48h", cfg.Retry.MaxAge)
	}
}

func TestParse_RejectsUnknownDuration(t *testing.T) {
	_, err := Parse([]byte(base(`
retry:
  max_age: "2 fortnights"
`)))
	if err == nil {
		t.Fatal("expected an error for an unparseable duration")
	}
}
