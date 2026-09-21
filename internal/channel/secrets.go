package channel

import (
	"sort"
	"strings"
)

// Redacted is what a secret becomes on its way out of the process.
const Redacted = "[redacted]"

// SecretValues returns the configured values of every parameter the schema
// declares private.
//
// This is what makes ParamSpec.Private mean something outside the two places
// that already honour it. /api/v1/channels masks a private default and the
// channel implementations are careful not to log one, but an error string is
// assembled somewhere else entirely: a transport failure carries the URL it
// failed to reach, and for DingTalk, Feishu, Slack and WeCom the credential is
// *in* that URL. Redacting on the way out of the router covers every channel,
// including one written by somebody who never read this comment.
//
// Deriving the list from the same declaration that documents the parameter is
// the point: a new private parameter is protected by the act of declaring it
// private, with nothing else to remember.
func SecretValues(specs []ParamSpec, cfg map[string]any) []string {
	if len(specs) == 0 || len(cfg) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(specs))
	out := make([]string, 0, len(specs))

	for _, s := range specs {
		if !s.Private {
			continue
		}
		// Only strings. A private integer is not a credential, and there is no
		// meaningful way to redact a number that appears throughout a log line.
		v, ok := cfg[s.Name].(string)
		if !ok || v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}

	// Longest first, so a secret that contains another one is replaced whole
	// rather than left with a fragment of itself showing.
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// Redact replaces every occurrence of a secret in s.
//
// There is deliberately no minimum length. A one-character secret will redact
// every occurrence of that character and make the message unreadable, which is
// the correct direction to fail: an unusable log line, never a leaked
// credential.
func Redact(s string, secrets []string) string {
	if s == "" || len(secrets) == 0 {
		return s
	}
	for _, secret := range secrets {
		s = strings.ReplaceAll(s, secret, Redacted)
	}
	return s
}
