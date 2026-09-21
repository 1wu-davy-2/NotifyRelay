// Package secret seals individual configuration values so that a database dump
// does not contain credentials.
//
// The threat is mundane and specific: the channel configuration lives in SQLite
// now, and SQLite is a file. It gets copied to a laptop to debug a queue
// problem, attached to a ticket, picked up by a backup job, or read by anyone
// who can open the data directory. A webhook token in that file is a webhook
// token that has leaked, and the people most likely to leak it are the ones
// doing their job correctly.
//
// Sealing is per value rather than per document. A whole-blob cipher would hide
// the SMTP host along with the SMTP password, and the host is exactly what
// somebody reading the database is usually trying to find out.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	// sealedPrefix marks a sealed value and names the scheme that produced it.
	//
	// It is stored with the value rather than tracked alongside it so that a
	// reader can tell a sealed value from a plaintext one without knowing which
	// parameter it came from. That matters for a channel type this binary does
	// not have registered: the row is still readable, and still refuses to hand
	// back a value it cannot open.
	//
	// It is also the seam for a key rotation: v2 can be introduced, and both
	// versions opened, without guessing which rows are which.
	sealedPrefix = "v1:"

	// KeyLen is the AES-256 key length.
	KeyLen = 32
)

// ErrNotSealed is returned when a value is opened that was never sealed.
//
// It is not a failure of the cipher; it means somebody wrote a plaintext
// credential into the database by hand. Accepting it would defeat the point of
// sealing, so it is refused loudly instead — with the remedy in the message,
// because the person who did it is mid-incident and needs the command.
var ErrNotSealed = errors.New("secret: value is not sealed; " +
	"write it through the admin API, or seal it with `notifyrelay --seal-value`")

// Key is an AES-256 key.
type Key [KeyLen]byte

// ParseKey reads a key from the configuration.
//
// Base64 is accepted in both the standard and URL-safe alphabets, and hex as
// well, because the three are indistinguishable at a glance and an operator who
// generated the key with `openssl rand -hex 32` should not have to convert it.
//
// The length decides, not the encoding. A 64-character hex string is also
// perfectly valid base64 — it decodes to 48 bytes — so trying the encodings in
// order and stopping at the first that parses would reject exactly the keys
// people generate with openssl. Each encoding is tried, and the first that
// yields a key of the right size wins.
func ParseKey(encoded string) (Key, error) {
	var k Key

	s := strings.TrimSpace(encoded)
	if s == "" {
		return k, errors.New("secret: no key given")
	}

	for _, decode := range []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
		hex.DecodeString,
	} {
		raw, err := decode(s)
		if err != nil || len(raw) != KeyLen {
			continue
		}
		copy(k[:], raw)
		return k, nil
	}

	return k, fmt.Errorf(
		"secret: key is not %d bytes in any of base64, base64url or hex (%d characters given)",
		KeyLen, len(s))
}

// GenerateKey returns a new random key and its base64 form.
func GenerateKey() (Key, string, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return k, "", fmt.Errorf("secret: generate key: %w", err)
	}
	return k, base64.StdEncoding.EncodeToString(k[:]), nil
}

// String redacts the key.
//
// A key type that prints itself is a key that ends up in a log line the first
// time somebody writes slog.Any("key", k). Making String() useless costs
// nothing — nothing needs to print a key — and removes the mistake.
func (k Key) String() string { return "[redacted]" }

// Cipher seals and opens individual values.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a cipher from a key.
func NewCipher(k Key) (*Cipher, error) {
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, fmt.Errorf("secret: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// IsSealed reports whether a value carries the sealed marker.
func IsSealed(value string) bool { return strings.HasPrefix(value, sealedPrefix) }

// Seal encrypts a value.
//
// An empty value is returned unchanged: there is nothing to protect, and
// sealing it would turn "this parameter is unset" into "this parameter holds
// ciphertext", which is a different thing to every reader downstream.
func (c *Cipher) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secret: nonce: %w", err)
	}

	// The nonce is prepended rather than stored beside the ciphertext: a value
	// has to be self-contained, because it is read back one field at a time
	// from a JSON document.
	sealed := c.aead.Seal(nonce, nonce, []byte(plain), nil)
	return sealedPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Open decrypts a value.
//
// A value that is not sealed is refused rather than passed through. The
// alternative — treat it as plaintext — makes the sealing optional in practice,
// because a credential written by hand would keep working and nobody would
// notice the protection had stopped applying to that row.
func (c *Cipher) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	if !IsSealed(sealed) {
		return "", ErrNotSealed
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, sealedPrefix))
	if err != nil {
		return "", fmt.Errorf("secret: value is not valid base64: %w", err)
	}

	nonceSize := c.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("secret: value is too short to be sealed")
	}

	plain, err := c.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		// GCM authentication failed. The usual cause is a key that is not the
		// one the value was sealed with, so the message says so rather than
		// leaving the operator to guess from "cipher: message authentication
		// failed".
		return "", fmt.Errorf("secret: value could not be opened with this key "+
			"(wrong key, or the value was sealed by another instance): %w", err)
	}
	return string(plain), nil
}
