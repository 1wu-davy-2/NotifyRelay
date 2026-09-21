package email

import (
	"strings"
	"testing"

	"notifyrelay/internal/channel"
)

// Getting TLS wrong is the single most common way an SMTP configuration fails,
// and several similar projects document port 465 with STARTTLS — a combination
// that cannot work. Inferring the mode from the port makes that mistake
// impossible to express by accident.
func TestParseConfig_InfersTLSFromPort(t *testing.T) {
	tests := []struct {
		name string
		port int
		want TLSMode
	}{
		{name: "465 is implicit TLS", port: 465, want: TLSImplicit},
		{name: "587 is STARTTLS", port: 587, want: TLSStartTLS},
		{name: "25 is STARTTLS", port: 25, want: TLSStartTLS},
		{name: "2525 is STARTTLS", port: 2525, want: TLSStartTLS},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseConfig(map[string]any{
				"host": "smtp.example.com", "port": tt.port,
				"from": "a@b.c", "to": []any{"x@y.z"},
			})
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if cfg.TLS != tt.want {
				t.Errorf("tls = %q, want %q", cfg.TLS, tt.want)
			}
			if cfg.RequireTLS != (tt.want != TLSNone) {
				t.Errorf("require_tls = %v for mode %q", cfg.RequireTLS, tt.want)
			}
		})
	}
}

func TestParseConfig_ExplicitTLSWins(t *testing.T) {
	cfg, err := parseConfig(map[string]any{
		"host": "smtp.example.com", "port": 465, "tls": "starttls",
		"from": "a@b.c", "to": []any{"x@y.z"},
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.TLS != TLSStartTLS {
		t.Errorf("tls = %q, want starttls (an explicit setting must win)", cfg.TLS)
	}
}

func TestParseConfig_Defaults(t *testing.T) {
	cfg, err := parseConfig(map[string]any{
		"host": "smtp.example.com",
		"from": "a@b.c",
		"to":   []any{"x@y.z"},
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}

	if cfg.Port != defaultPort {
		t.Errorf("port = %d, want %d", cfg.Port, defaultPort)
	}
	if cfg.Timeout != defaultTimeout {
		t.Errorf("timeout = %v, want %v", cfg.Timeout, defaultTimeout)
	}
	if cfg.AuthType != AuthAuto {
		t.Errorf("auth_type = %q, want auto", cfg.AuthType)
	}
}

func TestParseConfig_AuthTypeParsing(t *testing.T) {
	for _, want := range []AuthType{AuthAuto, AuthPlain, AuthLogin, AuthCramMD5} {
		cfg, err := parseConfig(map[string]any{
			"host": "h", "from": "a@b.c", "to": []any{"x@y.z"},
			"auth_type": string(want),
		})
		if err != nil {
			t.Fatalf("parseConfig(%q): %v", want, err)
		}
		if cfg.AuthType != want {
			t.Errorf("auth_type = %q, want %q", cfg.AuthType, want)
		}
	}
}

func TestParseConfig_RejectsBadInput(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			"host": "smtp.example.com",
			"from": "a@b.c",
			"to":   []any{"x@y.z"},
		}
	}

	tests := []struct {
		name    string
		mutate  func(map[string]any)
		wantSub string
	}{
		{
			name:    "missing host",
			mutate:  func(m map[string]any) { delete(m, "host") },
			wantSub: "host",
		},
		{
			name:    "missing from",
			mutate:  func(m map[string]any) { delete(m, "from") },
			wantSub: "from",
		},
		{
			name:    "missing to",
			mutate:  func(m map[string]any) { delete(m, "to") },
			wantSub: "to",
		},
		{
			name:    "empty recipient list",
			mutate:  func(m map[string]any) { m["to"] = []any{} },
			wantSub: "at least one recipient",
		},
		{
			name:    "port out of range",
			mutate:  func(m map[string]any) { m["port"] = 70000 },
			wantSub: "at most 65535",
		},
		{
			name:    "unknown tls mode",
			mutate:  func(m map[string]any) { m["tls"] = "ssl-please" },
			wantSub: "unknown mode",
		},
		{
			name:    "unknown auth type",
			mutate:  func(m map[string]any) { m["auth_type"] = "magic" },
			wantSub: "unknown type",
		},
		{
			// The classic misconfiguration this project exists to refuse.
			name: "tls none but require_tls true",
			mutate: func(m map[string]any) {
				m["tls"] = "none"
				m["require_tls"] = true
			},
			wantSub: "require_tls",
		},
		{
			name:    "password without username",
			mutate:  func(m map[string]any) { m["password"] = "secret" },
			wantSub: "username",
		},
		{
			name:    "username without password",
			mutate:  func(m map[string]any) { m["username"] = "relay" },
			wantSub: "password",
		},
		{
			name:    "zero timeout",
			mutate:  func(m map[string]any) { m["timeout"] = "0s" },
			wantSub: "greater than zero",
		},
		{
			name:    "unparseable timeout",
			mutate:  func(m map[string]any) { m["timeout"] = "soon" },
			wantSub: "timeout",
		},
		{
			name:    "wrong type for port",
			mutate:  func(m map[string]any) { m["port"] = "fifty-eighty-seven" },
			wantSub: "integer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := base()
			tt.mutate(raw)

			_, err := parseConfig(raw)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q should mention %q", err, tt.wantSub)
			}
		})
	}
}

