// Package sqlite is the SQLite-backed implementation of store.Store.
//
// It is the first implementation, not the only possible one: MySQL or
// PostgreSQL differ in how a claim is made atomic (SELECT ... FOR UPDATE SKIP
// LOCKED rather than a single immediate write transaction) but nothing above
// the store package knows the difference.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	// Registers the "sqlite" driver. It is a pure-Go translation of SQLite,
	// so the binary stays static and CGO_ENABLED=0 keeps working.
	_ "modernc.org/sqlite"
)

const driverName = "sqlite"

// Store is a SQLite-backed store.
type Store struct {
	db *sql.DB

	// now is the clock used for timestamps the caller does not supply.
	//
	// It exists so retention and orphan recovery can be tested without
	// sleeping: a test that has to wait 24 hours to see whether pruning works
	// is a test that never runs.
	now func() time.Time
}

// SetClock replaces the store's clock. Intended for tests.
func (s *Store) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Store) clock() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

// Open opens a database, creating the file and its directory if needed.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite: a database path is required")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("sqlite: create %s: %w", dir, err)
		}
	}

	db, err := sql.Open(driverName, dsn(path))
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", path, err)
	}

	// WAL lets readers run while a writer holds the lock, which is the shape
	// of this workload: a few short writes against many status queries.
	//
	// The pool is small on purpose. SQLite serialises writers regardless, so
	// a large pool buys nothing and costs file descriptors; the point of more
	// than one connection is to keep the read path from queueing behind a
	// claim.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: ping %s: %w", path, err)
	}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

// dsn builds the connection string.
//
// Every setting has to be a DSN parameter rather than a PRAGMA issued after
// opening: database/sql pools connections, and a PRAGMA applies to the one
// connection it ran on. The others in the pool would silently run with the
// defaults.
func dsn(path string) string {
	params := url.Values{}

	// Begin transactions with BEGIN IMMEDIATE. Claiming a batch has to read
	// the due rows and mark them in one step; a deferred transaction would
	// take the write lock only at the first write, leaving a window where two
	// claimers pick the same row.
	params.Set("_txlock", "immediate")

	// Wait rather than fail when another writer holds the lock.
	params.Add("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "journal_mode(WAL)")
	// NORMAL under WAL is durable across process crashes; only a machine-level
	// power loss can lose the last transactions, and a notification that has
	// to survive that belongs in the sender's own retry, not here.
	params.Add("_pragma", "synchronous(NORMAL)")
	params.Add("_pragma", "foreign_keys(1)")

	return "file:" + path + "?" + params.Encode()
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("sqlite: apply schema: %w", err)
	}
	return nil
}

// Ping implements store.Store.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close implements store.Store.
func (s *Store) Close() error {
	return s.db.Close()
}

// --------------------------------------------------------------- conversions

// toNanos converts a time for storage. The zero time becomes 0 rather than a
// huge negative number that would sort before every real timestamp.
func toNanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixNano()
}

func fromNanos(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}
