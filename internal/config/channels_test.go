package config

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	_ "notifyrelay/internal/channel/all"
	"notifyrelay/internal/secret"
	"notifyrelay/internal/store"
	"notifyrelay/internal/store/sqlite"
)

func testSource(t *testing.T, withKey bool) (*ChannelSource, *sqlite.Store) {
	t.Helper()

	st, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	var cipher *secret.Cipher
	if withKey {
		key, _, err := secret.GenerateKey()
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		if cipher, err = secret.NewCipher(key); err != nil {
			t.Fatalf("NewCipher: %v", err)
		}
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewChannelSource(st, cipher, log), st
}

func emailChannel(name, password string) ChannelConfig {
	return ChannelConfig{
		Name: name,
		Type: "email",
		Config: map[string]any{
			"host":     "smtp.example.com",
			"from":     "notify@example.com",
			"to":       []any{"ops@example.com"},
			"username": "notify@example.com",
			"password": password,
		},
	}
}

// The acceptance criterion for the whole feature: a database dump must not
// contain the credential, and must still be readable for everything else.
func TestSave_SealsOnlyTheParametersDeclaredPrivate(t *testing.T) {
	ctx := context.Background()
	src, st := testSource(t, true)

	const password = "hunter2-super-secret"
	if err := src.Save(ctx, emailChannel("oncall", password)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Read the row back through the store, which does not know about sealing.
	// This is what somebody with a SQLite client sees.
	rec, err := st.GetChannel(ctx, "oncall")
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if rec == nil {
		t.Fatal("the channel was not stored")
	}

	stored, _ := rec.Config["password"].(string)
	if stored == password {
		t.Error("the password was stored in the clear")
	}
	if !secret.IsSealed(stored) {
		t.Errorf("stored password = %q, want a sealed value", stored)
	}

	// Everything else stays readable. A whole-blob cipher would hide the host
	// as well, and the host is what somebody opening the database is usually
	// looking for.
	if got := rec.Config["host"]; got != "smtp.example.com" {
		t.Errorf("host = %v, want it readable in the clear", got)
	}
	if got := rec.Config["username"]; got != "notify@example.com" {
		t.Errorf("username = %v, want it readable — it is not declared private", got)
	}
}

func TestLoad_OpensSealedValues(t *testing.T) {
	ctx := context.Background()
	src, _ := testSource(t, true)

	const password = "hunter2-super-secret"
	if err := src.Save(ctx, emailChannel("oncall", password)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := src.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d channels, want 1", len(loaded))
	}
	if got := loaded[0].Config["password"]; got != password {
		t.Errorf("password = %v, want the plaintext back", got)
	}
}

// A channel with no credentials must be storable without a key, or every
// deployment would have to configure one whether it needed it or not.
//
// Note what "no credentials" means here: an email channel with no password.
// Every webhook-ish channel declares its URL private, because the URL is where
// Slack, DingTalk, Feishu and WeCom keep the token — so those do need a key,
// and that is the intended cost rather than an oversight.
func TestSave_WithoutAKeyIsFineWhenThereIsNothingToSeal(t *testing.T) {
	ctx := context.Background()
	src, _ := testSource(t, false)

	cfg := ChannelConfig{
		Name: "oncall",
		Type: "email",
		Config: map[string]any{
			"host": "smtp.example.com",
			"from": "notify@example.com",
			"to":   []any{"ops@example.com"},
		},
	}
	if err := src.Save(ctx, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// The counterpart, stated as a test so the consequence is visible rather than
// discovered during an upgrade: a webhook channel cannot be stored without a
// key, because its URL is declared private and a private value is a credential
// whether or not this particular one carries a token.
func TestSave_AWebhookNeedsAKeyBecauseItsURLIsACredential(t *testing.T) {
	ctx := context.Background()

	cfg := ChannelConfig{
		Name:   "hook",
		Type:   "webhook",
		Config: map[string]any{"url": "https://example.com/notify", "auth_type": "none"},
	}

	if err := mustSource(t, false).Save(ctx, cfg); err == nil {
		t.Error("a webhook was stored with no key configured")
	}
	if err := mustSource(t, true).Save(ctx, cfg); err != nil {
		t.Errorf("a webhook was refused even with a key configured: %v", err)
	}
}

// ...and refused the moment there is. Writing the credential in the clear
// because no key was configured is the one outcome nobody would notice.
func TestSave_WithoutAKeyRefusesACredential(t *testing.T) {
	ctx := context.Background()
	src, st := testSource(t, false)

	err := src.Save(ctx, emailChannel("oncall", "hunter2"))
	if err == nil {
		t.Fatal("a credential was stored with no key configured")
	}
	if !strings.Contains(err.Error(), "secret_key") {
		t.Errorf("error = %q, want it to name the setting", err)
	}

	n, err := st.CountChannels(ctx)
	if err != nil {
		t.Fatalf("CountChannels: %v", err)
	}
	if n != 0 {
		t.Errorf("%d channels were stored despite the failure", n)
	}
}

// A type this binary does not have cannot be checked for which of its
// parameters are credentials, so storing it would seal nothing and say nothing.
func TestSave_RejectsAnUnknownChannelType(t *testing.T) {
	ctx := context.Background()
	src, _ := testSource(t, true)

	err := src.Save(ctx, ChannelConfig{
		Name:   "mystery",
		Type:   "no-such-channel-type",
		Config: map[string]any{"password": "hunter2"},
	})
	if err == nil {
		t.Fatal("an unknown channel type was stored")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("error = %q, want it to say the type is unknown", err)
	}
}

// A value that arrives already sealed must not be sealed again. The operator UI
// shows a masked placeholder rather than the credential, and a form that posts
// the placeholder back would otherwise encrypt the ciphertext.
func TestSave_DoesNotDoubleSeal(t *testing.T) {
	ctx := context.Background()
	src, st := testSource(t, true)

	const password = "hunter2"
	if err := src.Save(ctx, emailChannel("oncall", password)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rec, err := st.GetChannel(ctx, "oncall")
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	sealedOnce, _ := rec.Config["password"].(string)

	// Save the same row back, as an edit that changed only the host.
	loaded, err := src.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	edited := loaded[0]
	edited.Config["host"] = "smtp2.example.com"
	if err := src.Save(ctx, edited); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	rec, err = st.GetChannel(ctx, "oncall")
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	sealedTwice, _ := rec.Config["password"].(string)
	if strings.Count(sealedTwice, "v1:") != 1 {
		t.Errorf("password = %q, want exactly one layer of sealing", sealedTwice)
	}

	// And the value still opens, which is the property that matters.
	loaded, err = src.Load(ctx)
	if err != nil {
		t.Fatalf("Load after edit: %v", err)
	}
	if got := loaded[0].Config["password"]; got != password {
		t.Errorf("password = %v, want %q", got, password)
	}
	_ = sealedOnce
}

// ------------------------------------------------------------------- import

func TestImportOnce_SeedsOnceAndThenStops(t *testing.T) {
	ctx := context.Background()
	src, _ := testSource(t, true)

	fromFile := []ChannelConfig{emailChannel("oncall", "hunter2")}

	n, err := src.ImportOnce(ctx, fromFile)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if n != 1 {
		t.Errorf("imported %d, want 1", n)
	}

	// A second boot must not import again: the database is authoritative, and
	// re-importing would undo every edit made through the UI.
	n, err = src.ImportOnce(ctx, fromFile)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if n != 0 {
		t.Errorf("imported %d on the second boot, want 0", n)
	}
}

// The case the "only when empty" rule exists for: an operator deletes every
// channel through the UI, and the next restart must not bring them all back.
func TestImportOnce_DoesNotResurrectDeletedChannels(t *testing.T) {
	ctx := context.Background()
	src, _ := testSource(t, true)

	fromFile := []ChannelConfig{emailChannel("oncall", "hunter2"), emailChannel("ops", "hunter3")}

	if _, err := src.ImportOnce(ctx, fromFile); err != nil {
		t.Fatalf("import: %v", err)
	}

	for _, name := range []string{"oncall", "ops"} {
		if _, err := src.Delete(ctx, name); err != nil {
			t.Fatalf("Delete %s: %v", name, err)
		}
	}

	n, err := src.ImportOnce(ctx, fromFile)
	if err != nil {
		t.Fatalf("import after deletion: %v", err)
	}
	if n != 0 {
		t.Errorf("imported %d channels that had been deleted on purpose", n)
	}

	loaded, err := src.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("%d channels came back from the file", len(loaded))
	}
}

func TestImportOnce_DoesNothingWithoutFileChannels(t *testing.T) {
	ctx := context.Background()
	src, _ := testSource(t, true)

	n, err := src.ImportOnce(ctx, nil)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 0 {
		t.Errorf("imported %d from an empty list", n)
	}
}

// An upgrade that adds the database-backed configuration must not silently drop
// a credential into a plaintext column. Failing here costs one environment
// variable and one restart; not failing costs a leaked token.
func TestImportOnce_RefusesToImportCredentialsWithoutAKey(t *testing.T) {
	ctx := context.Background()
	src, st := testSource(t, false)

	_, err := src.ImportOnce(ctx, []ChannelConfig{emailChannel("oncall", "hunter2")})
	if err == nil {
		t.Fatal("credentials were imported with no key configured")
	}
	if !strings.Contains(err.Error(), "secret_key") {
		t.Errorf("error = %q, want it to name the setting", err)
	}

	n, err := st.CountChannels(ctx)
	if err != nil {
		t.Fatalf("CountChannels: %v", err)
	}
	if n != 0 {
		t.Errorf("%d channels were imported despite the failure", n)
	}
}

// ------------------------------------------------------------------ quotas

func TestSave_RoundTripsQuota(t *testing.T) {
	ctx := context.Background()
	src, _ := testSource(t, true)

	cfg := emailChannel("oncall", "hunter2")
	cfg.Quota = QuotaConfig{PerSecond: 5, PerMinute: 100, PerHour: 1000, PerDay: 5000, PerMonth: 100000}
	cfg.Enabled = boolPtr(false)

	if err := src.Save(ctx, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := src.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d, want 1", len(loaded))
	}

	if loaded[0].Quota != cfg.Quota {
		t.Errorf("quota = %+v, want %+v", loaded[0].Quota, cfg.Quota)
	}
	if loaded[0].IsEnabled() {
		t.Error("a disabled channel came back enabled")
	}
}

func boolPtr(b bool) *bool { return &b }

// compile-time check that the store interface is satisfied by the sqlite
// implementation, so a missing method is a build failure rather than a runtime
// surprise in main.
var _ store.Channels = (*sqlite.Store)(nil)

func mustSource(t *testing.T, withKey bool) *ChannelSource {
	t.Helper()
	src, _ := testSource(t, withKey)
	return src
}
