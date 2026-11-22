package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// maxBackupDisambiguation caps the search for an unused backup name.
const maxBackupDisambiguation = 100

// BackupKind says why a backup was taken. Pre-migration backups are kept outside
// the rotation and never counted against retention (ADR-15), so it has to survive
// a restart in the filename -- there is nowhere else it is recorded.
type BackupKind string

const (
	// BackupRotation is a routine backup, subject to retention.
	BackupRotation BackupKind = "rotation"
	// BackupPreMigration runs unconditionally before every schema migration
	// (Invariant 7) and is kept outside the rotation.
	BackupPreMigration BackupKind = "premigration"
)

// Backup writes a consistent copy of the database into directory and returns its path.
func (s *Store) Backup(ctx context.Context, directory string, kind BackupKind) (string, error) {
	if !filepath.IsAbs(directory) {
		return "", fmt.Errorf("store %s: backup directory %q must be absolute", s.config.Name, directory)
	}
	if kind != BackupRotation && kind != BackupPreMigration {
		return "", fmt.Errorf("store %s: unsupported backup kind %q", s.config.Name, kind)
	}

	var path string
	err := s.copyOut(ctx, func() (string, error) {
		var err error
		path, err = s.unusedBackupPath(directory, kind)
		return path, err
	})
	if err != nil {
		return "", fmt.Errorf("store %s: backup to %q: %w", s.config.Name, path, err)
	}
	return path, nil
}

// SnapshotTo writes a consistent copy of the database to path, which must not exist. Writes go on meanwhile.
func (s *Store) SnapshotTo(ctx context.Context, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("store %s: snapshot path %q must be absolute", s.config.Name, path)
	}
	if err := s.copyOut(ctx, func() (string, error) { return path, nil }); err != nil {
		return fmt.Errorf("store %s: snapshot to %q: %w", s.config.Name, path, err)
	}
	return nil
}

// copyOut writes the copy VACUUM INTO makes of one read snapshot, outside the write queue: it reads under an ordinary read
// transaction and writes only the new file. choose runs under the lock, so simultaneous callers do not pick one name.
func (s *Store) copyOut(ctx context.Context, choose func() (string, error)) error {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	path, err := choose()
	if err != nil {
		return err
	}
	if s.copyHook != nil {
		s.copyHook()
	}
	// VACUUM INTO refuses to overwrite, which is the behaviour we want; the name is chosen rather than relying on that refusal.
	if _, err := s.backupConn.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return err
	}
	// SQLite creates the copy with the umask's mode; it carries what the live file's 0600 protects.
	return s.fs.Chmod(path, 0o600)
}

// unusedBackupPath finds a free name (ADR-2) with the kind embedded (<name>.db.<kind>.<timestamp>[-N]) so retention can tell pre-migration from rotation backups after a restart.
func (s *Store) unusedBackupPath(directory string, kind BackupKind) (string, error) {
	stamp := strings.ReplaceAll(s.clock.Now().UTC().Format("2006-01-02T15:04:05Z"), ":", "-")
	base := filepath.Join(directory, fmt.Sprintf("%s.db.%s.%s", s.config.Name, kind, stamp))

	for attempt := range maxBackupDisambiguation {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", base, attempt)
		}
		_, err := s.fs.Lstat(candidate)
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
