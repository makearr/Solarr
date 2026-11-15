package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf8"

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
	// ReplacedByObjectID is the media_object that took the file's place; nil when none did.
	ReplacedByObjectID *int64
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
	// ReplacedByObjectID is the media_object that took the file's place, so reverting its import can bring this one back.
	ReplacedByObjectID *int64
}

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
	// online says whether a root folder is reachable; nil takes every root for online.
	online func(rootFolderID int64) bool
}

// ErrRootOffline reports an entry whose root folder is not reachable (a disk not mounted, a share down): its file may
// well still be there, so it is neither purged nor forgotten.
var ErrRootOffline = errors.New("recycle bin: the entry's root folder is offline")

// WithOnline returns the bin refusing to purge entries of roots online reports as unreachable.
func (q RecycleBin) WithOnline(online func(rootFolderID int64) bool) RecycleBin {
	q.online = online
	return q
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

	dir, err := q.recycleDirectory(req.RootPath)
	if err != nil {
		return Entry{}, err
	}

	// The row first, and its id names the file: no search for a free name, nothing two recyclers can both pick. A file
	// moved without a row would never expire and no screen would show it.
	now := q.clock.Now()
	var row librarydb.RecycleBin
	err = q.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		var writeErr error
		row, writeErr = queries.RecycleBin(ctx, librarydb.RecycleBinParams{
			OriginalPath: req.OriginalPath, RecycledPath: fmt.Sprintf("pending-%016x", rand.Uint64()),
			RootFolderID: nullInt64(req.RootFolderID), MediaObjectID: nullInt64(req.MediaObjectID),
			AcquisitionID: nullInt64(req.AcquisitionID), Reason: string(req.Reason),
			SizeBytes: info.Size, RecycledAt: now.Unix(), ExpiresAt: now.Add(req.Retain).Unix(),
			ReplacedByMediaObjectID: nullInt64(req.ReplacedByObjectID),
		})
		if writeErr != nil {
			return writeErr
		}
		row.RecycledPath = filepath.Join(dir, fmt.Sprintf("%d-%s", row.ID, recycledBaseName(req.OriginalPath)))
		return queries.SetRecycleBinPath(ctx, librarydb.SetRecycleBinPathParams{RecycledPath: row.RecycledPath, ID: row.ID})
	})
	if err != nil {
		return Entry{}, fmt.Errorf("recycle bin: record %q: %w", req.OriginalPath, err)
	}
	recycledPath := row.RecycledPath

	if err := relocate(q.fsys, req.OriginalPath, recycledPath, info.Size); err != nil {
		_ = q.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			return librarydb.New(tx).DeleteRecycleBinEntry(ctx, row.ID)
		})
		return Entry{}, fmt.Errorf("recycle bin: move %q to %q: %w", req.OriginalPath, recycledPath, err)
	}

	if req.RemoveEmptyFolders {
		q.removeEmptyFolders(filepath.Dir(req.OriginalPath), req.RootPath)
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

// recycleDirectory is today's folder in rootPath's own recycle bin, made if needed.
func (q RecycleBin) recycleDirectory(rootPath string) (string, error) {
	day := q.clock.Now().UTC().Format("2006-01-02")
	dir := filepath.Join(rootPath, RecycleBinDirectory, day)
	if err := q.fsys.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("recycle bin: create %q: %w", dir, err)
	}
	return dir, nil
}

// maxRecycledBaseBytes keeps "<id>-<name>" inside the 255-byte component limit.
const maxRecycledBaseBytes = 120

// recycledBaseName is the file's name cut to fit, its extension kept.
func recycledBaseName(originalPath string) string {
	base := filepath.Base(originalPath)
	if len(base) <= maxRecycledBaseBytes {
		return base
	}
	extension := filepath.Ext(base)
	if len(extension) > 16 {
		extension = ""
	}
	return truncateUTF8(base[:len(base)-len(extension)], maxRecycledBaseBytes-len(extension)) + extension
}

func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

func recycleBinEntryFromRow(row librarydb.RecycleBin) Entry {
	return Entry{
		ID: row.ID, OriginalPath: row.OriginalPath, RecycledPath: row.RecycledPath,
		RootFolderID: int64Ptr(row.RootFolderID), MediaObjectID: int64Ptr(row.MediaObjectID),
		AcquisitionID: int64Ptr(row.AcquisitionID), Reason: Reason(row.Reason),
		SizeBytes: row.SizeBytes, RecycledAt: row.RecycledAt, ExpiresAt: row.ExpiresAt,
		RestoredAt: int64Ptr(row.RestoredAt), ReplacedByObjectID: int64Ptr(row.ReplacedByMediaObjectID),
	}
}
