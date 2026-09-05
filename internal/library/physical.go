package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// quickHashSampleBytes is how much of a file's head and tail the quick hash reads --
// section 10.4's "stat verify" layer, cheap enough to run on every newly seen path,
// unlike the exact oshash a background job computes later (ADR-35).
const quickHashSampleBytes = 65536

// QuickHash is media_object.quick_hash: size plus a head and tail sample. It is not a
// content hash -- two files differing only in the middle would collide -- which is the
// point: it is cheap identity, not proof, and a hash collision only costs a wasted
// dedupe lookup rather than a wrong one (size is checked too).
func QuickHash(fsys fsx.FS, absolutePath string, size int64) (string, error) {
	file, err := fsys.Open(absolutePath)
	if err != nil {
		return "", fmt.Errorf("library: quick hash %s: %w", absolutePath, err)
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	hash.Write(fmt.Appendf(nil, "%d:", size))

	head := make([]byte, min(int64(quickHashSampleBytes), size))
	if _, err := io.ReadFull(file, head); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("library: quick hash %s: %w", absolutePath, err)
	}
	hash.Write(head)

	if size > quickHashSampleBytes {
		tail := make([]byte, quickHashSampleBytes)
		if _, err := file.ReadAt(tail, size-quickHashSampleBytes); err != nil {
			return "", fmt.Errorf("library: quick hash %s: %w", absolutePath, err)
		}
		hash.Write(tail)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// PhysicalFile is one file a scan found, with its quick hash already computed --
// Register never reads bytes itself, since a file read cannot happen while holding
// the write transaction it runs in (Invariant 2).
type PhysicalFile struct {
	RelPath     string
	SizeBytes   int64
	QuickHash   string
	FirstSeenAt time.Time
}

// Physical records what a scan found: media_object and library_path (section 5.2).
// Deciding what a file *is* belongs to a media type's Identifier chain (ADR-24); this
// only records that the bytes exist.
type Physical struct{ store *store.Store }

// NewPhysical builds it over an open library store.
func NewPhysical(opened *store.Store) Physical { return Physical{store: opened} }

// RegisterBatch finds or creates the media_object and library_path row for each file,
// in one transaction (Invariant 2) -- the same bound a scan batch already commits at.
// Results come back in the same order as files.
func (p Physical) RegisterBatch(ctx context.Context, rootFolderID int64, files []PhysicalFile) ([]int64, error) {
	ids := make([]int64, len(files))
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for index, file := range files {
			id, err := registerOne(ctx, queries, rootFolderID, file)
			if err != nil {
				return fmt.Errorf("library: register %s: %w", file.RelPath, err)
			}
			ids[index] = id
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func registerOne(
	ctx context.Context, queries *librarydb.Queries, rootFolderID int64, file PhysicalFile,
) (int64, error) {
	existing, err := queries.GetPathByLocation(ctx, librarydb.GetPathByLocationParams{
		RootFolderID: rootFolderID, RelPath: file.RelPath,
	})
	if err == nil {
		// Re-observing a known path is what proves it is still present (section
		// 10.4), whether or not it was ever marked missing.
		if err := queries.TouchLibraryPath(ctx, librarydb.TouchLibraryPathParams{
			LastSeenAt: file.FirstSeenAt.Unix(), ID: existing.ID,
		}); err != nil {
			return 0, fmt.Errorf("touch path: %w", err)
		}
		return existing.MediaObjectID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("look up: %w", err)
	}

	mediaObjectID, err := findOrCreateObject(ctx, queries, file)
	if err != nil {
		return 0, err
	}

	if _, err := queries.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
		MediaObjectID: mediaObjectID, RootFolderID: rootFolderID, RelPath: file.RelPath,
		LinkKind: "primary", IsPrimary: 1, CreatedAt: file.FirstSeenAt.Unix(),
		LastSeenAt: file.FirstSeenAt.Unix(),
	}); err != nil {
		return 0, fmt.Errorf("add path: %w", err)
	}
	return mediaObjectID, nil
}

// findOrCreateObject reuses an existing media_object sharing this quick hash and size
// -- a second path to bytes already known, such as a hardlink -- rather than creating
// a duplicate (ADR-11: "cross-entry: hardlink, else the user picks").
func findOrCreateObject(ctx context.Context, queries *librarydb.Queries, file PhysicalFile) (int64, error) {
	matches, err := queries.FindMediaObjectsByQuickHash(ctx, file.QuickHash)
	if err != nil {
		return 0, fmt.Errorf("find by quick hash: %w", err)
	}
	for _, match := range matches {
		if match.SizeBytes == file.SizeBytes {
			return match.ID, nil
		}
	}

	created, err := queries.CreateMediaObject(ctx, librarydb.CreateMediaObjectParams{
		SizeBytes: file.SizeBytes, QuickHash: file.QuickHash,
		FirstSeenAt: file.FirstSeenAt.Unix(),
	})
	if err != nil {
		return 0, fmt.Errorf("create media object: %w", err)
	}
	return created, nil
}

// UnmeasuredFile is one media_object still waiting for ADR-39's measured half,
// with the primary path a probe needs to actually read it.
type UnmeasuredFile struct {
	MediaObjectID int64
	RootFolderID  int64
	RelPath       string
}

// ListUnmeasured pages through every media_object with no measured_attributes_id
// yet, oldest id first -- the same resumable-by-cursor shape ListUnfingerprinted
// already uses (ADR-35), so a file that fails its probe this run does not block
// the cursor from reaching what comes after it.
func (p Physical) ListUnmeasured(ctx context.Context, afterID int64, pageSize int64) ([]UnmeasuredFile, error) {
	rows, err := librarydb.New(p.store.Read()).ListUnmeasured(ctx, librarydb.ListUnmeasuredParams{
		AfterID: afterID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list unmeasured: %w", err)
	}
	files := make([]UnmeasuredFile, len(rows))
	for index, row := range rows {
		files[index] = UnmeasuredFile{
			MediaObjectID: row.ID, RootFolderID: row.RootFolderID, RelPath: row.RelPath,
		}
	}
	return files, nil
}

// Exists reports whether a media_object id is real -- an API layer's own
// existence check before an action like RemoveAndBlock, which does not
// itself fail on an unknown id (a Reopen-only or Block-only request simply
// finds nothing to do).
func (p Physical) Exists(ctx context.Context, mediaObjectID int64) (bool, error) {
	_, err := librarydb.New(p.store.Read()).GetMediaObject(ctx, mediaObjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("library: check object %d exists: %w", mediaObjectID, err)
	}
	return true, nil
}

// PathsForObject answers every library_path row for one media_object,
// primary first -- ADR-11 cross-entry hardlinking means one set of bytes can
// have more than one path, which a caller removing a file everywhere those
// bytes exist (ADR-53's own "remove and block") needs to know about rather
// than assuming a single location.
func (p Physical) PathsForObject(ctx context.Context, mediaObjectID int64) ([]librarydb.LibraryPath, error) {
	rows, err := librarydb.New(p.store.Read()).ListPathsForObject(ctx, mediaObjectID)
	if err != nil {
		return nil, fmt.Errorf("library: paths for object %d: %w", mediaObjectID, err)
	}
	return rows, nil
}

// DeleteObject removes a media_object outright, cascading to every
// library_path and desired_item_file row that referenced it. Unlike
// MarkMissing (section 10.4's "a vanished path is MISSING, not deleted,
// until the root is confirmed online"), this is for a caller that already
// knows, with certainty, that the bytes are gone -- a deliberate quarantine,
// not a scan's own uncertainty about a path it has not looked at again yet.
func (p Physical) DeleteObject(ctx context.Context, mediaObjectID int64) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteMediaObject(ctx, mediaObjectID)
	})
	if err != nil {
		return fmt.Errorf("library: delete object %d: %w", mediaObjectID, err)
	}
	return nil
}

