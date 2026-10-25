package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"syscall"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Reason says why a file was recycled (section 5.8's recycle bin.reason
// column).
type Reason string

// The five reasons recycle bin.reason accepts.
const (
	ReasonRejectedWorse Reason = "rejected_worse"
	ReasonReplaced      Reason = "replaced"
	ReasonJunk          Reason = "junk"
	ReasonUserDelete    Reason = "user_delete"
	ReasonFailedImport  Reason = "failed_import"
)

// ErrAlreadyRestored reports that a recycle bin entry was already restored
// (or does not exist), so a second restore is refused rather than silently
// re-running.
var ErrAlreadyRestored = errors.New("recycle bin: entry already restored or does not exist")

// ErrRestoreConflict reports that the original path is occupied by something
// else now -- something has replaced whatever was recycled, and restoring
// would silently overwrite it (section 10.8: "surfaces the conflict rather
// than overwriting").
var ErrRestoreConflict = errors.New("recycle bin: original path already exists")

// Entry is one recycle bin row.
type Entry struct {
	ID            int64
	OriginalPath  string
	RecycledPath  string
	RootFolderID  *int64
	MediaObjectID *int64
	AcquisitionID *int64
	Reason        Reason
	SizeBytes     int64
	RecycledAt    int64
	ExpiresAt     int64
	RestoredAt    *int64
}

// PutRequest is one file to the recycle bin.
type PutRequest struct {
	// RootPath is the root folder's own absolute filesystem path -- recycle bin
	// lives on the same filesystem as its source, one directory per root
	// (section 10.8), so the caller resolves which root this file is under.
	RootPath      string
	OriginalPath  string
	RootFolderID  *int64
	MediaObjectID *int64
	AcquisitionID *int64
	Reason        Reason
	// Retain is how long this entry survives before Sweep purges it -- the
	// caller's own config.yaml recycle bin.retain (section 10.8), passed in
	// rather than read from settings.db by this type.
	Retain time.Duration
	// RemoveEmptyFolders removes the folders the move left empty, up to the root (a library's
	// "Delete empty folders", Sonarr's).
	RemoveEmptyFolders bool
}

// maxRecycleBinDisambiguation caps the search for an unused recycle bin path,
// the same bound store.unusedBackupPath already uses for the identical
// problem.
const maxRecycleBinDisambiguation = 100

// RecycleBin is section 10.8: nothing is deleted directly. Every destructive
// operation moves a file into a recycle bin directory first -- same
// filesystem as its source, time- and space-bounded, restorable with its
// provenance intact -- and only a later Sweep (or a manual Purge) removes
// the bytes for good.
//
// It does not read settings.db itself (ADR-25's own database split, and the
// same separation internal/moving/placement keeps from internal/db/settings) -- a
// caller resolves a root folder's path and the retention policy in force
// and passes both in.
type RecycleBin struct {
	store *store.Store
	fsys  fsx.FS
	clock clock.Clock
}

// NewRecycleBin builds it over an open library store.
func NewRecycleBin(opened *store.Store, fsys fsx.FS, cl clock.Clock) RecycleBin {
	return RecycleBin{store: opened, fsys: fsys, clock: cl}
}

// Put moves one file into the recycle bin and records it.
func (q RecycleBin) Put(ctx context.Context, req PutRequest) (Entry, error) {
	info, err := q.fsys.Lstat(req.OriginalPath)
	if err != nil {
		return Entry{}, fmt.Errorf("recycle bin: stat %q: %w", req.OriginalPath, err)
	}

	recycledPath, err := q.reserveRecycledPath(req.RootPath, req.OriginalPath)
	if err != nil {
		return Entry{}, err
	}

	if err := relocate(q.fsys, req.OriginalPath, recycledPath, info.Size); err != nil {
		return Entry{}, fmt.Errorf("recycle bin: move %q to %q: %w", req.OriginalPath, recycledPath, err)
	}

	if req.RemoveEmptyFolders {
		q.removeEmptyFolders(filepath.Dir(req.OriginalPath), req.RootPath)
	}

	now := q.clock.Now()
	var row librarydb.RecycleBin
	err = q.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var writeErr error
		row, writeErr = librarydb.New(tx).RecycleBin(ctx, librarydb.RecycleBinParams{
			OriginalPath: req.OriginalPath, RecycledPath: recycledPath,
			RootFolderID: nullInt64(req.RootFolderID), MediaObjectID: nullInt64(req.MediaObjectID),
			AcquisitionID: nullInt64(req.AcquisitionID), Reason: string(req.Reason),
			SizeBytes: info.Size, RecycledAt: now.Unix(), ExpiresAt: now.Add(req.Retain).Unix(),
		})
		return writeErr
	})
	if err != nil {
		// The move already happened -- rolling it back would leave the caller
		// worse off if the second rename also fails, and a file sitting in
		// recycle bin with no row is exactly what a startup orphan sweep
		// (a documented follow-up) exists to reconcile, not this call.
		return Entry{}, fmt.Errorf(
			"recycle bin: moved %q to %q but failed to record it: %w", req.OriginalPath, recycledPath, err)
	}
	return recycleBinEntryFromRow(row), nil
}

