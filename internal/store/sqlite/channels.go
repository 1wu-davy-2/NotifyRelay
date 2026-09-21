package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"notifyrelay/internal/store"
)

// ListChannels implements store.Channels.
func (s *Store) ListChannels(ctx context.Context) ([]*store.ChannelInstance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, type, enabled, config_json, quota_json, created_at, updated_at
		FROM channel_instances`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list channels: %w", err)
	}
	defer rows.Close()

	var out []*store.ChannelInstance
	for rows.Next() {
		ci, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ci)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list channels: %w", err)
	}

	// Sorted here rather than in SQL: the caller builds the router from this
	// list, and a router whose channel order changes between restarts produces
	// logs that are hard to diff. The list is small enough that the sort costs
	// nothing worth measuring.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetChannel implements store.Channels.
func (s *Store) GetChannel(ctx context.Context, name string) (*store.ChannelInstance, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT name, type, enabled, config_json, quota_json, created_at, updated_at
		FROM channel_instances WHERE name = ?`, name)

	ci, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return ci, err
}

// PutChannel implements store.Channels.
//
// Upsert rather than separate create and update: the caller's intent is "this
// is what the channel should be", and making it choose between two statements
// adds a race — between the read that decides which one to use and the write
// that acts on it, another request can create the row.
func (s *Store) PutChannel(ctx context.Context, ci *store.ChannelInstance) error {
	if ci == nil || ci.Name == "" {
		return errors.New("sqlite: a channel instance needs a name")
	}

	config, err := encodeJSON(ci.Config)
	if err != nil {
		return fmt.Errorf("sqlite: channel %s: config: %w", ci.Name, err)
	}
	quota, err := encodeJSON(ci.Quota)
	if err != nil {
		return fmt.Errorf("sqlite: channel %s: quota: %w", ci.Name, err)
	}

	now := s.clock()
	enabled := 0
	if ci.Enabled {
		enabled = 1
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO channel_instances
			(name, type, enabled, config_json, quota_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET
			type        = excluded.type,
			enabled     = excluded.enabled,
			config_json = excluded.config_json,
			quota_json  = excluded.quota_json,
			updated_at  = excluded.updated_at`,
		ci.Name, ci.Type, enabled, config, quota, toNanos(now), toNanos(now))
	if err != nil {
		return fmt.Errorf("sqlite: put channel %s: %w", ci.Name, err)
	}

	ci.CreatedAt = now
	ci.UpdatedAt = now
	return nil
}

// DeleteChannel implements store.Channels.
func (s *Store) DeleteChannel(ctx context.Context, name string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM channel_instances WHERE name = ?`, name)
	if err != nil {
		return false, fmt.Errorf("sqlite: delete channel %s: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite: delete channel %s: %w", name, err)
	}
	return n > 0, nil
}

// CountChannels implements store.Channels.
func (s *Store) CountChannels(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM channel_instances`).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: count channels: %w", err)
	}
	return n, nil
}

// scanner is what scanChannel needs, so it works for both a *sql.Row and a
// *sql.Rows.
type scanner interface {
	Scan(...any) error
}

func scanChannel(sc scanner) (*store.ChannelInstance, error) {
	var (
		ci                   store.ChannelInstance
		enabled              int
		configJSON, quotaJSON string
		createdAt, updatedAt int64
	)

	if err := sc.Scan(&ci.Name, &ci.Type, &enabled, &configJSON, &quotaJSON,
		&createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("sqlite: read channel: %w", err)
	}

	ci.Enabled = enabled != 0
	ci.CreatedAt = fromNanos(createdAt)
	ci.UpdatedAt = fromNanos(updatedAt)

	if err := decodeJSON(configJSON, &ci.Config); err != nil {
		return nil, fmt.Errorf("sqlite: channel %s: config: %w", ci.Name, err)
	}
	if err := decodeJSON(quotaJSON, &ci.Quota); err != nil {
		return nil, fmt.Errorf("sqlite: channel %s: quota: %w", ci.Name, err)
	}
	return &ci, nil
}

func encodeJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeJSON(raw string, v any) error {
	if raw == "" {
		raw = "{}"
	}
	return json.Unmarshal([]byte(raw), v)
}

// ---------------------------------------------------------------------- meta

// GetMeta implements store.Meta.
func (s *Store) GetMeta(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sqlite: get meta %s: %w", key, err)
	}
	return value, true, nil
}

// SetMeta implements store.Meta.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("sqlite: set meta %s: %w", key, err)
	}
	return nil
}