func TestParseConfig_SingleRecipientStringIsAccepted(t *testing.T) {
	cfg, err := parseConfig(map[string]any{
		"host": "h", "from": "a@b.c", "to": "ops@example.com",
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if len(cfg.To) != 1 || cfg.To[0] != "ops@example.com" {
		t.Errorf("to = %v, want [ops@example.com]", cfg.To)
	}
}

func TestParamSchemaCoversEveryAcceptedParameter(t *testing.T) {
	// Every declared parameter must be one parseConfig actually reads, and
	// vice versa. A schema that drifts from the parser would produce a
	// generated form (M5) whose fields do nothing.
	schemaNames := map[string]bool{}
	for _, p := range paramSchema() {
		schemaNames[p.Name] = true
	}

	raw := map[string]any{
		"host": "smtp.example.com", "port": 587, "tls": "starttls",
		"require_tls": true, "username": "u", "password": "p",
		"auth_type": "auto", "from": "a@b.c", "to": []any{"x@y.z"},
		"helo": "relay.local", "timeout": "5s", "ca_file": "",
		"subject_template": "{title}",
	}
	if _, err := parseConfig(raw); err != nil {
		t.Fatalf("parseConfig rejected a fully populated config: %v", err)
	}

	for name := range raw {
		if !schemaNames[name] {
			t.Errorf("parameter %q is read by parseConfig but missing from paramSchema", name)
		}
	}
	for name := range schemaNames {
		if _, ok := raw[name]; !ok {
			t.Errorf("paramSchema declares %q but parseConfig never reads it", name)
		}
	}
}

// The range a configuration is held to lives in the schema, not in parseConfig.
//
// This is the property that makes ParamSpec a single source of truth: the
// operator form, the /api/v1/channels document and the parser all read the same
// declaration. A second copy of "1 to 65535" inside parseConfig would agree
// today and drift the first time somebody edited one of them — and the drift
// would show up as a form that accepts a value the service then refuses.
func TestParseConfig_PortBoundComesFromTheSchema(t *testing.T) {
	raw := func(port int) map[string]any {
		return map[string]any{
			"host": "smtp.example.com",
			"from": "notify@example.com",
			"to":   []any{"ops@example.com"},
			"port": port,
		}
	}

	// What the schema declares, so the numbers below are not a guess.
	specs := paramSchema()
	var declared *channel.ParamSpec
	for i := range specs {
		if specs[i].Name == "port" {
			declared = &specs[i]
		}
	}
	if declared == nil || declared.Min == nil || declared.Max == nil {
		t.Fatal("the schema no longer declares bounds for port")
	}
	if *declared.Min != 1 || *declared.Max != 65535 {
		t.Fatalf("port bounds are %v..%v, want 1..65535", *declared.Min, *declared.Max)
	}

	// Out of range is refused.
	if _, err := parseConfig(raw(70000)); err == nil {
		t.Error("a port above the declared maximum was accepted")
	}
	if _, err := parseConfig(raw(0)); err == nil {
		t.Error("a port below the declared minimum was accepted")
	}

	// Widen the declaration and the same configuration becomes legal. If the
	// bound were a copy living in parseConfig, this would still fail.
	widened := paramSchema()
	for i := range widened {
		if widened[i].Name == "port" {
			hi := 99999.0
			widened[i].Max = &hi
		}
	}
	if _, err := parseConfigWith(raw(70000), widened); err != nil {
		t.Errorf("after widening the schema the port was still refused: %v", err)
	}

	// And tightening it moves the other end too.
	tightened := paramSchema()
	for i := range tightened {
		if tightened[i].Name == "port" {
			lo := 1024.0
			tightened[i].Min = &lo
		}
	}
	if _, err := parseConfigWith(raw(587), tightened); err == nil {
		t.Error("after raising the minimum, port 587 was still accepted")
	}
}
