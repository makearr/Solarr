package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Get answers one quarantine entry by id. A missing id surfaces
// sql.ErrNoRows, wrapped.
func (q Quarantine) Get(ctx context.Context, id int64) (Entry, error) {
	row, err := librarydb.New(q.store.Read()).GetQuarantine(ctx, id)
	if err != nil {
		return Entry{}, fmt.Errorf("quarantine: get %d: %w", id, err)
	}
	return quarantineEntryFromRow(row), nil
}

// Restore puts a quarantined file back where it came from. Refused if it was
// already restored, and refused -- rather than silently overwriting -- if
// something now occupies the original path (section 10.8: "restoring an item
// whose desired state has since been satisfied differently surfaces the
// conflict rather than overwriting").
func (q Quarantine) Restore(ctx context.Context, id int64) (Entry, error) {
	entry, err := q.Get(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	if entry.RestoredAt != nil {
		return Entry{}, fmt.Errorf("%w: id %d", ErrAlreadyRestored, id)
	}

	if _, err := q.fsys.Stat(entry.OriginalPath); err == nil {
		return Entry{}, fmt.Errorf("%w: %s", ErrRestoreConflict, entry.OriginalPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Entry{}, fmt.Errorf("quarantine: check %q before restoring: %w", entry.OriginalPath, err)
	}

	if err := q.fsys.Rename(entry.QuarantinePath, entry.OriginalPath); err != nil {
		return Entry{}, fmt.Errorf(
			"quarantine: restore %q to %q: %w", entry.QuarantinePath, entry.OriginalPath, err)
	}

	now := q.clock.Now().Unix()
	var rows int64
	err = q.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var writeErr error
		rows, writeErr = librarydb.New(tx).MarkQuarantineRestored(ctx, librarydb.MarkQuarantineRestoredParams{
			RestoredAt: sql.NullInt64{Int64: now, Valid: true}, ID: id,
		})
		return writeErr
	})
	if err != nil {
		return Entry{}, fmt.Errorf(
			"quarantine: restored %q on disk but failed to record it: %w", entry.OriginalPath, err)
	}
	if rows == 0 {
		// The file is already physically back -- reported so the caller does
		// not treat the restore as having failed, only its bookkeeping.
		return Entry{}, fmt.Errorf(
			"quarantine: restored %q on disk but the row was already marked restored", entry.OriginalPath)
	}

	entry.RestoredAt = &now
	return entry, nil
}

// Purge removes a quarantined file's bytes for good and its row with them --
// there is no purged_at column, since once the bytes are gone there is
// nothing left for one to distinguish from. Refused on an already-restored
// entry: its quarantine_path no longer holds anything to remove.
func (q Quarantine) Purge(ctx context.Context, id int64) error {
	entry, err := q.Get(ctx, id)
	if err != nil {
		return err
	}
	if entry.RestoredAt != nil {
		return fmt.Errorf("quarantine: %d was already restored, nothing to purge", id)
	}

	if err := q.fsys.Remove(entry.QuarantinePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("quarantine: remove %q: %w", entry.QuarantinePath, err)
	}

	err = q.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteQuarantineEntry(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("quarantine: purged %q on disk but failed to remove its row: %w", entry.QuarantinePath, err)
	}
	return nil
}
