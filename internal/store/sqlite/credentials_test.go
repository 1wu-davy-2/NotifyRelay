package sqlite

import (
	"context"
	"testing"
	"time"

	"notifyrelay/internal/store"
)

func TestAPIKeys_RoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	key := &store.APIKey{
		ID:        "k1",
		Name:      "ci-pipeline",
		KeyHash:   "sha256:aaaa",
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.PutAPIKey(ctx, key); err != nil {
		t.Fatalf("PutAPIKey: %v", err)
	}

	got, err := s.GetAPIKey(ctx, "k1")
	if err != nil {
		t.Fatalf("GetAPIKey: %v", err)
	}
	if got == nil {
		t.Fatal("the key was stored and GetAPIKey returned nil")
	}
	if got.Name != "ci-pipeline" || !got.Enabled {
		t.Errorf("got %+v, want the values that were stored", got)
	}
	if got.LastUsedAt != nil {
		t.Errorf("LastUsedAt = %v, want nil for a key that has never been used", got.LastUsedAt)
	}

	// A key that does not exist is nil, not an error: the caller distinguishes
	// "no such key" from "the store is broken", and only the second is a 500.
	missing, err := s.GetAPIKey(ctx, "nope")
	if err != nil {
		t.Fatalf("GetAPIKey for a missing id: %v", err)
	}
	if missing != nil {
		t.Errorf("GetAPIKey returned %+v for an id that does not exist", missing)
	}
}

// A disabled key must not authenticate. The filter is in SQL rather than in the
// caller, because a caller that forgot it would quietly honour a revoked
// credential — which is the one thing disabling a key is for.
func TestAPIKeys_DisabledKeysDoNotResolveByHash(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if err := s.PutAPIKey(ctx, &store.APIKey{
		ID: "k1", Name: "revoked", KeyHash: "sha256:bbbb",
		Enabled: false, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutAPIKey: %v", err)
	}

	found, err := s.FindAPIKeyByHash(ctx, "sha256:bbbb")
	if err != nil {
		t.Fatalf("FindAPIKeyByHash: %v", err)
	}
	if found != nil {
		t.Error("a disabled key resolved by hash; it would authenticate")
	}

	// Re-enabling it makes it resolve again, which is what makes disable a
	// reversible operation rather than a delete.
	key, _ := s.GetAPIKey(ctx, "k1")
	key.Enabled = true
	if err := s.PutAPIKey(ctx, key); err != nil {
		t.Fatalf("PutAPIKey: %v", err)
	}

	found, err = s.FindAPIKeyByHash(ctx, "sha256:bbbb")
	if err != nil {
		t.Fatalf("FindAPIKeyByHash: %v", err)
	}
	if found == nil {
		t.Error("a re-enabled key does not resolve")
	}
}

// Two keys must not be storable with the same digest: the column is UNIQUE, and
// a duplicate would make "which key is this request using" ambiguous in the
// audit trail.
func TestAPIKeys_DigestsAreUnique(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	base := &store.APIKey{ID: "k1", Name: "one", KeyHash: "sha256:cccc", Enabled: true, CreatedAt: time.Now().UTC()}
	if err := s.PutAPIKey(ctx, base); err != nil {
		t.Fatalf("PutAPIKey: %v", err)
	}

	dup := &store.APIKey{ID: "k2", Name: "two", KeyHash: "sha256:cccc", Enabled: true, CreatedAt: time.Now().UTC()}
	if err := s.PutAPIKey(ctx, dup); err == nil {
		t.Error("a second key with the same digest was stored")
	}
}

// TouchAPIKey is what keeps a database write off the hot path of every
// notification. The interval is the mechanism, so it is what gets tested.
func TestAPIKeys_TouchIsRateLimitedByTheInterval(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	if err := s.PutAPIKey(ctx, &store.APIKey{
		ID: "k1", Name: "ci", KeyHash: "sha256:dddd", Enabled: true, CreatedAt: now,
	}); err != nil {
		t.Fatalf("PutAPIKey: %v", err)
	}

	// The first touch always writes: there is nothing recorded yet.
	if err := s.TouchAPIKey(ctx, "k1", now, time.Hour); err != nil {
		t.Fatalf("TouchAPIKey: %v", err)
	}
	first, _ := s.GetAPIKey(ctx, "k1")
	if first.LastUsedAt == nil {
		t.Fatal("the first touch did not record a time")
	}

	// A second touch inside the interval must leave the recorded time alone.
	// This is the assertion that would fail if the WHERE clause were dropped:
	// the write would happen and the timestamp would move.
	later := now.Add(time.Minute)
	if err := s.TouchAPIKey(ctx, "k1", later, time.Hour); err != nil {
		t.Fatalf("TouchAPIKey: %v", err)
	}
	second, _ := s.GetAPIKey(ctx, "k1")
	if !second.LastUsedAt.Equal(*first.LastUsedAt) {
		t.Errorf("a touch inside the interval moved the timestamp: %v -> %v",
			first.LastUsedAt, second.LastUsedAt)
	}

	// Past the interval it writes again.
	beyond := now.Add(2 * time.Hour)
	if err := s.TouchAPIKey(ctx, "k1", beyond, time.Hour); err != nil {
		t.Fatalf("TouchAPIKey: %v", err)
	}
	third, _ := s.GetAPIKey(ctx, "k1")
	if third.LastUsedAt.Equal(*first.LastUsedAt) {
		t.Error("a touch past the interval did not update the timestamp")
	}
}

// The first-run page closes by this returning false. If it returned true twice,
// a second person could take a deployment that already has an administrator.
func TestAdminCredential_CanOnlyBeCreatedOnce(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	none, err := s.GetAdminCredential(ctx)
	if err != nil {
		t.Fatalf("GetAdminCredential: %v", err)
	}
	if none != nil {
		t.Fatalf("a fresh database reports an administrator: %+v", none)
	}

	created, err := s.CreateAdminCredential(ctx, &store.AdminCredential{
		Username: "ops", PasswordHash: "argon2id$fake", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateAdminCredential: %v", err)
	}
	if !created {
		t.Fatal("the first credential was not stored")
	}

	// The second attempt is the race: two people submitting the setup form at
	// the same moment. Exactly one may win, and the loser must be told so
	// rather than silently replacing the winner.
	again, err := s.CreateAdminCredential(ctx, &store.AdminCredential{
		Username: "attacker", PasswordHash: "argon2id$other", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateAdminCredential (second): %v", err)
	}
	if again {
		t.Error("a second credential was created; the deployment can be taken over")
	}

	stored, err := s.GetAdminCredential(ctx)
	if err != nil {
		t.Fatalf("GetAdminCredential: %v", err)
	}
	if stored.Username != "ops" {
		t.Errorf("the stored username is %q, want the first one that was written", stored.Username)
	}
}
