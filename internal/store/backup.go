package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// maxBackupDisambiguation caps the search for an unused backup name. Reaching it
// means something is generating backups in a tight loop, which is a bug worth
// reporting rather than papering over.
const maxBackupDisambiguation = 100

// Backup writes a consistent copy of the database into directory and returns its
// path.
//
// VACUUM INTO rather than a file copy: it produces a defragmented, internally
// consistent snapshot without stopping writers, and copying the file while WAL
// is active gives a torn database that looks fine until it is restored.
//
// It cannot run inside a transaction, so it goes through the queue's exclusive
// path — still the single writer, still serialised against everything else.
func (s *Store) Backup(ctx context.Context, directory string) (string, error) {
	if !filepath.IsAbs(directory) {
		return "", fmt.Errorf("store %s: backup directory %q must be absolute", s.config.Name, directory)
	}

	path, err := s.unusedBackupPath(directory)
	if err != nil {
		return "", err
	}

	err = s.writeExclusive(ctx, PrioBulk, true, func(ctx context.Context, db *sql.DB) error {
		// VACUUM INTO refuses to overwrite, which is the behaviour we want; the
		// name is chosen above rather than relying on that refusal.
		_, execErr := db.ExecContext(ctx, "VACUUM INTO ?", path)
		return execErr
	})
	if err != nil {
		return "", fmt.Errorf("store %s: backup to %q: %w", s.config.Name, path, err)
	}
	return path, nil
}

// unusedBackupPath finds a free name. ADR-2's naming is second-resolution, so
// two backups within one second — a pre-migration backup and a scheduled one, or
// anything running against a frozen clock — would otherwise collide and fail.
func (s *Store) unusedBackupPath(directory string) (string, error) {
	stamp := strings.ReplaceAll(s.clock.Now().UTC().Format("2006-01-02T15:04:05Z"), ":", "-")
	base := filepath.Join(directory, fmt.Sprintf("%s.db.%s", s.config.Name, stamp))

	for attempt := range maxBackupDisambiguation {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", base, attempt)
		}
		_, err := s.fs.Stat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("store %s: check backup path %q: %w", s.config.Name, candidate, err)
		}
	}
	return "", fmt.Errorf("store %s: %d backups already exist for this second in %q",
		s.config.Name, maxBackupDisambiguation, directory)
}
