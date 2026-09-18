package store

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// minBackupsKept is the retention floor regardless of configuration -- "minimum 3
// enforced" (ADR-15) -- so a misconfigured keep value cannot leave the library with
// too little to recover from.
const minBackupsKept = 3

// backupStampLayout matches unusedBackupPath's stamp -- fixed width, so it can be
// sliced out of a name that may carry a "-N" disambiguation suffix after it.
const backupStampLayout = "2006-01-02T15-04-05Z"

// BackupInfo is one backup found on disk.
type BackupInfo struct {
	Path string
	Kind BackupKind
	At   time.Time
}

// ListBackups returns every backup for this database in directory, newest first.
func (s *Store) ListBackups(directory string) ([]BackupInfo, error) {
	return ListBackups(s.fs, directory, s.config.Name)
}

// ListBackups is the same listing without a store, because the database recovery is
// restoring cannot be opened. At comes from the name, not the file's mtime: mtime is
// the real filesystem clock, which the clock.Clock seam does not reach.
func ListBackups(fsys fsx.FS, directory, name string) ([]BackupInfo, error) {
	entries, err := fsys.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("store %s: list backups in %q: %w", name, directory, err)
	}

	prefix := name + ".db."
	var found []BackupInfo
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		info, ok := parseBackupName(entry.Name, prefix)
		if !ok {
			continue
		}
		info.Path = filepath.Join(directory, entry.Name)
		found = append(found, info)
	}
	// Path is a deterministic tiebreak for two backups the same second apart --
	// ReadDir's own order is not guaranteed, and ties are routine in a fast test.
	slices.SortFunc(found, func(a, b BackupInfo) int {
		if c := b.At.Compare(a.At); c != 0 {
			return c
		}
		return strings.Compare(b.Path, a.Path)
	})
	return found, nil
}

// parseBackupName recovers what unusedBackupPath encoded: <name>.db.<kind>.<stamp>,
// optionally followed by "-N" if it disambiguated a same-second collision.
func parseBackupName(name, prefix string) (BackupInfo, bool) {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return BackupInfo{}, false
	}

	for _, kind := range []BackupKind{BackupRotation, BackupPreMigration} {
		stamped, ok := strings.CutPrefix(rest, string(kind)+".")
		if !ok {
			continue
		}
		if len(stamped) < len(backupStampLayout) {
			return BackupInfo{}, false
		}
		at, err := time.Parse(backupStampLayout, stamped[:len(backupStampLayout)])
		if err != nil {
			return BackupInfo{}, false
		}
		return BackupInfo{Kind: kind, At: at}, true
	}
	return BackupInfo{}, false
}

// PruneBackups deletes rotation backups beyond keep, oldest first, and returns
// what it removed. Pre-migration backups are never touched -- ADR-15 keeps them
// outside the rotation entirely -- and keep is raised to minBackupsKept if it is
// configured lower.
func (s *Store) PruneBackups(directory string, keep int) ([]string, error) {
	if keep < minBackupsKept {
		keep = minBackupsKept
	}

	all, err := s.ListBackups(directory)
	if err != nil {
		return nil, err
	}

	var rotation []BackupInfo
	for _, backup := range all {
		if backup.Kind == BackupRotation {
			rotation = append(rotation, backup)
		}
	}
	if len(rotation) <= keep {
		return nil, nil
	}

	// rotation is newest first (ListBackups), so the tail past keep is the oldest.
	var removed []string
	for _, backup := range rotation[keep:] {
		if err := s.fs.Remove(backup.Path); err != nil {
			return removed, fmt.Errorf("store %s: remove backup %q: %w", s.config.Name, backup.Path, err)
		}
		removed = append(removed, backup.Path)
	}
	return removed, nil
}