// relocate renames, and across filesystems copies, verifies and only then unlinks: nothing is deleted directly
// (user, 2026-10-24, replacing section 10.8's direct delete).
func relocate(fsys fsx.FS, from, to string, sizeBytes int64) error {
	err := fsys.Rename(from, to)
	if err == nil || !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if _, err := mover.PlaceOne(fsys, from, to, sizeBytes, []mover.PlaceMethod{mover.PlaceCopy}); err != nil {
		return err
	}
	if err := fsys.Remove(from); err != nil {
		// Two copies would leave the bin claiming a file that is still in place.
		_ = fsys.Remove(to)
		return fmt.Errorf("remove %q after a verified copy: %w", from, err)
	}
	return nil
}

// removeEmptyFolders removes folder and its emptied parents, never rootPath itself. Removing a
// folder that still holds anything fails, which is what stops it.
func (q RecycleBin) removeEmptyFolders(folder, rootPath string) {
	root := filepath.Clean(rootPath)
	for folder = filepath.Clean(folder); folder != root && fsx.Within(root, folder); folder = filepath.Dir(folder) {
		if err := q.fsys.Remove(folder); err != nil {
			return
		}
	}
}

// RecycleBinDirectory is where each root keeps its recycle bin; a scan never treats it as library.
const RecycleBinDirectory = "recycle-bin"

// LegacyRecycleBinDirectory is the name before 2026-09-25; files recycled then still live there.
const LegacyRecycleBinDirectory = "quarantine"

// reserveRecycledPath finds an unused path under rootPath's own recycle bin
// directory, dated today -- the same stat-and-retry shape
// store.unusedBackupPath already uses for an identical problem (an unused
// name, not an unused id).
func (q RecycleBin) reserveRecycledPath(rootPath, originalPath string) (string, error) {
	day := q.clock.Now().UTC().Format("2006-01-02")
	dir := filepath.Join(rootPath, RecycleBinDirectory, day)
	if err := q.fsys.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("recycle bin: create %q: %w", dir, err)
	}

	base := filepath.Base(originalPath)
	for attempt := range maxRecycleBinDisambiguation {
		name := base
		if attempt > 0 {
			name = fmt.Sprintf("%d-%s", attempt, base)
		}
		candidate := filepath.Join(dir, name)
		if _, err := q.fsys.Lstat(candidate); errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", fmt.Errorf("recycle bin: check %q: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("recycle bin: %d entries already exist for %q in %q", maxRecycleBinDisambiguation, base, dir)
}

func recycleBinEntryFromRow(row librarydb.RecycleBin) Entry {
	return Entry{
		ID: row.ID, OriginalPath: row.OriginalPath, RecycledPath: row.RecycledPath,
		RootFolderID: int64Ptr(row.RootFolderID), MediaObjectID: int64Ptr(row.MediaObjectID),
		AcquisitionID: int64Ptr(row.AcquisitionID), Reason: Reason(row.Reason),
		SizeBytes: row.SizeBytes, RecycledAt: row.RecycledAt, ExpiresAt: row.ExpiresAt,
		RestoredAt: int64Ptr(row.RestoredAt),
	}
}
