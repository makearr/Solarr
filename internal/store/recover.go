package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
)

// ErrCorrupt reports a database SQLite will not vouch for. It wraps whatever the
// check actually said, so the log names the first damaged page rather than "corrupt".
var ErrCorrupt = errors.New("store: database is corrupt")

// ErrUnrecoverable reports corruption that no backup in the retention set could
// repair. Refusing to start is preferred over starting with silent data loss (ADR-15).
var ErrUnrecoverable = errors.New("store: no usable backup")

// walSuffixes are the files SQLite keeps beside the database. A stale log left next
// to a restored copy is a second corruption, so Restore clears them.
var walSuffixes = []string{"-wal", "-shm"}

// RecoveryOptions configures OpenChecked.
type RecoveryOptions struct {
	// BackupDir is where restorable copies live. Empty means corruption is fatal
	// rather than recoverable, which is what a database with no backups deserves.
	BackupDir string

	// Deep forces integrity_check on the first pass instead of quick_check.
	Deep bool
}

// RecoveryReport says what OpenChecked had to do. Recovery is loud, never silent
// (ADR-15), and this is what the caller logs and notifies from.
type RecoveryReport struct {
	// Database is which one this is about, so a caller holding several can tell
	// them apart.
	Database string

	// Recovered is false on the ordinary path, where the database checked out.
	Recovered bool

	// CorruptPath is where the damaged file was moved. It is never deleted.
	CorruptPath string

	// From is the backup that was restored, and BackupAt when it was taken --
	// the notification has to say how much history was lost.
	From     string
	BackupAt time.Time

	// Rejected names the backups tried before From, newest first, that were
	// themselves corrupt.
	Rejected []string
}

// HistoryLost is how far behind the restored backup is. Zero when nothing was restored.
func (r RecoveryReport) HistoryLost(now time.Time) time.Duration {
	if !r.Recovered {
		return 0
	}
	return now.Sub(r.BackupAt)
}

// OpenChecked opens a database, verifying it first and restoring a backup over it if
// the check fails (ADR-15, section 15.1 step 2). The check runs on its own throwaway
// connection before Open, because Open settles the page size by querying the schema
// and so fails on a corrupt file before any of the three real handles exist.
func OpenChecked(ctx context.Context, c clock.Clock, config Config, options RecoveryOptions) (*Store, RecoveryReport, error) {
	config = config.withDefaults()

	healthy := RecoveryReport{Database: config.Name}

	err := probe(ctx, config, options.Deep)
	if err == nil {
		opened, openErr := Open(ctx, c, config)
		return opened, healthy, openErr
	}
	if !errors.Is(err, ErrCorrupt) {
		return nil, healthy, err
	}
	// quick_check has false positives, so a shallow failure is confirmed before
	// anything is moved aside (ADR-15).
	if !options.Deep {
		if confirm := probe(ctx, config, true); confirm == nil {
			opened, openErr := Open(ctx, c, config)
			return opened, healthy, openErr
		}
	}

	report, err := recoverFromBackup(ctx, c, config, options)
	if err != nil {
		return nil, report, err
	}
	opened, err := Open(ctx, c, config)
	return opened, report, err
}

// recoverFromBackup quarantines the damaged file and walks the retention set newest
// first until one restores clean.
func recoverFromBackup(ctx context.Context, c clock.Clock, config Config, options RecoveryOptions) (RecoveryReport, error) {
	if options.BackupDir == "" {
		return RecoveryReport{Database: config.Name}, fmt.Errorf("store %s: %w: no backup directory configured", config.Name, ErrUnrecoverable)
	}

	backups, err := ListBackups(config.FS, options.BackupDir, config.Name)
	if err != nil {
		return RecoveryReport{Database: config.Name}, err
	}
	if len(backups) == 0 {
		return RecoveryReport{Database: config.Name}, fmt.Errorf("store %s: %w: %s holds no backups",
			config.Name, ErrUnrecoverable, options.BackupDir)
	}

	corruptPath, err := quarantine(c, config)
	if err != nil {
		return RecoveryReport{Database: config.Name}, err
	}
	report := RecoveryReport{Database: config.Name, CorruptPath: corruptPath}

	for _, backup := range backups {
		if err := Restore(config.FS, backup.Path, config.Path); err != nil {
			return report, err
		}
		if err := probe(ctx, config, true); err != nil {
			report.Rejected = append(report.Rejected, backup.Path)
			continue
		}
		report.Recovered = true
		report.From = backup.Path
		report.BackupAt = backup.At
		return report, nil
	}

	return report, fmt.Errorf("store %s: %w: all %d backups in %s are corrupt too; the damaged database is kept at %s",
		config.Name, ErrUnrecoverable, len(backups), options.BackupDir, corruptPath)
}

