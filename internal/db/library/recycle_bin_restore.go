package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Get answers one recycle bin entry by id. A missing id surfaces
// sql.ErrNoRows, wrapped.
func (q RecycleBin) Get(ctx context.Context, id int64) (Entry, error) {
	row, err := librarydb.New(q.store.Read()).GetRecycleBin(ctx, id)
	if err != nil {
		return Entry{}, fmt.Errorf("recycle bin: get %d: %w", id, err)
	}
	return recycleBinEntryFromRow(row), nil
}

// ReplacedBy lists what the bin still holds that mediaObjectID took the place of.
func (q RecycleBin) ReplacedBy(ctx context.Context, mediaObjectID int64) ([]Entry, error) {
	rows, err := librarydb.New(q.store.Read()).ListRecycledInFavourOf(ctx, sql.NullInt64{Int64: mediaObjectID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("recycle bin: replaced by object %d: %w", mediaObjectID, err)
	}
	entries := make([]Entry, len(rows))
	for index, row := range rows {
		entries[index] = recycleBinEntryFromRow(row)
	}
	return entries, nil
}

// Restore puts a recycled file back; refused if already restored or if the original path is occupied, surfacing the conflict (section 10.8).
func (q RecycleBin) Restore(ctx context.Context, id int64) (Entry, error) {
	entry, err := q.Get(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	if entry.RestoredAt != nil {
		return Entry{}, fmt.Errorf("%w: id %d", ErrAlreadyRestored, id)
	}

	if _, err := q.fsys.Lstat(entry.OriginalPath); err == nil {
		return Entry{}, fmt.Errorf("%w: %s", ErrRestoreConflict, entry.OriginalPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Entry{}, fmt.Errorf("recycle bin: check %q before restoring: %w", entry.OriginalPath, err)
	}

	// Recycling with "delete empty folders" took the file's folder with it.
	if err := q.fsys.MkdirAll(filepath.Dir(entry.OriginalPath), 0o755); err != nil {
		return Entry{}, fmt.Errorf("recycle bin: recreate the folder of %q: %w", entry.OriginalPath, err)
	}
	if err := relocate(q.fsys, entry.RecycledPath, entry.OriginalPath, entry.SizeBytes); err != nil {
		return Entry{}, fmt.Errorf(
			"recycle bin: restore %q to %q: %w", entry.RecycledPath, entry.OriginalPath, err)
	}

	now := q.clock.Now().Unix()
	var rows int64
	err = q.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var writeErr error
		rows, writeErr = librarydb.New(tx).MarkRecycleBinRestored(ctx, librarydb.MarkRecycleBinRestoredParams{
			RestoredAt: sql.NullInt64{Int64: now, Valid: true}, ID: id,
		})
		return writeErr
	})
	if err != nil {
		return Entry{}, fmt.Errorf(
			"recycle bin: restored %q on disk but failed to record it: %w", entry.OriginalPath, err)
	}
	if rows == 0 {
		// The file is already physically back -- reported so the caller does
		// not treat the restore as having failed, only its bookkeeping.
		return Entry{}, fmt.Errorf(
			"recycle bin: restored %q on disk but the row was already marked restored", entry.OriginalPath)
	}

	entry.RestoredAt = &now
	return entry, nil
}

// Purge removes a recycled file's bytes and row; there is no purged_at, and an already-restored entry is refused as it holds nothing.
func (q RecycleBin) Purge(ctx context.Context, id int64) error {
	entry, err := q.Get(ctx, id)
	if err != nil {
		return err
	}
	if entry.RestoredAt != nil {
		return fmt.Errorf("recycle bin: %d was already restored, nothing to purge", id)
	}
	// A root that is not mounted answers "no such file" for everything: dropping the row would lose the file.
	if entry.RootFolderID != nil && q.online != nil && !q.online(*entry.RootFolderID) {
		return fmt.Errorf("%w: root folder %d, entry %d", ErrRootOffline, *entry.RootFolderID, id)
	}

	if err := q.fsys.Remove(entry.RecycledPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("recycle bin: remove %q: %w", entry.RecycledPath, err)
	}

	err = q.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteRecycleBinEntry(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("recycle bin: purged %q on disk but failed to remove its row: %w", entry.RecycledPath, err)
	}
	return nil
}

// maxRecycleBinList bounds one listing; the totals still count everything.
const maxRecycleBinList = 500

// List answers what the bin holds, newest first, with the count and bytes of the whole bin.
func (q RecycleBin) List(ctx context.Context) (entries []Entry, totalEntries, totalBytes int64, err error) {
	reader := librarydb.New(q.store.Read())
	rows, err := reader.ListActiveRecycleBinNewestFirst(ctx, maxRecycleBinList)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("recycle bin: list: %w", err)
	}
	for _, row := range rows {
		entries = append(entries, recycleBinEntryFromRow(row))
	}
	if totalBytes, err = reader.SumRecycledBytes(ctx); err != nil {
		return nil, 0, 0, fmt.Errorf("recycle bin: sum: %w", err)
	}
	if totalEntries, err = reader.CountRecycleBin(ctx); err != nil {
		return nil, 0, 0, fmt.Errorf("recycle bin: count: %w", err)
	}
	return entries, totalEntries, totalBytes, nil
}

// Empty purges every entry now, in batches; an entry that will not purge is counted and skipped, not retried.
func (q RecycleBin) Empty(ctx context.Context) (RecycleBinSweepResult, error) {
	var result RecycleBinSweepResult
	failed := map[int64]bool{}
	for {
		rows, err := librarydb.New(q.store.Read()).ListActiveRecycleBinOldestFirst(ctx, int64(len(failed))+200)
		if err != nil {
			return result, fmt.Errorf("recycle bin: empty: %w", err)
		}
		progressed := false
		for _, row := range rows {
			if failed[row.ID] {
				continue
			}
			if err := q.Purge(ctx, row.ID); err != nil {
				failed[row.ID] = true
				result.Errors = append(result.Errors, err)
				continue
			}
			result.Purged++
			result.BytesFreed += row.SizeBytes
			progressed = true
		}
		if !progressed {
			return result, nil
		}
	}
}
