package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"notifyrelay/internal/store"
)

// ---------------------------------------------------------------- idempotency

// GetRecord implements store.Idempotency.
func (s *Store) GetRecord(ctx context.Context, key string) (*store.Record, error) {
	if key == "" {
		return nil, nil
	}

	var (
		rec       store.Record
		createdAt int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT key, request_id, status, body, created_at
		FROM idempotency WHERE key = ?`, key,
	).Scan(&rec.Key, &rec.RequestID, &rec.Status, &rec.Body, &createdAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: idempotency get: %w", err)
	}

	rec.CreatedAt = fromNanos(createdAt)
	return &rec, nil
}

// PutRecord implements store.Idempotency.
//
// A conflicting key is ignored rather than overwritten: two callers raced,
// and the first response is the one the other caller will have been given.
// Replacing it would mean two callers holding different answers for the same
// key.
func (s *Store) PutRecord(ctx context.Context, rec *store.Record) error {
	if rec == nil || rec.Key == "" {
		return nil
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO idempotency (key, request_id, status, body, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (key) DO NOTHING`,
		rec.Key, rec.RequestID, rec.Status, rec.Body, toNanos(rec.CreatedAt))
	if err != nil {
		return fmt.Errorf("sqlite: idempotency put: %w", err)
	}
	return nil
}

// PruneRecords implements store.Idempotency.
func (s *Store) PruneRecords(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM idempotency WHERE created_at < ?`, toNanos(before))
	if err != nil {
		return 0, fmt.Errorf("sqlite: prune idempotency: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: prune idempotency: %w", err)
	}
	return int(n), nil
}

// ------------------------------------------------------------------- breakers

// LoadBreaker implements store.Breakers. A channel with no stored state
// returns nil, which callers read as "closed".
func (s *Store) LoadBreaker(ctx context.Context, channel string) (*store.BreakerState, error) {
	var (
		st        store.BreakerState
		openedAt  int64
		updatedAt int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT channel, state, failures, opened_at, probes, updated_at
		FROM breakers WHERE channel = ?`, channel,
	).Scan(&st.Channel, &st.State, &st.Failures, &openedAt, &st.Probes, &updatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: breaker get: %w", err)
	}

	st.OpenedAt = fromNanos(openedAt)
	st.UpdatedAt = fromNanos(updatedAt)
	return &st, nil
}

// SaveBreaker implements store.Breakers.
func (s *Store) SaveBreaker(ctx context.Context, st *store.BreakerState) error {
	if st == nil || st.Channel == "" {
		return nil
	}
	if st.UpdatedAt.IsZero() {
		st.UpdatedAt = time.Now().UTC()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO breakers (channel, state, failures, opened_at, probes, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (channel) DO UPDATE SET
			state = excluded.state,
			failures = excluded.failures,
			opened_at = excluded.opened_at,
			probes = excluded.probes,
			updated_at = excluded.updated_at`,
		st.Channel, st.State, st.Failures, toNanos(st.OpenedAt), st.Probes, toNanos(st.UpdatedAt))
	if err != nil {
		return fmt.Errorf("sqlite: breaker save: %w", err)
	}
	return nil
}
