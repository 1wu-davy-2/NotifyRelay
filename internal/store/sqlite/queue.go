package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"notifyrelay/internal/store"
)

const deliveryColumns = `id, request_id, target, channel_type, status, attempts,
	next_attempt_at, last_error, last_class, created_at, updated_at, claimed_at, sent_at`

// Enqueue implements store.Queue.
func (s *Store) Enqueue(ctx context.Context, items []*store.Delivery) error {
	if len(items) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: enqueue: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO deliveries
			(id, request_id, target, channel_type, status, attempts,
			 next_attempt_at, last_error, last_class, created_at, updated_at, claimed_at, sent_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`)
	if err != nil {
		return fmt.Errorf("sqlite: enqueue: prepare: %w", err)
	}
	defer stmt.Close()

	for _, d := range items {
		next := d.NextAttemptAt
		if next.IsZero() {
			next = d.CreatedAt
		}
		if _, err := stmt.ExecContext(ctx,
			d.ID, d.RequestID, d.Target, d.ChannelType, string(d.Status), d.Attempts,
			toNanos(next), d.LastError, d.LastClass,
			toNanos(d.CreatedAt), toNanos(d.UpdatedAt), toNanos(timeOrZero(d.SentAt)),
		); err != nil {
			return fmt.Errorf("sqlite: enqueue %s: %w", d.ID, err)
		}
	}

	return tx.Commit()
}

// Claim implements store.Queue.
//
// The read and the status change happen inside one immediate transaction, so
// a delivery cannot be handed to two workers. Selecting the rows and then
// updating them in separate transactions is the shape of a duplicate
// notification: two claimers read the same row, both believe it is theirs.
func (s *Store) Claim(ctx context.Context, limit int, now time.Time) ([]*store.Delivery, error) {
	if limit <= 0 {
		return nil, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlite: claim: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT `+deliveryColumns+`
		FROM deliveries
		WHERE status = ? AND next_attempt_at <= ?
		ORDER BY next_attempt_at, created_at
		LIMIT ?`, string(store.StatusQueued), toNanos(now), limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: claim: select: %w", err)
	}

	var claimed []*store.Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		claimed = append(claimed, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("sqlite: claim: %w", err)
	}
	rows.Close()

	if len(claimed) == 0 {
		return nil, tx.Commit()
	}

	ids := make([]any, 0, len(claimed)+2)
	for _, d := range claimed {
		ids = append(ids, d.ID)
	}

	query := `UPDATE deliveries SET status = ?, updated_at = ?, claimed_at = ? WHERE id IN (?` +
		strings.Repeat(", ?", len(claimed)-1) + `)`
	args := append([]any{string(store.StatusSending), toNanos(now), toNanos(now)}, ids...)

	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return nil, fmt.Errorf("sqlite: claim: mark: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlite: claim: commit: %w", err)
	}

	for _, d := range claimed {
		d.Status = store.StatusSending
		d.UpdatedAt = now
		d.ClaimedAt = now
	}
	return claimed, nil
}

