package all

import (
	"strings"
	"testing"

	"notifyrelay/internal/channel"
)

// allowedPublic lists parameters that look like credentials but are not, with
// the reason. Entries are "type.param".
//
// It is empty, and that is the intended state: every parameter in this binary
// whose value is a credential is declared private. An entry here is a decision
// somebody has to make on purpose, which is the point — the alternative to an
// allowlist is a rule loose enough to miss the next one.
//
// Example of the shape an entry would take:
//
//	"somechannel.server_key": "a public identifier, not a secret",
var allowedPublic = map[string]string{}

// secretishName reports whether a parameter name is the kind that carries a
// credential.
//
// Substring matching rather than a fixed list of names, because the failure
// this test exists to catch is a *new* channel written by somebody who did not
// read the audit: `signing_key`, `bot_token`, `app_secret` are all names that
// would appear in a good-faith implementation and none of them are in a list
// anybody would remember to update.
func secretishName(name string) bool {
	lower := strings.ToLower(name)

	for _, marker := range []string{
		"password", "passwd", "secret", "token", "credential", "api_key", "apikey",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	// URLs are here because for most messaging platforms the endpoint *is* the
	// credential: DingTalk and Feishu put an access token in the query, Slack
	// puts the secret in the path, WeCom in the query again. A URL parameter
	// that is genuinely public is a deliberate exception, not a default.
	switch lower {
	case "url", "webhook_url", "header_value", "auth_value":
		return true
	}

	return false
}

// Declaring a parameter private is what protects its value. The router builds
// its redaction list by reading Private out of this schema, and the audit that
// motivated this test found a live credential reaching the API response and the
// log because a URL parameter had not been declared.
//
// A missed Private produces no error anywhere: the service starts, delivers,
// and quietly writes the token into every failure it reports. Nothing else in
// the test suite can see that, which is why it is checked here, over every
// registered channel at once.
func TestEveryCredentialParameterIsDeclaredPrivate(t *testing.T) {
	descriptors := channel.Descriptors()
	if len(descriptors) == 0 {
		t.Fatal("no channel types are registered; this test would pass vacuously")
	}

	checked := 0

	for _, d := range descriptors {
		for _, p := range d.ParamSchema {
			if !secretishName(p.Name) {
				continue
			}
			checked++

			if p.Private {
				continue
			}
			if reason, ok := allowedPublic[d.Type+"."+p.Name]; ok {
				t.Logf("%s.%s is public by an explicit exception: %s", d.Type, p.Name, reason)
				continue
			}

			t.Errorf("%s.%s carries a credential but is not declared Private, "+
				"so its value is not redacted from delivery outcomes; "+
				"mark it private or add an entry to allowedPublic saying why it is not a secret",
				d.Type, p.Name)
		}
	}

	// A rule that matched nothing would pass this test while protecting
	// nothing, and the day it stops matching would be silent.
	if checked < 10 {
		t.Errorf("only %d credential-shaped parameters were found across %d channels; "+
			"the name rule has probably stopped matching", checked, len(descriptors))
	}
}

// The other half of the schema contract: at most one of the auth fields is ever
// in play, and ShowIf is what says which. Dropping a ShowIf is otherwise
// invisible — the service still works, the form just starts lying.
func TestConditionalParametersDeclareTheirCondition(t *testing.T) {
	cases := []struct {
		channelType string
		param       string
		field       string
		equals      any
	}{
		{"webhook", "token", "auth_type", "bearer"},
		{"webhook", "password", "auth_type", "basic"},
		{"webhook", "header_value", "auth_type", "header"},
		{"webhook", "secret", "auth_type", "hmac"},
		{"webhook", "signature_base64", "auth_type", "hmac"},
		{"wecom", "webhook_url", "mode", "webhook"},
		{"wecom", "corp_secret", "mode", "app"},
		{"wecom", "agent_id", "mode", "app"},
	}

	for _, tc := range cases {
		t.Run(tc.channelType+"."+tc.param, func(t *testing.T) {
			d, ok := channel.Lookup(tc.channelType)
			if !ok {
				t.Fatalf("channel type %q is not registered", tc.channelType)
			}

			for _, p := range d.ParamSchema {
				if p.Name != tc.param {
					continue
				}
				if p.ShowIf == nil {
					t.Fatalf("%s.%s no longer declares a ShowIf", tc.channelType, tc.param)
				}
				if p.ShowIf.Field != tc.field {
					t.Errorf("ShowIf.Field = %q, want %q", p.ShowIf.Field, tc.field)
				}
				if p.ShowIf.Equals != tc.equals {
					t.Errorf("ShowIf.Equals = %v, want %v", p.ShowIf.Equals, tc.equals)
				}
				return
			}
			t.Fatalf("%s no longer declares a parameter %q", tc.channelType, tc.param)
		})
	}
}

// Every condition must name a parameter that exists, or the field it guards is
// hidden for every possible configuration and nothing says so.
//
// checkSchema enforces this at registration, so a violation would already have
// panicked during init — this test states the property rather than relying on
// the reader noticing why the binary will not start.
func TestEveryConditionNamesARealParameter(t *testing.T) {
	for _, d := range channel.Descriptors() {
		declared := map[string]bool{}
		for _, p := range d.ParamSchema {
			declared[p.Name] = true
		}

		for _, p := range d.ParamSchema {
			if p.ShowIf == nil {
				continue
			}
			if !declared[p.ShowIf.Field] {
				t.Errorf("%s.%s is conditional on %q, which is not a declared parameter",
					d.Type, p.Name, p.ShowIf.Field)
			}
		}
	}
}

// A bound has to be on a type that can have one, and has to make sense.
func TestDeclaredBoundsAreUsable(t *testing.T) {
	for _, d := range channel.Descriptors() {
		for _, p := range d.ParamSchema {
			if p.Min == nil && p.Max == nil {
				continue
			}
			if p.Type != channel.ParamInt && p.Type != channel.ParamFloat {
				t.Errorf("%s.%s declares bounds on a %s parameter", d.Type, p.Name, p.Type)
			}
			if p.Min != nil && p.Max != nil && *p.Min > *p.Max {
				t.Errorf("%s.%s declares min %v above max %v", d.Type, p.Name, *p.Min, *p.Max)
			}
			// A default outside its own bounds is a schema that refuses to
			// start with the value it says it would use.
			if n, ok := numeric(p.Default); ok {
				if p.Min != nil && n < *p.Min {
					t.Errorf("%s.%s defaults to %v, below its own minimum %v", d.Type, p.Name, n, *p.Min)
				}
				if p.Max != nil && n > *p.Max {
					t.Errorf("%s.%s defaults to %v, above its own maximum %v", d.Type, p.Name, n, *p.Max)
				}
			}
		}
	}
}

func numeric(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}
