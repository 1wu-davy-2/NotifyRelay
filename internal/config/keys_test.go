package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"notifyrelay/internal/secret"
)

// The whole point of resolution is that the first boot of a deployment that
// configured nothing still comes up. If this ever fails, `docker compose up -d`
// stops being a complete deployment.
func TestResolveKey_GeneratesAndPersistsOnFirstUse(t *testing.T) {
	dir := t.TempDir()

	first, err := ResolveKey("", dir, "secret")
	if err != nil {
		t.Fatalf("ResolveKey: %v", err)
	}
	if first.Origin != KeyGenerated {
		t.Errorf("Origin = %q, want %q on a directory with no key in it", first.Origin, KeyGenerated)
	}

	path := filepath.Join(dir, KeysDir, "secret.key")
	if first.Path != path {
		t.Errorf("Path = %q, want %q", first.Path, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the generated key was not written: %v", err)
	}

	// A key every process on the host can read is not a key, so the mode is
	// checked on the file rather than trusted to the code that wrote it.
	//
	// Only on Unix. Windows has no mode bits to check: os.Chmod there sets the
	// read-only attribute and os.Stat reports 0666 for anything writable, so
	// this assertion would fail on a correct implementation. It is not skipped
	// quietly — the deployments this protects are Linux, and the check runs
	// there.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("the key file is %04o, want 0600", perm)
		}
	}

	// The second call reads what the first wrote. This is what makes a restart
	// able to open credentials sealed before it.
	second, err := ResolveKey("", dir, "secret")
	if err != nil {
		t.Fatalf("ResolveKey (second): %v", err)
	}
	if second.Origin != KeyFromFile {
		t.Errorf("Origin = %q, want %q on the second call", second.Origin, KeyFromFile)
	}
	if second.Encoded != first.Encoded {
		t.Error("the second call produced a different key; a restart would lose every sealed credential")
	}
}

// An operator who wrote a key down meant it. Generating over the top of one
// would silently make every credential sealed with the old key unreadable.
func TestResolveKey_ConfiguredValueWins(t *testing.T) {
	dir := t.TempDir()

	// Something already on disk that must not be used.
	if _, err := ResolveKey("", dir, "secret"); err != nil {
		t.Fatalf("seeding the directory: %v", err)
	}

	_, encoded, err := secret.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	got, err := ResolveKey(encoded, dir, "secret")
	if err != nil {
		t.Fatalf("ResolveKey: %v", err)
	}
	if got.Origin != KeyFromConfig {
		t.Errorf("Origin = %q, want %q", got.Origin, KeyFromConfig)
	}
	if got.Encoded != encoded {
		t.Error("the configured key was not the one returned")
	}
}

func TestResolveKey_RejectsAMalformedConfiguredKey(t *testing.T) {
	_, err := ResolveKey("not-a-key", t.TempDir(), "secret")
	if err == nil {
		t.Fatal("a malformed key was accepted")
	}
	// The name has to be in the message: with two keys, "the key is invalid"
	// leaves the operator to work out which one.
	if !strings.Contains(err.Error(), "secret") {
		t.Errorf("the error does not name the key: %v", err)
	}
}

// Two purposes, two keys. Deriving one from the other is how a weakness in the
// weaker use becomes a weakness in both.
func TestResolveKeys_GeneratesTwoDifferentKeys(t *testing.T) {
	dir := t.TempDir()

	cfg := &Config{}
	cfg.Storage.Path = filepath.Join(dir, "notifyrelay.db")
	cfg.Admin.Enabled = true

	resolved, err := cfg.ResolveKeys()
	if err != nil {
		t.Fatalf("ResolveKeys: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("resolved %d keys, want 2 (secret and session)", len(resolved))
	}
	if cfg.SecretKey == cfg.Admin.SessionKey {
		t.Fatal("secret_key and session_key are the same value")
	}

	// And they are separate files, so restoring one without the other is
	// possible and visible.
	if resolved[0].Path == resolved[1].Path {
		t.Errorf("both keys were written to %s", resolved[0].Path)
	}
}

// The keys live beside the database so that a backup of the data directory is a
// complete, restorable backup. If they landed anywhere else, that property
// would be quietly false.
func TestResolveKeys_PutsThemInTheDataDirectory(t *testing.T) {
	dir := t.TempDir()

	cfg := &Config{}
	cfg.Storage.Path = filepath.Join(dir, "data", "notifyrelay.db")
	cfg.Admin.Enabled = true

	if _, err := cfg.ResolveKeys(); err != nil {
		t.Fatalf("ResolveKeys: %v", err)
	}

	for _, name := range []string{"secret.key", "session.key"} {
		want := filepath.Join(dir, "data", KeysDir, name)
		if _, err := os.Stat(want); err != nil {
			t.Errorf("%s was not written next to the database: %v", name, err)
		}
	}
}

// A session key for a deployment with no sessions is a credential on disk for a
// feature nobody enabled.
func TestResolveKeys_SkipsTheSessionKeyWhenAdminIsOff(t *testing.T) {
	dir := t.TempDir()

	cfg := &Config{}
	cfg.Storage.Path = filepath.Join(dir, "notifyrelay.db")
	cfg.Admin.Enabled = false

	resolved, err := cfg.ResolveKeys()
	if err != nil {
		t.Fatalf("ResolveKeys: %v", err)
	}
	if len(resolved) != 1 {
		t.Errorf("resolved %d keys with admin off, want 1", len(resolved))
	}
	if _, err := os.Stat(filepath.Join(dir, KeysDir, "session.key")); err == nil {
		t.Error("a session key was written for a deployment with the operator UI off")
	}
}

// With nowhere to keep a generated key, the honest answer is an error rather
// than a key that exists only in memory and vanishes on restart.
func TestResolveKey_RefusesWithoutADataDirectory(t *testing.T) {
	_, err := ResolveKey("", "", "secret")
	if err == nil {
		t.Fatal("a key was generated with no data directory to persist it in")
	}
}