// Complete implements store.Queue.
func (s *Store) Complete(ctx context.Context, id string, attempt *store.Attempt, at time.Time) error {
	if attempt != nil && attempt.CreatedAt.IsZero() {
		attempt.CreatedAt = at
	}
	return s.finish(ctx, id, store.StatusSent, attempt, "", at, at, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE deliveries SET sent_at = ? WHERE id = ?`, toNanos(at), id)
		return err
	})
}

// Reschedule implements store.Queue.
func (s *Store) Reschedule(ctx context.Context, id string, attempt *store.Attempt, next time.Time) error {
	return s.finish(ctx, id, store.StatusQueued, attempt, "", next, time.Time{}, nil)
}

// DeadLetter implements store.Queue.
// The zero next time means "never again"; finish leaves it at the current
// instant, which keeps the due-rows index well ordered.
func (s *Store) DeadLetter(ctx context.Context, id string, attempt *store.Attempt, reason string) error {
	return s.finish(ctx, id, store.StatusFailed, attempt, reason, time.Time{}, time.Time{}, nil)
}

// finish applies a terminal or retry transition together with its attempt
// record, in one transaction.
//
// The attempt row and the state change belong together: an audit trail that
// can disagree with the delivery's own status is worse than no audit trail,
// because it is believed.
func (s *Store) finish(
	ctx context.Context,
	id string,
	status store.Status,
	attempt *store.Attempt,
	lastError string,
	next time.Time,
	at time.Time,
	extra func(*sql.Tx) error,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: finish %s: %w", id, err)
	}
	defer tx.Rollback()

	now := at
	if now.IsZero() {
		now = s.clock()
	}

	// The reason a delivery is being retried is whatever the attempt says
	// went wrong; making every caller repeat it invites the two from drifting
	// apart.
	if lastError == "" && attempt != nil {
		lastError = attempt.Error
	}

	// An attempt with a zero time is the caller saying "do not record this".
	if attempt != nil {
		if attempt.CreatedAt.IsZero() {
			attempt.CreatedAt = now
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO attempts
				(delivery_id, request_id, target, channel_type, attempt_no,
				 class, detail, error, skip_reason, elapsed_ms, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			attempt.DeliveryID, attempt.RequestID, attempt.Target, attempt.ChannelType,
			attempt.AttemptNo, attempt.Class, attempt.Detail, attempt.Error,
			attempt.SkipReason, attempt.ElapsedMS, toNanos(attempt.CreatedAt),
		); err != nil {
			return fmt.Errorf("sqlite: finish %s: record attempt: %w", id, err)
		}
	}

	if next.IsZero() {
		next = now
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE deliveries
		SET status = ?, attempts = attempts + 1,
		    next_attempt_at = ?, last_error = ?, last_class = ?, updated_at = ?
		WHERE id = ?`,
		string(status), toNanos(next), lastError, attemptClass(attempt), toNanos(now), id,
	); err != nil {
		return fmt.Errorf("sqlite: finish %s: update: %w", id, err)
	}

	if extra != nil {
		if err := extra(tx); err != nil {
			return fmt.Errorf("sqlite: finish %s: %w", id, err)
		}
	}

	return tx.Commit()
}

func attemptClass(a *store.Attempt) string {
	if a == nil {
		return ""
	}
	return a.Class
}

