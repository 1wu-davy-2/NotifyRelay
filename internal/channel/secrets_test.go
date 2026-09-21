package channel

import (
	"strings"
	"testing"
)

func TestSecretValues_OnlyPrivateStrings(t *testing.T) {
	specs := []ParamSpec{
		{Name: "host", Type: ParamString},
		{Name: "password", Type: ParamString, Private: true},
		{Name: "token", Type: ParamString, Private: true},
		{Name: "port", Type: ParamInt, Private: true}, // not a string; not a credential
		{Name: "unset", Type: ParamString, Private: true},
	}
	cfg := map[string]any{
		"host": "smtp.example.com", "password": "hunter2", "token": "abc123",
		"port": 587, "unset": "",
	}

	got := SecretValues(specs, cfg)

	if len(got) != 2 {
		t.Fatalf("got %v, want the two private strings", got)
	}
	for _, s := range got {
		if s == "smtp.example.com" || s == "" {
			t.Errorf("%q was collected as a secret", s)
		}
	}
}

// A secret that contains another secret must be replaced whole, or the shorter
// one is consumed first and leaves a fragment of the longer on show.
func TestSecretValues_LongestFirst(t *testing.T) {
	specs := []ParamSpec{
		{Name: "a", Type: ParamString, Private: true},
		{Name: "b", Type: ParamString, Private: true},
	}
	cfg := map[string]any{"a": "abc", "b": "abcdef"}

	got := SecretValues(specs, cfg)

	if len(got) != 2 || got[0] != "abcdef" {
		t.Fatalf("got %v, want the longer secret first", got)
	}

	redacted := Redact("url?k=abcdef", got)
	if redacted != "url?k="+Redacted {
		t.Errorf("Redact = %q, want the whole secret gone", redacted)
	}
}

func TestRedact(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		secrets []string
		want    string
	}{
		{"no secrets", "nothing to hide", nil, "nothing to hide"},
		{"empty input", "", []string{"x"}, ""},
		{"single occurrence", "token=hunter2", []string{"hunter2"}, "token=" + Redacted},
		{"repeated", "hunter2 and hunter2", []string{"hunter2"}, Redacted + " and " + Redacted},
		{"absent secret leaves the text alone", "clean", []string{"hunter2"}, "clean"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Redact(tc.in, tc.secrets); got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A short secret redacts a lot of unrelated text. That is the correct
// direction: an unreadable log line is a nuisance, a leaked credential is not.
func TestRedact_ShortSecretOverRedactsRatherThanLeaks(t *testing.T) {
	// "z" rather than "a": the replacement marker itself contains an "a", and a
	// test that cannot tell the secret from the notice is no test at all.
	got := Redact("xzxz", []string{"z"})

	if strings.Contains(got, "z") {
		t.Errorf("Redact = %q, want every occurrence of the secret gone", got)
	}
	if got != "x"+Redacted+"x"+Redacted {
		t.Errorf("Redact = %q, want only the secret replaced", got)
	}
}