// ClaimedAttributesFor answers the claimed half of ADR-39 for one file, when
// there is one -- the claimed side is not wired up from a release name yet
// (todo 4.2's own remaining item), so found is false for every file today,
// but the read path exists and is exercised so that wiring is a producer
// change only, not a new consumer too.
func (p Physical) ClaimedAttributesFor(ctx context.Context, mediaObjectID int64) (AttributeClaim, bool, error) {
	queries := librarydb.New(p.store.Read())
	object, err := queries.GetMediaObject(ctx, mediaObjectID)
	if err != nil {
		return AttributeClaim{}, false, fmt.Errorf("library: get media object: %w", err)
	}
	if !object.ClaimedAttributesID.Valid {
		return AttributeClaim{}, false, nil
	}
	row, err := queries.GetAttributes(ctx, object.ClaimedAttributesID.Int64)
	if err != nil {
		return AttributeClaim{}, false, fmt.Errorf("library: get claimed attributes: %w", err)
	}
	return attributeClaimFromRow(row), true, nil
}

// MeasuredScalars are the per-file facts media_object stores directly rather
// than through the interned media_attributes row -- unique per file, so
// interning them would give one attributes row per object (media_object's own
// comment on why).
type MeasuredScalars struct {
	DurationMs                                 int64
	Width, Height                              int
	VideoBitrate, AudioBitrate, OverallBitrate int64
	ProbeJSON                                  string
}

