package sqlite

import (
	"context"
	"fmt"
	"time"

	"notifyrelay/internal/store"
)

const attemptColumns = `id, delivery_id, request_id, target, channel_type,
	attempt_no, class, detail, error, elapsed_ms, created_at`

// Attempts implements store.Audit.
func (s *Store) Attempts(ctx context.Context, deliveryID string) ([]*store.Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+attemptColumns+`
		FROM attempts WHERE delivery_id = ? ORDER BY id`, deliveryID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: attempts for %s: %w", deliveryID, err)
	}
	defer rows.Close()

	return scanAttempts(rows)
}

// RecentAttempts implements store.Audit.
func (s *Store) RecentAttempts(ctx context.Context, limit int) ([]*store.Attempt, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+attemptColumns+`
		FROM attempts ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: recent attempts: %w", err)
	}
	defer rows.Close()

	return scanAttempts(rows)
}

// PruneAttempts implements store.Audit.
//
// Attempts normally disappear with their delivery through ON DELETE CASCADE.
// This catches the ones that outlive it — a delivery pruned by an older build,
// or a row removed by hand.
func (s *Store) PruneAttempts(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM attempts WHERE created_at < ?`, toNanos(before))
	if err != nil {
		return 0, fmt.Errorf("sqlite: prune attempts: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: prune attempts: %w", err)
	}
	return int(n), nil
}

func scanAttempts(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*store.Attempt, error) {
	var out []*store.Attempt

	for rows.Next() {
		var (
			a         store.Attempt
			createdAt int64
		)
		if err := rows.Scan(
			&a.ID, &a.DeliveryID, &a.RequestID, &a.Target, &a.ChannelType,
			&a.AttemptNo, &a.Class, &a.Detail, &a.Error, &a.ElapsedMS, &createdAt,
		); err != nil {
			return nil, err
		}
		a.CreatedAt = fromNanos(createdAt)
		out = append(out, &a)
	}

	return out, rows.Err()
}