// Release implements store.Queue.
//
// The delivery goes back to the queue and the attempt is recorded for the
// audit trail, but the attempt counter is left alone: nothing was delivered
// and nothing was refused, so charging the message for it would let a
// downstream outage exhaust every retry budget at once.
func (s *Store) Release(ctx context.Context, id string, reason, skipReason string, next time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("sqlite: release %s: %w", id, err)
	}
	defer tx.Rollback()

	now := s.clock()

	// Only a claimed delivery can be released. A worker that finished the
	// job and then crashed before recording it would otherwise have its
	// result overwritten by a late release.
	res, err := tx.ExecContext(ctx, `
		UPDATE deliveries
		SET status = ?, next_attempt_at = ?, last_error = ?, last_class = ?, updated_at = ?
		WHERE id = ? AND status = ?`,
		string(store.StatusQueued), toNanos(next), reason, string(classReleased), toNanos(now),
		id, string(store.StatusSending))
	if err != nil {
		return false, fmt.Errorf("sqlite: release %s: update: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite: release %s: %w", id, err)
	}
	if affected == 0 {
		return false, tx.Commit()
	}

	// Recorded for the audit trail, but the attempt counter is untouched.
	//
	// skip_reason is what separates the two kinds of release. "No channel
	// capacity" covers both a channel that is down and an allowance that is
	// spent, and those call for different actions from whoever is reading the
	// queue: chase the endpoint, or wait for the window to roll over.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO attempts
			(delivery_id, request_id, target, channel_type, attempt_no,
			 class, detail, error, skip_reason, elapsed_ms, created_at)
		SELECT id, request_id, target, channel_type, attempts, ?, ?, ?, ?, 0, ?
		FROM deliveries WHERE id = ?`,
		string(classReleased), reason, reason, skipReason, toNanos(now), id,
	); err != nil {
		return false, fmt.Errorf("sqlite: release %s: record: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("sqlite: release %s: commit: %w", id, err)
	}
	return true, nil
}

// classReleased marks an attempt that neither delivered nor consumed budget.
const classReleased = "RELEASED"

// RecoverOrphans implements store.Queue.
//
// The test is against claimed_at, not updated_at: a row can be touched for any
// number of reasons while it is in flight, and none of them mean the claim was
// renewed. Comparing against the claim itself is what makes "this has been in
// flight too long" a question with one answer.
func (s *Store) RecoverOrphans(ctx context.Context, claimTimeout time.Duration, now time.Time) (int, error) {
	now = now.UTC()
	deadline := now.Add(-claimTimeout)

	res, err := s.db.ExecContext(ctx, `
		UPDATE deliveries
		SET status = ?, updated_at = ?
		WHERE status = ? AND claimed_at > 0 AND claimed_at < ?`,
		string(store.StatusQueued), toNanos(now),
		string(store.StatusSending), toNanos(deadline),
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: recover orphans: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: recover orphans: %w", err)
	}
	return int(n), nil
}

// Get implements store.Queue.
func (s *Store) Get(ctx context.Context, id string) (*store.Delivery, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+deliveryColumns+` FROM deliveries WHERE id = ?`, id)

	d, err := scanDelivery(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get %s: %w", id, err)
	}
	return d, nil
}

// List implements store.Queue.
func (s *Store) List(ctx context.Context, f store.Filter) ([]*store.Delivery, error) {
	query := `SELECT ` + deliveryColumns + ` FROM deliveries`
	var where []string
	var args []any

	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.Target != "" {
		where = append(where, "target = ?")
		args = append(args, f.Target)
	}
	if f.RequestID != "" {
		where = append(where, "request_id = ?")
		args = append(args, f.RequestID)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC"

	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query += " LIMIT ? OFFSET ?"
	args = append(args, limit, f.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list: %w", err)
	}
	defer rows.Close()

	var out []*store.Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Prune implements store.Queue. Attempts are removed with their delivery by
// ON DELETE CASCADE.
func (s *Store) Prune(ctx context.Context, sentBefore, failedBefore time.Time) (int, error) {
	var removed int

	for _, c := range []struct {
		status store.Status
		before time.Time
	}{
		{store.StatusSent, sentBefore},
		{store.StatusFailed, failedBefore},
	} {
		res, err := s.db.ExecContext(ctx, `
			DELETE FROM deliveries WHERE status = ? AND updated_at < ?`,
			string(c.status), toNanos(c.before))
		if err != nil {
			return removed, fmt.Errorf("sqlite: prune %s: %w", c.status, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return removed, fmt.Errorf("sqlite: prune %s: %w", c.status, err)
		}
		removed += int(n)
	}

	return removed, nil
}

// Stats implements store.Queue.
func (s *Store) Stats(ctx context.Context) (store.Stats, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM deliveries GROUP BY status`)
	if err != nil {
		return store.Stats{}, fmt.Errorf("sqlite: stats: %w", err)
	}
	defer rows.Close()

	var out store.Stats
	for rows.Next() {
		var (
			status string
			n      int
		)
		if err := rows.Scan(&status, &n); err != nil {
			return store.Stats{}, fmt.Errorf("sqlite: stats: %w", err)
		}
		switch store.Status(status) {
		case store.StatusQueued:
			out.Queued = n
		case store.StatusSending:
			out.Sending = n
		case store.StatusSent:
			out.Sent = n
		case store.StatusFailed:
			out.Failed = n
		}
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- scanning

type scannable interface {
	Scan(dest ...any) error
}

func scanDelivery(row scannable) (*store.Delivery, error) {
	var (
		d         store.Delivery
		status    string
		next      int64
		created   int64
		updated   int64
		claimedAt int64
		sentAt    int64
	)

	if err := row.Scan(
		&d.ID, &d.RequestID, &d.Target, &d.ChannelType, &status, &d.Attempts,
		&next, &d.LastError, &d.LastClass, &created, &updated, &claimedAt, &sentAt,
	); err != nil {
		return nil, err
	}

	d.Status = store.Status(status)
	d.NextAttemptAt = fromNanos(next)
	d.CreatedAt = fromNanos(created)
	d.UpdatedAt = fromNanos(updated)
	d.ClaimedAt = fromNanos(claimedAt)
	if sentAt != 0 {
		t := fromNanos(sentAt)
		d.SentAt = &t
	}
	return &d, nil
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
