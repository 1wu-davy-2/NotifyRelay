package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"notifyrelay/internal/store"
)

// ListAPIKeys implements store.APIKeys.
func (s *Store) ListAPIKeys(ctx context.Context) ([]*store.APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, key_hash, enabled, created_at, last_used_at
		FROM api_keys ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list api keys: %w", err)
	}
	defer rows.Close()

	out := make([]*store.APIKey, 0, 8)
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list api keys: %w", err)
	}
	return out, nil
}

// GetAPIKey implements store.APIKeys.
func (s *Store) GetAPIKey(ctx context.Context, id string) (*store.APIKey, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, key_hash, enabled, created_at, last_used_at
		FROM api_keys WHERE id = ?`, id)

	k, err := scanAPIKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return k, err
}

// PutAPIKey implements store.APIKeys.
func (s *Store) PutAPIKey(ctx context.Context, k *store.APIKey) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO api_keys (id, name, key_hash, enabled, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name     = excluded.name,
			key_hash = excluded.key_hash,
			enabled  = excluded.enabled`,
		k.ID, k.Name, k.KeyHash, boolInt(k.Enabled), toNanos(k.CreatedAt), nanosOrNull(k.LastUsedAt))
	if err != nil {
		return fmt.Errorf("sqlite: put api key: %w", err)
	}
	return nil
}

// DeleteAPIKey implements store.APIKeys.
func (s *Store) DeleteAPIKey(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("sqlite: delete api key: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite: delete api key: %w", err)
	}
	return n > 0, nil
}

// FindAPIKeyByHash implements store.APIKeys.
//
// Disabled keys are filtered in SQL rather than by the caller: a disabled key
// must not authenticate, and a caller that forgot the check would be a caller
// that quietly honoured a revoked credential.
func (s *Store) FindAPIKeyByHash(ctx context.Context, keyHash string) (*store.APIKey, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, key_hash, enabled, created_at, last_used_at
		FROM api_keys WHERE key_hash = ? AND enabled = 1`, keyHash)

	k, err := scanAPIKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return k, err
}

// TouchAPIKey implements store.APIKeys.
//
// The WHERE clause is what keeps this off the hot path: once a key has been
// seen within the interval the statement matches no rows and writes nothing.
// An unconditional UPDATE would put a database write in front of every
// notification, which is a lot of cost for a column answering a question
// nobody asks during an incident.
func (s *Store) TouchAPIKey(ctx context.Context, id string, now time.Time, interval time.Duration) error {
	cutoff := toNanos(now.Add(-interval))

	_, err := s.db.ExecContext(ctx, `
		UPDATE api_keys SET last_used_at = ?
		WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`,
		toNanos(now), id, cutoff)
	if err != nil {
		return fmt.Errorf("sqlite: touch api key: %w", err)
	}
	return nil
}

// GetAdminCredential implements store.AdminCredentials.
func (s *Store) GetAdminCredential(ctx context.Context) (*store.AdminCredential, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT username, password_hash, created_at FROM admin_credentials WHERE id = 1`)

	var (
		c         store.AdminCredential
		createdAt int64
	)
	err := row.Scan(&c.Username, &c.PasswordHash, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		// Nil means "nobody has claimed this deployment yet", which is what
		// opens the first-run page. A real error is returned as an error and
		// never as nil: reporting a broken database as "unclaimed" would hand
		// the setup page to whoever asked next.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get admin credential: %w", err)
	}

	c.CreatedAt = fromNanos(createdAt)
	return &c, nil
}

// CreateAdminCredential implements store.AdminCredentials.
//
// DO NOTHING rather than DO UPDATE, and the result is read from RowsAffected:
// this is the conditional write that closes the first-run page. With DO UPDATE
// two simultaneous submissions would both report success and the second would
// quietly replace the first, which is a deployment taken over by whoever
// happened to click second.
func (s *Store) CreateAdminCredential(ctx context.Context, c *store.AdminCredential) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_credentials (id, username, password_hash, created_at)
		VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		c.Username, c.PasswordHash, toNanos(c.CreatedAt))
	if err != nil {
		return false, fmt.Errorf("sqlite: create admin credential: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite: create admin credential: %w", err)
	}
	return n > 0, nil
}

// SetAdminPassword implements store.AdminCredentials.
//
// An UPDATE with no insert path. The caller has already proved it can
// authenticate; what it must not be able to do is create the account it is
// changing, and "UPDATE ... ON CONFLICT DO UPDATE" or an upsert would let it.
func (s *Store) SetAdminPassword(ctx context.Context, hash string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE admin_credentials SET password_hash = ? WHERE id = 1`, hash)
	if err != nil {
		return false, fmt.Errorf("sqlite: set admin password: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite: set admin password: %w", err)
	}
	return n > 0, nil
}

// ---------------------------------------------------------------- scanning

func scanAPIKey(sc scanner) (*store.APIKey, error) {
	var (
		k          store.APIKey
		enabled    int
		createdAt  int64
		lastUsedAt sql.NullInt64
	)
	if err := sc.Scan(&k.ID, &k.Name, &k.KeyHash, &enabled, &createdAt, &lastUsedAt); err != nil {
		return nil, err
	}

	k.Enabled = enabled != 0
	k.CreatedAt = fromNanos(createdAt)
	if lastUsedAt.Valid && lastUsedAt.Int64 != 0 {
		t := fromNanos(lastUsedAt.Int64)
		k.LastUsedAt = &t
	}
	return &k, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nanosOrNull(t *time.Time) any {
	if t == nil {
		return nil
	}
	return toNanos(*t)
}
