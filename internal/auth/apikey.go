// Package auth holds credential primitives shared by the HTTP and SMTP
// inbound paths.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// HashPrefix is the algorithm prefix used in configuration files.
const HashPrefix = "sha256:"

// Key is a parsed API key entry. Only the digest is ever retained; the token
// itself is never stored, logged or written to disk.
type Key struct {
	Name    string
	Hash    []byte // raw SHA-256 digest of the token
	Enabled bool
	// AllowedRecipients lists the address patterns this key may name as a
	// recipient of a notification; see internal/recipients. Empty means it may
	// name none, and can only reach the destinations an operator configured.
	AllowedRecipients []string
}

// HashAPIKey returns the configuration representation of a token's digest.
//
// Operators use this to generate key_hash values:
//
//	notifyrelay --hash-key 'the-token'
func HashAPIKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return HashPrefix + hex.EncodeToString(sum[:])
}

// ParseHash decodes a "sha256:<hex>" string read from configuration.
func ParseHash(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("auth: key_hash must not be empty")
	}

	rest, ok := strings.CutPrefix(s, HashPrefix)
	if !ok {
		return nil, fmt.Errorf("auth: key_hash must start with %q (use --hash-key to generate one)", HashPrefix)
	}

	sum, err := hex.DecodeString(rest)
	if err != nil {
		return nil, fmt.Errorf("auth: key_hash is not valid hex: %w", err)
	}
	if len(sum) != sha256.Size {
		return nil, fmt.Errorf("auth: key_hash must be a %d-byte SHA-256 digest, got %d bytes", sha256.Size, len(sum))
	}
	return sum, nil
}

// Verify reports whether token matches any enabled key.
func Verify(keys []Key, token string) bool {
	_, ok := Identify(keys, token)
	return ok
}

// Identify returns the enabled key a token matches, and whether one did.
//
// The digest comparison is constant time, and every key is compared even after
// a match has been found, so the response time reveals neither which key
// matched nor how many keys are configured. Identify keeps that property and
// adds the caller's identity, which is what a per-key allow list needs — a
// boolean cannot say which list applies.
//
// The first match wins. Two keys cannot share a digest in practice, and if they
// did, the answer would be one of them either way.
func Identify(keys []Key, token string) (Key, bool) {
	if token == "" {
		return Key{}, false
	}
	sum := sha256.Sum256([]byte(token))

	var (
		found   Key
		matched bool
	)
	for _, k := range keys {
		// Computed unconditionally: this is the part that must not short-circuit.
		eq := subtle.ConstantTimeCompare(sum[:], k.Hash) == 1
		if eq && k.Enabled && !matched {
			found = k
			matched = true
		}
	}
	return found, matched
}
