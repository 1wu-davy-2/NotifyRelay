package recipients

import "testing"

func TestAllows(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		addr     string
		want     bool
	}{
		{"no patterns allows nothing", nil, "a@example.com", false},
		{"empty list allows nothing", []string{}, "a@example.com", false},

		{"star allows any address", []string{"*"}, "anyone@anywhere.test", true},
		{"star among others still allows any", []string{"@corp.test", "*"}, "a@b.test", true},

		{"bare domain covers that domain", []string{"@example.com"}, "user@example.com", true},
		{"bare domain covers the domain itself", []string{"@example.com"}, "example.com@example.com", true},
		{"bare domain does not cover another domain", []string{"@example.com"}, "user@other.test", false},

		// The one that a suffix check gets wrong, and the reason this test
		// exists: "user@notexample.com" ends with "example.com".
		{"bare domain is not a suffix match", []string{"@example.com"}, "user@notexample.com", false},
		{"bare domain does not cover a subdomain", []string{"@example.com"}, "user@mail.example.com", false},

		{"full address matches exactly", []string{"ops@example.com"}, "ops@example.com", true},
		{"full address does not match another local part", []string{"ops@example.com"}, "dev@example.com", false},
		{"full address does not match another domain", []string{"ops@example.com"}, "ops@other.test", false},

		// Domains are case-insensitive by definition. Local parts formally are
		// not, but no mail system anyone runs enforces that, and treating
		// User@ as distinct from user@ would deny a key the address its
		// operator plainly meant.
		{"domain case is ignored", []string{"@Example.COM"}, "user@example.com", true},
		{"address case is ignored", []string{"@example.com"}, "User@Example.Com", true},
		{"local part case is ignored", []string{"Ops@example.com"}, "ops@example.com", true},

		{"a pattern with no @ matches nothing", []string{"example.com"}, "user@example.com", false},
		{"a pattern with a trailing @ matches nothing", []string{"user@"}, "user@example.com", false},
		{"a malformed pattern does not open the list", []string{"example.com"}, "user@anywhere.test", false},

		{"an address with no @ is never allowed", []string{"*"}, "not-an-address", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Allows(tt.patterns, tt.addr); got != tt.want {
				t.Errorf("Allows(%v, %q) = %v, want %v", tt.patterns, tt.addr, got, tt.want)
			}
		})
	}
}

// A quoted local part may contain an @, so the split has to be at the last one.
// Splitting at the first reads the domain as `b"@example.com` and the allow
// list stops working for an address that looks fine.
func TestAllows_QuotedLocalPart(t *testing.T) {
	if !Allows([]string{"@example.com"}, `"a@b"@example.com`) {
		t.Error(`"a@b"@example.com should be covered by @example.com`)
	}
}

func TestValidate(t *testing.T) {
	valid := []string{"*", "@example.com", "user@example.com", "User@Example.com", `"a@b"@example.com`}
	for _, p := range valid {
		if err := Validate(p); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", p, err)
		}
	}

	// "example.com" reads like it covers that domain and would cover nothing;
	// the whitespace inside "user @c.test" is a typo that would otherwise be
	// stored as a pattern matching nobody, and the operator would learn about
	// it from a refusal they cannot explain.
	invalid := []string{
		"", "   ",
		"example.com",
		"user@",
		"@",
		"user @c.test",
	}
	for _, p := range invalid {
		if err := Validate(p); err == nil {
			t.Errorf("Validate(%q) = nil, want an error", p)
		}
	}
}
