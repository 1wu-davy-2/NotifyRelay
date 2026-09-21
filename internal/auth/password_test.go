package auth

import (
	"strings"
	"testing"
)

// cheapParams keeps the tests fast. The cost of argon2 is the whole point in
// production and pure delay in a test suite that runs on every save.
func cheapParams() Argon2Params {
	return Argon2Params{Memory: 64, Time: 1, Parallelism: 1, SaltLength: 8, KeyLength: 16}
}

func TestPasswordHash_RoundTrip(t *testing.T) {
	const password = "correct horse battery staple"

	encoded, err := HashPasswordWith(password, cheapParams())
	if err != nil {
		t.Fatalf("HashPasswordWith: %v", err)
	}

	h, err := ParsePasswordHash(encoded)
	if err != nil {
		t.Fatalf("ParsePasswordHash(%q): %v", encoded, err)
	}

	if !h.Verify(password) {
		t.Error("the correct password was rejected")
	}
	if h.Verify("correct horse battery stapl") {
		t.Error("a wrong password was accepted")
	}
	if h.Verify("") {
		t.Error("an empty password was accepted")
	}
}

// The encoded form is the one every other argon2 implementation reads, which is
// what lets an operator generate a hash with a tool they already have.
func TestHashPassword_ProducesTheStandardEncoding(t *testing.T) {
	encoded, err := HashPasswordWith("hunter2", cheapParams())
	if err != nil {
		t.Fatalf("HashPasswordWith: %v", err)
	}

	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Errorf("encoded = %q, want the standard $argon2id$ form", encoded)
	}
	if strings.Contains(encoded, "hunter2") {
		t.Errorf("the password is visible in %q", encoded)
	}
}

// Two hashes of the same password must differ, or the stored hashes reveal
// which accounts share a password.
func TestHashPassword_IsSalted(t *testing.T) {
	first, err := HashPasswordWith("hunter2", cheapParams())
	if err != nil {
		t.Fatalf("HashPasswordWith: %v", err)
	}
	second, err := HashPasswordWith("hunter2", cheapParams())
	if err != nil {
		t.Fatalf("HashPasswordWith: %v", err)
	}

	if first == second {
		t.Error("the same password produced the same hash twice")
	}
}

// The parameters travel with the hash, so raising the cost later does not
// invalidate the passwords people already have.
func TestPasswordHash_VerifiesWithItsOwnParameters(t *testing.T) {
	weak := Argon2Params{Memory: 64, Time: 1, Parallelism: 1, SaltLength: 8, KeyLength: 16}
	strong := Argon2Params{Memory: 128, Time: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32}

	old, err := HashPasswordWith("hunter2", weak)
	if err != nil {
		t.Fatalf("HashPasswordWith: %v", err)
	}

	h, err := ParsePasswordHash(old)
	if err != nil {
		t.Fatalf("ParsePasswordHash: %v", err)
	}
	if !h.Verify("hunter2") {
		t.Error("a hash made with weaker parameters no longer verifies")
	}

	// And the operator can find out that it is worth re-hashing.
	if !h.NeedsRehash(strong) {
		t.Error("a weak hash was reported as not needing a rehash")
	}

	fresh, err := HashPasswordWith("hunter2", strong)
	if err != nil {
		t.Fatalf("HashPasswordWith: %v", err)
	}
	hf, err := ParsePasswordHash(fresh)
	if err != nil {
		t.Fatalf("ParsePasswordHash: %v", err)
	}
	if hf.NeedsRehash(strong) {
		t.Error("a hash made with the current parameters was reported as needing a rehash")
	}
}

func TestParsePasswordHash_RejectsMalformedInput(t *testing.T) {
	for name, encoded := range map[string]string{
		"empty":            "",
		"plaintext":        "hunter2",
		"wrong algorithm":  "$argon2i$v=19$m=64,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2g",
		"wrong version":    "$argon2id$v=13$m=64,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2g",
		"missing fields":   "$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHQ",
		"bad salt base64":  "$argon2id$v=19$m=64,t=1,p=1$!!!!$aGFzaGhhc2g",
		"bad hash base64":  "$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHQ$!!!!",
		"no leading brace": "argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHQ$aGFzaA",
	} {
		if _, err := ParsePasswordHash(encoded); err == nil {
			t.Errorf("%s: %q was accepted", name, encoded)
		}
	}
}

// A hash that declares a gigabyte of memory is a denial of service aimed at the
// login endpoint, and it would arrive as a configuration value.
func TestParsePasswordHash_RefusesAbsurdCostParameters(t *testing.T) {
	for name, encoded := range map[string]string{
		"memory":      "$argon2id$v=19$m=99999999,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2g",
		"time":        "$argon2id$v=19$m=64,t=99999,p=1$c2FsdHNhbHQ$aGFzaGhhc2g",
		"parallelism": "$argon2id$v=19$m=64,t=1,p=99$c2FsdHNhbHQ$aGFzaGhhc2g",
	} {
		if _, err := ParsePasswordHash(encoded); err == nil {
			t.Errorf("%s: %q was accepted", name, encoded)
		}
	}
}

// A zero-value hash must never verify, whatever password it is given. It is
// what a struct field holds before it is filled in, and "the empty hash matches
// everything" is a login bypass with a one-line cause.
func TestPasswordHash_ZeroValueVerifiesNothing(t *testing.T) {
	var h PasswordHash

	if h.Verify("") {
		t.Error("the zero hash accepted an empty password")
	}
	if h.Verify("hunter2") {
		t.Error("the zero hash accepted a password")
	}
}

func TestHashPassword_RefusesAnEmptyPassword(t *testing.T) {
	if _, err := HashPasswordWith("", cheapParams()); err == nil {
		t.Error("an empty password was hashed")
	}
}
