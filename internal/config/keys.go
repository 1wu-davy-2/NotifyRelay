package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"notifyrelay/internal/secret"
)

// Key resolution.
//
// The service has two long-lived keys and neither can be derived from the
// other: one seals channel credentials in the database, the other signs
// operator session cookies. Deriving either from the other, or from an API key
// digest, would mean a weakness in the weaker use is a weakness in both — and
// the two uses have nothing in common except being "a secret".
//
// Resolution order for each, independently:
//
//  1. the value in the configuration file, which always wins;
//  2. the key file in the data directory, from a previous run;
//  3. a newly generated key, written to that file.
//
// Step 3 is what makes `docker compose up -d` a complete deployment. Without it
// an operator has to generate two secrets on a workstation and copy them in
// before the service will start, which is exactly the friction that leads to
// somebody reusing one value for both — or committing it.

// KeyOrigin says where a key came from, so the service can say so at startup.
//
// It exists because "where is this key" is the first question when a deployment
// is restored from a backup that turns out not to contain it, and answering it
// from the log beats answering it from the source.
type KeyOrigin string

const (
	// KeyFromConfig means the configuration file supplied it.
	KeyFromConfig KeyOrigin = "configuration file"
	// KeyFromFile means a key file in the data directory supplied it.
	KeyFromFile KeyOrigin = "key file"
	// KeyGenerated means it was created on this boot and written to disk.
	KeyGenerated KeyOrigin = "newly generated"
)

// KeysDir is the directory under the data directory that holds generated keys.
const KeysDir = "keys"

// ResolvedKey is a key and where it came from.
//
// Encoded is carried alongside the parsed key rather than recovered from it:
// secret.Key deliberately redacts itself when printed, so there is no way back
// to the text form — which is the property that keeps a key out of a log line.
type ResolvedKey struct {
	Key     secret.Key
	Encoded string
	Origin  KeyOrigin
	Path    string
}

// DataDir is the directory holding the database, and therefore the generated
// keys.
//
// Derived from storage.path rather than configured separately: a second setting
// naming the same directory is a second setting that can disagree with the
// first, and the failure that produces — keys written somewhere the backup does
// not cover — is discovered on restore day.
func (c *Config) DataDir() string {
	if strings.TrimSpace(c.Storage.Path) == "" {
		return ""
	}
	return filepath.Dir(c.Storage.Path)
}

// ResolveKeys fills in secret_key and admin.session_key, generating and
// persisting either that the configuration does not supply.
//
// Called after Load and before anything reads either key. The values are
// written back onto the configuration so that every existing consumer —
// Cipher(), the session store — keeps working unchanged.
func (c *Config) ResolveKeys() ([]ResolvedKey, error) {
	dir := c.DataDir()

	resolved := make([]ResolvedKey, 0, 2)

	secretKey, err := ResolveKey(c.SecretKey, dir, "secret")
	if err != nil {
		return nil, err
	}
	c.SecretKey = secretKey.Encoded
	resolved = append(resolved, secretKey)

	// Only when the operator surface is on. Generating a session key for a
	// deployment that has no sessions would leave a credential on disk for a
	// feature nobody enabled, which is clutter at best.
	if c.Admin.Enabled {
		sessionKey, err := ResolveKey(c.Admin.SessionKey, dir, "session")
		if err != nil {
			return nil, err
		}
		c.Admin.SessionKey = sessionKey.Encoded
		resolved = append(resolved, sessionKey)

		// Two purposes, two keys. Checked here rather than in validate()
		// because this is the first point at which both values are known:
		// before resolution one or both may be empty, and empty is not a
		// collision.
		if c.SecretKey == c.Admin.SessionKey {
			return nil, errors.New(
				"admin.session_key and secret_key resolved to the same value: " +
					"one key must not serve two purposes")
		}
	}

	return resolved, nil
}

// ResolveKey returns the key for one purpose.
//
// dir is the data directory — the one holding the database, so that a backup of
// it is a complete, restorable backup. That property is the reason the keys
// live there rather than somewhere the operator has to remember separately, and
// it is also the trade-off being made: whoever holds a copy of the data
// directory holds the credentials sealed with this key. For an internal
// service, "one directory restores everything" is worth more than "a leaked
// dump is unreadable", because the failure that actually happens is losing the
// key, not leaking the dump.
//
// The file is created 0600. A key that is world-readable is a key every process
// on the host has.
func ResolveKey(configured, dir, name string) (ResolvedKey, error) {
	if strings.TrimSpace(configured) != "" {
		key, err := secret.ParseKey(configured)
		if err != nil {
			// The name is passed through so the operator is told which of the
			// two keys is malformed, not merely that one of them is.
			return ResolvedKey{}, fmt.Errorf("%s: %w", name, err)
		}
		return ResolvedKey{Key: key, Encoded: configured, Origin: KeyFromConfig}, nil
	}

	if strings.TrimSpace(dir) == "" {
		return ResolvedKey{}, fmt.Errorf(
			"%s: not set in the configuration and there is no data directory to keep a generated one in", name)
	}

	path := filepath.Join(dir, KeysDir, name+".key")

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		encoded := strings.TrimSpace(string(raw))
		key, perr := secret.ParseKey(encoded)
		if perr != nil {
			return ResolvedKey{}, fmt.Errorf("%s: %s is not a usable key: %w", name, path, perr)
		}
		return ResolvedKey{Key: key, Encoded: encoded, Origin: KeyFromFile, Path: path}, nil

	case errors.Is(err, fs.ErrNotExist):
		// First boot, or a data directory that has lost its keys. Both end the
		// same way here; the difference shows up when the service tries to open
		// a sealed credential and finds it cannot.

	default:
		return ResolvedKey{}, fmt.Errorf("%s: reading %s: %w", name, path, err)
	}

	key, encoded, err := secret.GenerateKey()
	if err != nil {
		return ResolvedKey{}, fmt.Errorf("%s: generating: %w", name, err)
	}
	if err := writeKeyFile(path, encoded); err != nil {
		return ResolvedKey{}, err
	}
	return ResolvedKey{Key: key, Encoded: encoded, Origin: KeyGenerated, Path: path}, nil
}

// writeKeyFile writes a key so that it is never briefly readable by anyone else.
//
// Written to a temporary file with the mode already set and then renamed into
// place, rather than created and then chmodded: between those two steps the key
// exists with the default mode, which on a permissive umask is 0644.
func writeKeyFile(path, encoded string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("secret key: creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".key-*")
	if err != nil {
		return fmt.Errorf("secret key: creating a temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	// Every failure past this point has to remove the temporary file, or a
	// half-written key is left behind for the next boot to find and reject.
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secret key: setting the mode on %s: %w", tmpName, err)
	}
	if _, err := tmp.WriteString(encoded + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("secret key: writing %s: %w", tmpName, err)
	}
	// Flushed before the rename: a rename that lands before the contents do
	// leaves a key file that is present, correctly named, and empty.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("secret key: flushing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("secret key: closing %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("secret key: renaming %s to %s: %w", tmpName, path, err)
	}
	tmpName = "" // the deferred removal must not delete the key we just wrote
	return nil
}