// RecordMeasured writes ADR-39's measured half for one file: interning claim
// into media_attributes and pointing media_object at it, in one transaction so
// a crash between the two never leaves media_object referencing a row that was
// never actually committed. mismatchJSON is empty when there is no claimed
// side yet to compare against (todo 4.2: claimed-side wiring from a release
// name is not built yet, so this is the common case today).
func (p Physical) RecordMeasured(
	ctx context.Context, mediaObjectID int64, claim AttributeClaim, scalars MeasuredScalars,
	mismatchJSON string, verifiedAt int64,
) error {
	return p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		attrRow, err := queries.InternAttributes(ctx, internParams(claim))
		if err != nil {
			return fmt.Errorf("intern measured attributes: %w", err)
		}
		return queries.SetMeasuredAttributes(ctx, librarydb.SetMeasuredAttributesParams{
			MeasuredAttributesID:  sql.NullInt64{Int64: attrRow.ID, Valid: true},
			AttributeMismatchJson: nullString(mismatchJSON),
			DurationMs:            sql.NullInt64{Int64: scalars.DurationMs, Valid: scalars.DurationMs > 0},
			Width:                 sql.NullInt64{Int64: int64(scalars.Width), Valid: scalars.Width > 0},
			Height:                sql.NullInt64{Int64: int64(scalars.Height), Valid: scalars.Height > 0},
			VideoBitrate:          sql.NullInt64{Int64: scalars.VideoBitrate, Valid: scalars.VideoBitrate > 0},
			AudioBitrate:          sql.NullInt64{Int64: scalars.AudioBitrate, Valid: scalars.AudioBitrate > 0},
			OverallBitrate:        sql.NullInt64{Int64: scalars.OverallBitrate, Valid: scalars.OverallBitrate > 0},
			ProbeJson:             nullString(scalars.ProbeJSON),
			VerifiedAt:            sql.NullInt64{Int64: verifiedAt, Valid: true},
			ID:                    mediaObjectID,
		})
	})
}

// MarkMissing marks every library_path in rootFolderID not observed since before as
// missing (section 10.4). Callers must only pass before from a completed *full*
// sweep's start time -- an incremental sweep can skip whole directories, so a path it
// never looked at is not evidence of absence.
func (p Physical) MarkMissing(ctx context.Context, rootFolderID int64, before time.Time) (int, error) {
	beforeUnix := before.Unix()
	total := 0
	for {
		var changed int64
		err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			var batchErr error
			changed, batchErr = librarydb.New(tx).MarkLibraryPathsMissing(ctx, librarydb.MarkLibraryPathsMissingParams{
				MissingSince: sql.NullInt64{Int64: beforeUnix, Valid: true},
				RootFolderID: rootFolderID, LastSeenAt: beforeUnix, Limit: dirtyBatchSize,
			})
			return batchErr
		})
		if err != nil {
			return total, fmt.Errorf("library: mark missing: %w", err)
		}
		// Each batch narrows what the next one matches, so a batch that changes
		// nothing means nothing is left to mark.
		if changed == 0 {
			return total, nil
		}
		total += int(changed)
	}
}
