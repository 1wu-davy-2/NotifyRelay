package email

import (
	"strings"
	"testing"
)

// Both spellings of a mailto target must work, and that is the point of this
// test rather than a happy path through it.
//
// RFC 6068 defines `mailto:` with no authority, and net/url implements that
// literally: it reads "mailto://ops@example.com" as userinfo "ops" on host
// "example.com". A parser written from the RFC — reading u.Opaque — passes
// every case in the first group below and returns nothing at all for the
// second, which is the form every human writes and the form docs/02-scope.md
// has promised since M0.
func TestParseTarget_BothSpellingsAgree(t *testing.T) {
	for _, raw := range []string{
		"mailto:ops@example.com",
		"mailto://ops@example.com",
		"MAILTO://ops@example.com",
		"mailto://ops@example.com?via=oncall",
		"mailto:ops@example.com?via=oncall",
	} {
		t.Run(raw, func(t *testing.T) {
			got, err := parseTarget(raw)
			if err != nil {
				t.Fatalf("parseTarget(%q): %v", raw, err)
			}
			if len(got.Recipients) != 1 || got.Recipients[0] != "ops@example.com" {
				t.Fatalf("recipients = %v, want [ops@example.com]", got.Recipients)
			}
			if got.Ref != raw {
				t.Errorf("Ref = %q, want the caller's own string %q", got.Ref, raw)
			}

			wantVia := ""
			if strings.Contains(raw, "via=oncall") {
				wantVia = "oncall"
			}
			if got.Instance != wantVia {
				t.Errorf("Instance = %q, want %q", got.Instance, wantVia)
			}
		})
	}
}

func TestParseTarget_Accepts(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"one address", "mailto://a@example.com", []string{"a@example.com"}},
		{"several addresses", "mailto://a@x.test,b@y.test", []string{"a@x.test", "b@y.test"}},
		{"spaces around the commas", "mailto://a@x.test, b@y.test", []string{"a@x.test", "b@y.test"}},
		{"a display name is reduced to the address", `mailto://Ops Team <ops@example.com>`, []string{"ops@example.com"}},
		{"a display name with a comma inside quotes", `mailto://"Ops, Team" <ops@example.com>`, []string{"ops@example.com"}},
		{"a percent-escaped address", "mailto://ops%40example.com", []string{"ops@example.com"}},
		{"a local-only domain is still an address", "mailto://root@localhost", []string{"root@localhost"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTarget(tt.raw)
			if err != nil {
				t.Fatalf("parseTarget(%q): %v", tt.raw, err)
			}
			if len(got.Recipients) != len(tt.want) {
				t.Fatalf("recipients = %v, want %v", got.Recipients, tt.want)
			}
			for i := range tt.want {
				if got.Recipients[i] != tt.want[i] {
					t.Errorf("recipients[%d] = %q, want %q", i, got.Recipients[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseTarget_Rejects(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantSub string
	}{
		{"another scheme", "tel:+8613800138000", "not a mailto"},
		{"no scheme", "ops@example.com", "not a mailto"},
		{"no recipient", "mailto://", "names no recipient"},
		{"no recipient with a query", "mailto://?via=oncall", "names no recipient"},
		// An empty address group parses cleanly and yields no addresses. It has
		// to be refused here: falling through would leave a target with no
		// recipients, which reads as "the instance's configured ones" and would
		// send to the on-call mailbox a message addressed to nobody.
		{"an empty address group", "mailto://undisclosed-recipients:;", "names no recipient"},

		// net/mail owns address syntax, so these are refused by it rather than
		// by a check of ours; the assertions are deliberately loose about the
		// wording.
		{"not an address", "mailto://not-an-address", ""},
		{"two @ signs in the local part", "mailto://a@b@example.com", ""},

		// A malformed escape must be reported. url.Values from u.Query() would
		// drop it, the target would resolve to a different instance than the
		// one written, and nothing would say so.
		{"a malformed escape in the query", "mailto://a@example.com?via=%zz", "malformed query"},

		{"via with no value", "mailto://a@example.com?via=", "exactly one channel instance"},
		{"via twice", "mailto://a@example.com?via=a&via=b", "exactly one channel instance"},
		{"the RFC's other recipient spelling", "mailto://?to=a@example.com", `not in a "to" parameter`},
		{"a content parameter", "mailto://a@example.com?subject=hi", "unsupported parameter"},
		{"a body parameter", "mailto://a@example.com?body=hi", "unsupported parameter"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTarget(tt.raw)
			if err == nil {
				t.Fatalf("parseTarget(%q) = nil error, want one", tt.raw)
			}
			// An empty wantSub means any error will do.
			if tt.wantSub != "" && !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q should mention %q", err, tt.wantSub)
			}
		})
	}
}
