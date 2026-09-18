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
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Reason says why a file was quarantined (section 5.8's quarantine.reason
// column).
type Reason string

// The five reasons quarantine.reason accepts.
const (
	ReasonRejectedWorse Reason = "rejected_worse"
	ReasonReplaced      Reason = "replaced"
	ReasonJunk          Reason = "junk"
	ReasonUserDelete    Reason = "user_delete"
	ReasonFailedImport  Reason = "failed_import"
)

// ErrCrossDevice reports that the source and its quarantine directory turned
// out to be on different filesystems. Quarantine never falls back to a copy
// here (unlike ADR-11's import linking) -- section 10.8 is explicit that this
// case deletes the file directly and says so, rather than silently copying
// it, because a cross-filesystem quarantine move stops being a cheap rename
// and starts being real, unbounded I/O on the path meant to be safe and fast.
var ErrCrossDevice = errors.New("quarantine: source and quarantine directory are on different filesystems; deleted directly instead")

// ErrAlreadyRestored reports that a quarantine entry was already restored
// (or does not exist), so a second restore is refused rather than silently
// re-running.
var ErrAlreadyRestored = errors.New("quarantine: entry already restored or does not exist")

// ErrRestoreConflict reports that the original path is occupied by something
// else now -- something has replaced whatever was quarantined, and restoring
// would silently overwrite it (section 10.8: "surfaces the conflict rather
// than overwriting").
var ErrRestoreConflict = errors.New("quarantine: original path already exists")

// Entry is one quarantine row.
type Entry struct {
	ID             int64
	OriginalPath   string
	QuarantinePath string
	RootFolderID   *int64
	MediaObjectID  *int64
	AcquisitionID  *int64
	Reason         Reason
	SizeBytes      int64
	QuarantinedAt  int64
	ExpiresAt      int64
	RestoredAt     *int64
}

// PutRequest is one file to quarantine.
type PutRequest struct {
	// RootPath is the root folder's own absolute filesystem path -- quarantine
	// lives on the same filesystem as its source, one directory per root
	// (section 10.8), so the caller resolves which root this file is under.
	RootPath      string
	OriginalPath  string
	RootFolderID  *int64
	MediaObjectID *int64
	AcquisitionID *int64
	Reason        Reason
	// Retain is how long this entry survives before Sweep purges it -- the
	// caller's own config.yaml quarantine.retain (section 10.8), passed in
	// rather than read from settings.db by this type.
	Retain time.Duration
}

// maxQuarantineDisambiguation caps the search for an unused quarantine path,
// the same bound store.unusedBackupPath already uses for the identical
// problem.
const maxQuarantineDisambiguation = 100

// Quarantine is section 10.8: nothing is deleted directly. Every destructive
// operation moves a file into a quarantine directory first -- same
// filesystem as its source, time- and space-bounded, restorable with its
// provenance intact -- and only a later Sweep (or a manual Purge) removes
// the bytes for good.
//
// It does not read settings.db itself (ADR-25's own database split, and the
// same separation internal/moving/placement keeps from internal/db/settings) -- a
// caller resolves a root folder's path and the retention policy in force
// and passes both in.
type Quarantine struct {
	store *store.Store
	fsys  fsx.FS
	clock clock.Clock
}

// NewQuarantine builds it over an open library store.
func NewQuarantine(opened *store.Store, fsys fsx.FS, cl clock.Clock) Quarantine {
	return Quarantine{store: opened, fsys: fsys, clock: cl}
}

// Put moves one file into quarantine and records it. On ErrCrossDevice the
// file is already gone (deleted directly, per this type's own doc comment)
// -- the caller must not retry or treat it as still present.
func (q Quarantine) Put(ctx context.Context, req PutRequest) (Entry, error) {
	info, err := q.fsys.Stat(req.OriginalPath)
	if err != nil {
		return Entry{}, fmt.Errorf("quarantine: stat %q: %w", req.OriginalPath, err)
	}

	quarantinePath, err := q.reserveQuarantinePath(req.RootPath, req.OriginalPath)
	if err != nil {
		return Entry{}, err
	}

	if err := q.fsys.Rename(req.OriginalPath, quarantinePath); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			if rmErr := q.fsys.Remove(req.OriginalPath); rmErr != nil {
				return Entry{}, fmt.Errorf(
					"quarantine: %q is on a different filesystem and could not be deleted either: %w",
					req.OriginalPath, rmErr)
			}
			return Entry{}, fmt.Errorf("%w: %s", ErrCrossDevice, req.OriginalPath)
		}
		return Entry{}, fmt.Errorf("quarantine: move %q to %q: %w", req.OriginalPath, quarantinePath, err)
	}

	now := q.clock.Now()
	var row librarydb.Quarantine
	err = q.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var writeErr error
		row, writeErr = librarydb.New(tx).Quarantine(ctx, librarydb.QuarantineParams{
			OriginalPath: req.OriginalPath, QuarantinePath: quarantinePath,
			RootFolderID: nullInt64(req.RootFolderID), MediaObjectID: nullInt64(req.MediaObjectID),
			AcquisitionID: nullInt64(req.AcquisitionID), Reason: string(req.Reason),
			SizeBytes: info.Size, QuarantinedAt: now.Unix(), ExpiresAt: now.Add(req.Retain).Unix(),
		})
		return writeErr
	})
	if err != nil {
		// The move already happened -- rolling it back would leave the caller
		// worse off if the second rename also fails, and a file sitting in
		// quarantine with no row is exactly what a startup orphan sweep
		// (a documented follow-up) exists to reconcile, not this call.
		return Entry{}, fmt.Errorf(
			"quarantine: moved %q to %q but failed to record it: %w", req.OriginalPath, quarantinePath, err)
	}
	return quarantineEntryFromRow(row), nil
}

// reserveQuarantinePath finds an unused path under rootPath's own quarantine
// directory, dated today -- the same stat-and-retry shape
// store.unusedBackupPath already uses for an identical problem (an unused
// name, not an unused id).
func (q Quarantine) reserveQuarantinePath(rootPath, originalPath string) (string, error) {
	day := q.clock.Now().UTC().Format("2006-01-02")
	dir := filepath.Join(rootPath, "quarantine", day)
	if err := q.fsys.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("quarantine: create %q: %w", dir, err)
	}

	base := filepath.Base(originalPath)
	for attempt := range maxQuarantineDisambiguation {
		name := base
		if attempt > 0 {
			name = fmt.Sprintf("%d-%s", attempt, base)
		}
		candidate := filepath.Join(dir, name)
		if _, err := q.fsys.Stat(candidate); errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", fmt.Errorf("quarantine: check %q: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("quarantine: %d entries already exist for %q in %q", maxQuarantineDisambiguation, base, dir)
}

func quarantineEntryFromRow(row librarydb.Quarantine) Entry {
	return Entry{
		ID: row.ID, OriginalPath: row.OriginalPath, QuarantinePath: row.QuarantinePath,
		RootFolderID: int64Ptr(row.RootFolderID), MediaObjectID: int64Ptr(row.MediaObjectID),
		AcquisitionID: int64Ptr(row.AcquisitionID), Reason: Reason(row.Reason),
		SizeBytes: row.SizeBytes, QuarantinedAt: row.QuarantinedAt, ExpiresAt: row.ExpiresAt,
		RestoredAt: int64Ptr(row.RestoredAt),
	}
}