// quarantine moves the database aside as <path>.corrupt.<timestamp>, never deleting
// it. Only the database: the driver discards an unreadable log when probe opens the
// file, so by here there is never one left to move (documentation/database-layout.md).
func quarantine(c clock.Clock, config Config) (string, error) {
	stamp := strings.ReplaceAll(c.Now().UTC().Format(time.RFC3339), ":", "-")
	corruptPath := fmt.Sprintf("%s.corrupt.%s", config.Path, stamp)
	if _, err := config.FS.Stat(corruptPath); err == nil {
		return "", fmt.Errorf("store %s: quarantine target %q already exists", config.Name, corruptPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("store %s: check quarantine target %q: %w", config.Name, corruptPath, err)
	}

	if err := config.FS.Rename(config.Path, corruptPath); err != nil {
		return "", fmt.Errorf("store %s: quarantine %q: %w", config.Name, config.Path, err)
	}
	return corruptPath, nil
}

// Restore copies a backup over a database path. The database must not be open --
// SQLite cannot have its file replaced under a live connection, which is why this is
// a package function and not the (*Store).Restore that architecture section 4.5 sketches.
func Restore(fsys fsx.FS, backupPath, databasePath string) error {
	source, err := fsys.Open(backupPath)
	if err != nil {
		return fmt.Errorf("store: open backup %q: %w", backupPath, err)
	}
	defer func() { _ = source.Close() }()

	// Atomic and 0600: a half-copied database must never be openable, and a restored
	// one is no less sensitive than the original (ADR-14, Invariant 68).
	err = fsx.WriteAtomicSecret(fsys, databasePath, func(w io.Writer) error {
		_, copyErr := io.Copy(w, source)
		return copyErr
	})
	if err != nil {
		return fmt.Errorf("store: restore %q to %q: %w", backupPath, databasePath, err)
	}
	// A backup is a VACUUM INTO snapshot with no log of its own, so any log still
	// beside the database belongs to the file that was just replaced.
	for _, suffix := range walSuffixes {
		if err := fsys.Remove(databasePath + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("store: remove stale %q: %w", databasePath+suffix, err)
		}
	}
	return nil
}

// Check runs the integrity pragma against the open database (architecture section 4.5).
func (s *Store) Check(ctx context.Context, deep bool) error {
	return checkOn(ctx, s.read, s.config.Name, deep)
}

// probe checks a database on a connection of its own, without creating it. A file
// that is not there yet is a fresh install, which has nothing to check and nothing
// to recover -- and creating it here would beat createRestricted to its 0600 mode.
func probe(ctx context.Context, config Config, deep bool) error {
	if _, err := config.FS.Stat(config.Path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("store %s: check %s: %w", config.Name, config.Path, err)
	}

	db, err := sql.Open(driverName, "file:"+config.Path)
	if err != nil {
		return fmt.Errorf("store %s: open for integrity check: %w", config.Name, err)
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()

	return checkOn(ctx, db, config.Name, deep)
}

// checkOn reports whatever SQLite says that is not the single row "ok". A file too
// damaged to answer the pragma at all is corrupt by the same definition.
func checkOn(ctx context.Context, db *sql.DB, name string, deep bool) error {
	pragma := "PRAGMA quick_check"
	if deep {
		pragma = "PRAGMA integrity_check"
	}

	rows, err := db.QueryContext(ctx, pragma)
	if err != nil {
		return fmt.Errorf("store %s: %w: %s: %w", name, ErrCorrupt, pragma, err)
	}
	defer func() { _ = rows.Close() }()

	var findings []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return fmt.Errorf("store %s: %w: %s: %w", name, ErrCorrupt, pragma, err)
		}
		if line != "ok" {
			findings = append(findings, line)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store %s: %w: %s: %w", name, ErrCorrupt, pragma, err)
	}
	if len(findings) > 0 {
		return fmt.Errorf("store %s: %w: %s reported %s", name, ErrCorrupt, pragma, strings.Join(findings, "; "))
	}
	return nil
}
