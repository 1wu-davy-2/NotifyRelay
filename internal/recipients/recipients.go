// Package recipients decides whether a caller may address a given email
// recipient.
//
// It exists because a relay that takes recipients from the request is a relay
// that will send mail to anybody unless something says otherwise. A key with an
// empty list may name no recipients at all: every API key that already exists
// keeps working exactly as it did, and addressing strangers is something an
// operator grants on purpose.
//
// The rule is deliberately three forms and no pattern engine. A glob library
// would be more expressive and would also be something an operator has to debug
// during an incident.
package recipients

import (
	"errors"
	"strings"
)

// Any matches every address.
const Any = "*"

// Validate reports whether a pattern is usable.
//
// Call it where the pattern is *stored*, not where it is matched. A typo
// refused at the moment somebody can still see what they typed is a
// configuration mistake; the same typo found at the first request is an
// outage.
func Validate(pattern string) error {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return errors.New("an allowed-recipient pattern must not be empty")
	}
	if p == Any {
		return nil
	}
	// Whitespace is refused inside a pattern. A quoted local part may legally
	// contain a space, and this rule therefore refuses an address that in
	// practice no one receives mail at — which is the cheaper mistake. The
	// alternative is that "user @example.com", a typo, is stored as a pattern
	// matching nothing, and the operator finds out from a refusal they cannot
	// explain.
	if strings.ContainsAny(p, " \t") {
		return errors.New("an allowed-recipient pattern must not contain whitespace")
	}

	// A bare domain with no @ is the mistake worth naming: "example.com" reads
	// like it covers that domain and would in fact cover nothing.
	_, domain, ok := cut(p)
	if !ok {
		return errors.New(`an allowed-recipient pattern must be "*", "@domain" or "user@domain"`)
	}
	if domain == "" {
		return errors.New("an allowed-recipient pattern has no domain after the @")
	}
	return nil
}

// Allows reports whether any pattern covers the address.
func Allows(patterns []string, addr string) bool {
	local, domain, ok := cut(strings.TrimSpace(addr))
	if !ok || domain == "" {
		return false
	}

	for _, pattern := range patterns {
		p := strings.TrimSpace(pattern)
		if p == Any {
			return true
		}

		wantLocal, wantDomain, ok := cut(p)
		if !ok || wantDomain == "" {
			// A malformed pattern matches nothing rather than everything. The
			// failure mode of guessing here would be an allow list that lets
			// through what it was written to stop.
			continue
		}

		// Compared whole, never as a suffix. "@example.com" must not match
		// "attacker@notexample.com", which is exactly what a HasSuffix check
		// against the address would do.
		if !strings.EqualFold(wantDomain, domain) {
			continue
		}
		if wantLocal == "" || strings.EqualFold(wantLocal, local) {
			return true
		}
	}
	return false
}

// cut splits an address at its last @.
//
// The last one, not the first: a quoted local part may contain an @ itself
// (`"a@b"@example.com`), and splitting at the first would read its domain as
// `b"@example.com`.
func cut(s string) (local, domain string, ok bool) {
	at := strings.LastIndex(s, "@")
	if at < 0 {
		return s, "", false
	}
	return s[:at], s[at+1:], true
}
