package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"notifyrelay/internal/store"
)

func TestEnqueue_ChannelAndRecipientsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	now := time.Now().UTC()
	d := delivery("r1", "mailto://user@example.com?via=tx", now)
	d.Channel = "tx"
	d.Recipients = []string{"user@example.com", "other@example.com"}
	mustEnqueue(t, s, d)

	got, err := s.Get(ctx, "r1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("delivery is missing")
	}

	// Both halves matter and they are not the same fact: Target is what the
	// caller wrote and is echoed back; Channel is what everything operational
	// keys on.
	if got.Target != "mailto://user@example.com?via=tx" {
		t.Errorf("Target = %q, want the caller's own string", got.Target)
	}
	if got.Channel != "tx" {
		t.Errorf("Channel = %q, want \"tx\"", got.Channel)
	}
	if len(got.Recipients) != 2 || got.Recipients[0] != "user@example.com" {
		t.Errorf("Recipients = %v, want both addresses", got.Recipients)
	}
}

// A delivery with no caller-supplied addressing stores the same thing as one
// that predates the column: nothing. It must not read back as a single empty
// address, which the email channel would then try to deliver to.
func TestEnqueue_NoRecipientsReadsBackAsNone(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	now := time.Now().UTC()
	d := delivery("r2", "oncall", now)
	d.Channel = "oncall"
	mustEnqueue(t, s, d)

	got, err := s.Get(ctx, "r2")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Recipients) != 0 {
		t.Errorf("Recipients = %v, want none", got.Recipients)
	}
}

// Rows written before the column existed had their instance in `target`,
// because the queue is the only writer. The migration has to copy it across:
// left empty, every metric label and every "show me what oncall sent" query
// for those rows would answer nothing.
func TestOpen_BackfillsChannelFromTarget(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	// A deliveries table as an earlier build wrote it: no channel, no
	// recipients.
	if _, err := raw.ExecContext(ctx, `
		CREATE TABLE deliveries (
			id              TEXT    PRIMARY KEY,
			request_id      TEXT    NOT NULL,
			target          TEXT    NOT NULL,
			channel_type    TEXT    NOT NULL,
			status          TEXT    NOT NULL,
			attempts        INTEGER NOT NULL DEFAULT 0,
			next_attempt_at INTEGER NOT NULL,
			last_error      TEXT    NOT NULL DEFAULT '',
			last_class      TEXT    NOT NULL DEFAULT '',
			created_at      INTEGER NOT NULL,
			updated_at      INTEGER NOT NULL,
			claimed_at      INTEGER NOT NULL DEFAULT 0,
			sent_at         INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		t.Fatalf("create old deliveries table: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO deliveries
			(id, request_id, target, channel_type, status, next_attempt_at, created_at, updated_at)
		VALUES ('old1', 'req-old', 'oncall', 'email', 'sent', 0, 0, 0)`); err != nil {
		t.Fatalf("insert old row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on an existing database: %v", err)
	}
	defer s.Close()

	got, err := s.Get(ctx, "old1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("the pre-existing delivery is missing")
	}
	if got.Channel != "oncall" {
		t.Errorf("Channel = %q, want it backfilled from target", got.Channel)
	}
	if len(got.Recipients) != 0 {
		t.Errorf("Recipients = %v, want none", got.Recipients)
	}
}

func TestAPIKey_AllowedRecipientsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	key := &store.APIKey{
		ID: "k1", Name: "user-service", KeyHash: "sha256:abc", Enabled: true,
		CreatedAt:         time.Now().UTC(),
		AllowedRecipients: []string{"@example.com", "ops@elsewhere.test"},
	}
	if err := s.PutAPIKey(ctx, key); err != nil {
		t.Fatalf("PutAPIKey: %v", err)
	}

	got, err := s.GetAPIKey(ctx, "k1")
	if err != nil {
		t.Fatalf("GetAPIKey: %v", err)
	}
	if len(got.AllowedRecipients) != 2 || got.AllowedRecipients[0] != "@example.com" {
		t.Errorf("AllowedRecipients = %v, want both patterns", got.AllowedRecipients)
	}

	// The authentication path reads the same row, so a key that is allowed to
	// address people must be able to do so after a lookup by digest too.
	byHash, err := s.FindAPIKeyByHash(ctx, "sha256:abc")
	if err != nil {
		t.Fatalf("FindAPIKeyByHash: %v", err)
	}
	if len(byHash.AllowedRecipients) != 2 {
		t.Errorf("AllowedRecipients = %v on the authentication path", byHash.AllowedRecipients)
	}

	// Clearing the list has to persist as "none", not as "unchanged": it is the
	// difference between a key that can mail anyone and one that cannot.
	got.AllowedRecipients = nil
	if err := s.PutAPIKey(ctx, got); err != nil {
		t.Fatalf("PutAPIKey (clear): %v", err)
	}
	again, err := s.GetAPIKey(ctx, "k1")
	if err != nil {
		t.Fatalf("GetAPIKey: %v", err)
	}
	if len(again.AllowedRecipients) != 0 {
		t.Errorf("AllowedRecipients = %v after clearing, want none", again.AllowedRecipients)
	}
}
