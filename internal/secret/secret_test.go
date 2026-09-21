package secret

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func testKey(t *testing.T) Key {
	t.Helper()
	k, _, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return k
}

func testCipher(t *testing.T, k Key) *Cipher {
	t.Helper()
	c, err := NewCipher(k)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestSealOpen_RoundTrip(t *testing.T) {
	c := testCipher(t, testKey(t))

	for _, plain := range []string{"hunter2", "a", strings.Repeat("x", 4096), "含中文的密码🔐"} {
		sealed, err := c.Seal(plain)
		if err != nil {
			t.Fatalf("Seal(%q): %v", plain, err)
		}
		if !IsSealed(sealed) {
			t.Errorf("Seal(%q) = %q, which is not marked as sealed", plain, sealed)
		}

		got, err := c.Open(sealed)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if got != plain {
			t.Errorf("Open(Seal(%q)) = %q", plain, got)
		}
	}
}

// A credential must not be readable in what gets stored.
//
// The check is on the decoded bytes, and only for values long enough for the
// answer to mean something: a one-character plaintext turns up inside a
// thirty-byte ciphertext about one time in ten, so asserting its absence would
// be a test that fails at random and gets deleted rather than fixed.
func TestSeal_TheCredentialIsNotReadableInTheCiphertext(t *testing.T) {
	c := testCipher(t, testKey(t))

	for _, plain := range []string{
		"hunter2-super-secret-value",
		"https://hooks.slack.com/services/T00/B00/XXXXXXXXXXXX",
		"a-16-byte-secret",
		strings.Repeat("x", 4096),
	} {
		sealed, err := c.Seal(plain)
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}

		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, "v1:"))
		if err != nil {
			t.Fatalf("Seal produced something that is not base64: %v", err)
		}
		if bytes.Contains(raw, []byte(plain)) {
			t.Errorf("the plaintext %q is readable in the ciphertext", plain)
		}
		if strings.Contains(sealed, plain) {
			t.Errorf("the plaintext %q is readable in the encoded value", plain)
		}
	}
}

// Two seals of the same value must differ, or the database tells an attacker
// which channels share a credential.
func TestSeal_IsNotDeterministic(t *testing.T) {
	c := testCipher(t, testKey(t))

	first, err := c.Seal("hunter2")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := c.Seal("hunter2")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if first == second {
		t.Error("the same plaintext produced the same ciphertext twice")
	}
}

// An empty value is left alone. Sealing it would turn "unset" into "holds
// ciphertext", which every reader downstream would have to know about.
func TestSeal_EmptyStaysEmpty(t *testing.T) {
	c := testCipher(t, testKey(t))

	sealed, err := c.Seal("")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if sealed != "" {
		t.Errorf("Seal(\"\") = %q, want empty", sealed)
	}

	opened, err := c.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened != "" {
		t.Errorf("Open(\"\") = %q, want empty", opened)
	}
}

// A credential written into the database by hand must not quietly keep working.
// Treating an unmarked value as plaintext makes sealing optional in practice,
// and nobody would notice it had stopped applying to that row.
func TestOpen_RefusesAPlaintextValue(t *testing.T) {
	c := testCipher(t, testKey(t))

	if _, err := c.Open("hunter2"); err == nil {
		t.Fatal("a plaintext value was accepted")
	} else if !strings.Contains(err.Error(), "--seal-value") {
		t.Errorf("error = %q, want it to name the remedy", err)
	}
}

// The common operational mistake is a key that is not the one the value was
// sealed with — a rotated key, or a database copied between environments. The
// error should say so rather than leave "message authentication failed" to be
// interpreted.
func TestOpen_WithTheWrongKeySaysSo(t *testing.T) {
	sealed, err := testCipher(t, testKey(t)).Seal("hunter2")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	_, err = testCipher(t, testKey(t)).Open(sealed)
	if err == nil {
		t.Fatal("a value sealed with another key was opened")
	}
	if !strings.Contains(err.Error(), "wrong key") {
		t.Errorf("error = %q, want it to name the likely cause", err)
	}
}

func TestOpen_RejectsMalformedInput(t *testing.T) {
	c := testCipher(t, testKey(t))

	for _, bad := range []string{
		"v1:not-base64!!",
		"v1:",         // no payload at all
		"v1:c2hvcnQ=", // decodes, but shorter than a nonce
		"v2:c2hvcnQ=", // a scheme this build does not know
	} {
		if _, err := c.Open(bad); err == nil {
			t.Errorf("Open(%q) succeeded", bad)
		}
	}
}

// --------------------------------------------------------------- key parsing

func TestParseKey_AcceptsTheFormatsPeopleActuallyUse(t *testing.T) {
	k := testKey(t)
	raw := k[:]

	for name, encoded := range map[string]string{
		"base64":        base64.StdEncoding.EncodeToString(raw),
		"base64 raw":    base64.RawStdEncoding.EncodeToString(raw),
		"base64 url":    base64.URLEncoding.EncodeToString(raw),
		"hex":           hex.EncodeToString(raw),
		"padded spaces": "  " + base64.StdEncoding.EncodeToString(raw) + "\n",
	} {
		got, err := ParseKey(encoded)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != k {
			t.Errorf("%s: parsed to a different key", name)
		}
	}
}

func TestParseKey_RejectsTheWrongLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("too short"))

	if _, err := ParseKey(short); err == nil {
		t.Error("a 9-byte key was accepted")
	}
	if _, err := ParseKey(""); err == nil {
		t.Error("an empty key was accepted")
	}
}

// A key type that prints itself ends up in a log line the first time somebody
// writes slog.Any("key", k).
func TestKey_DoesNotPrintItself(t *testing.T) {
	k := testKey(t)

	if s := k.String(); strings.Contains(s, "hunter") || s == string(rune(0)) {
		t.Errorf("String() = %q", s)
	}
	if s := k.String(); s != "[redacted]" {
		t.Errorf("String() = %q, want [redacted]", s)
	}
	if got := strings.Join([]string{"key:", k.String()}, " "); strings.Contains(got, "v1:") {
		t.Errorf("the key leaked through formatting: %q", got)
	}
}
