package sqlite

import (
	"context"
	"fmt"
	"time"

	"notifyrelay/internal/store"
)

// RecordAdminAction implements store.AdminAudit.
func (s *Store) RecordAdminAction(ctx context.Context, a *store.AdminAction) error {
	if a == nil {
		return nil
	}
	at := a.At
	if at.IsZero() {
		at = s.clock()
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_audit (at, actor, action, target, detail)
		VALUES (?, ?, ?, ?, ?)`,
		toNanos(at), a.Actor, a.Action, a.Target, a.Detail)
	if err != nil {
		return fmt.Errorf("sqlite: record admin action: %w", err)
	}
	if id, err := res.LastInsertId(); err == nil {
		a.ID = id
	}
	a.At = at
	return nil
}

// ListAdminActions implements store.AdminAudit.
func (s *Store) ListAdminActions(ctx context.Context, limit int) ([]*store.AdminAction, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, actor, action, target, detail
		FROM admin_audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list admin actions: %w", err)
	}
	defer rows.Close()

	var out []*store.AdminAction
	for rows.Next() {
		var (
			a  store.AdminAction
			at int64
		)
		if err := rows.Scan(&a.ID, &at, &a.Actor, &a.Action, &a.Target, &a.Detail); err != nil {
			return nil, fmt.Errorf("sqlite: read admin action: %w", err)
		}
		a.At = fromNanos(at)
		out = append(out, &a)
	}
	return out, rows.Err()
}

// PruneAdminActions implements store.AdminAudit.
//
// Nothing calls this yet, and that is deliberate: an operator action trail is
// the one thing in this database that is more useful the older it is. The
// method exists so a deployment that needs a retention policy has somewhere to
// put one.
func (s *Store) PruneAdminActions(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM admin_audit WHERE at < ?`, toNanos(before))
	if err != nil {
		return 0, fmt.Errorf("sqlite: prune admin actions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: prune admin actions: %w", err)
	}
	return int(n), nil
}
