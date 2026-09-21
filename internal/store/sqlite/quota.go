package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TryConsumeCounter implements store.Quotas.
//
// The read, the comparison and the increment happen inside one immediate
// transaction. Checking and then incrementing in separate statements is the
// shape of an allowance being overspent: two workers both read a count one
// below the limit, both decide there is room, and the platform sees the
// overage.
func (s *Store) TryConsumeCounter(ctx context.Context, channel, period string, limit int, now time.Time) (bool, error) {
	if period == "" {
		return true, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("sqlite: quota consume: %w", err)
	}
	defer tx.Rollback()

	var count int
	err = tx.QueryRowContext(ctx, `
		SELECT count FROM quota_counters WHERE channel = ? AND period = ?`,
		channel, period,
	).Scan(&count)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("sqlite: quota consume: %w", err)
	}

	if limit > 0 && count >= limit {
		return false, tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO quota_counters (channel, period, count, updated_at)
		VALUES (?, ?, 1, ?)
		ON CONFLICT (channel, period) DO UPDATE SET
			count = count + 1,
			updated_at = excluded.updated_at`,
		channel, period, toNanos(now),
	); err != nil {
		return false, fmt.Errorf("sqlite: quota consume: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("sqlite: quota consume: %w", err)
	}
	return true, nil
}

// ReleaseCounter implements store.Quotas.
//
// The count is decremented, never clipped at zero: a release without a
// matching reservation is a bug, and silently absorbing it would hide the bug
// while handing back an allowance that was never spent.
func (s *Store) ReleaseCounter(ctx context.Context, channel, period string) error {
	if period == "" {
		return nil
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE quota_counters SET count = count - 1
		WHERE channel = ? AND period = ? AND count > 0`,
		channel, period,
	); err != nil {
		return fmt.Errorf("sqlite: quota release: %w", err)
	}
	return nil
}

// Counter implements store.Quotas.
func (s *Store) Counter(ctx context.Context, channel, period string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT count FROM quota_counters WHERE channel = ? AND period = ?`,
		channel, period,
	).Scan(&count)

	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("sqlite: quota read: %w", err)
	}
	return count, nil
}

// PruneCounters implements store.Quotas.
func (s *Store) PruneCounters(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM quota_counters WHERE updated_at < ?`, toNanos(before))
	if err != nil {
		return 0, fmt.Errorf("sqlite: prune quotas: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: prune quotas: %w", err)
	}
	return int(n), nil
}
