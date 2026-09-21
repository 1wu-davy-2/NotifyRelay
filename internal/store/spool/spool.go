// Package spool stores message bodies as files on disk.
//
// The queue's database holds status, counters and indexes; the body lives
// here. Keeping them apart is what lets the queue table stay small enough to
// update quickly under load, and it means a notification's text never has to
// be pulled into memory to answer "how many are pending?".
package spool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// maxIDLength bounds the identifier before it becomes part of a path.
const maxIDLength = 64

// Store writes bodies under a root directory.
//
// Files are sharded by the first two bytes of the identifier, so a directory
// never accumulates enough entries for a listing to become slow.
type Store struct {
	root string
}

// New creates a spool rooted at dir, creating it if necessary.
func New(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("spool: a directory is required")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("spool: create %s: %w", dir, err)
	}
	return &Store{root: dir}, nil
}

// Put writes a body.
//
// The write goes to a temporary file and is renamed into place, so a crash
// mid-write leaves an incomplete temp file rather than a truncated body that
// would later be delivered as if it were whole.
func (s *Store) Put(id string, data []byte) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("spool: create shard: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("spool: create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("spool: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("spool: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("spool: close: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("spool: rename into place: %w", err)
	}
	return nil
}

// Get reads a body.
func (s *Store) Get(id string) ([]byte, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("spool: read %s: %w", id, err)
	}
	return data, nil
}

// Delete removes a body. A body that is already gone is not an error: the
// caller is cleaning up, and the desired state has been reached.
func (s *Store) Delete(id string) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("spool: delete %s: %w", id, err)
	}
	return nil
}

// Prune removes bodies last modified before the cutoff and reports how many
// went.
//
// It walks the tree by modification time rather than consulting the database.
// A body whose database row has already been pruned is exactly the orphan
// this is here to collect.
func (s *Store) Prune(olderThan time.Time) (int, error) {
	removed := 0

	err := filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			// The file vanished underneath us; that is the outcome we wanted.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.ModTime().Before(olderThan) {
			return nil
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		removed++
		return nil
	})

	if err != nil {
		return removed, fmt.Errorf("spool: prune: %w", err)
	}
	return removed, nil
}

// path turns an identifier into a sharded file path.
func (s *Store) path(id string) (string, error) {
	if err := validateID(id); err != nil {
		return "", err
	}
	return filepath.Join(s.root, id[:2], id[2:4], id+".json"), nil
}

// validateID rejects anything that is not a plain lowercase hex identifier.
//
// The identifiers are ours, but they travel through configuration and HTTP
// paths, and an identifier like "../../etc/passwd" would otherwise be a file
// write outside the spool.
func validateID(id string) error {
	if len(id) < 4 || len(id) > maxIDLength {
		return fmt.Errorf("spool: identifier must be 4-%d characters, got %d", maxIDLength, len(id))
	}
	for _, r := range id {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
		if !isHex {
			return fmt.Errorf("spool: identifier %q contains %q; only lowercase hex is allowed", id, r)
		}
	}
	return nil
}
