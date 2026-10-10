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

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/filekind"
	"github.com/makearr/Solarr/internal/platform/fsx"
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

// RegisteredFile is one file RegisterBatch recorded: the media_object it belongs to
// (new or, per ADR-11's quick-hash dedupe, an existing one this call merged into) and
// the library_path row this specific call touched. A caller that must know which path
// was its own -- ImportRevert's own 4.22 fix -- needs both, since two callers can share
// a MediaObjectID while never sharing a LibraryPathID.
type RegisteredFile struct {
	MediaObjectID int64
	LibraryPathID int64
}

// RegisterBatch finds or creates the media_object and library_path row for each file,
// in one transaction (Invariant 2) -- the same bound a scan batch already commits at.
// Results come back in the same order as files.
func (p Physical) RegisterBatch(ctx context.Context, rootFolderID int64, files []PhysicalFile) ([]RegisteredFile, error) {
	registered := make([]RegisteredFile, len(files))
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for index, file := range files {
			result, err := registerOne(ctx, queries, rootFolderID, file)
			if err != nil {
				return fmt.Errorf("library: register %s: %w", file.RelPath, err)
			}
			registered[index] = result
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return registered, nil
}

func registerOne(
	ctx context.Context, queries *librarydb.Queries, rootFolderID int64, file PhysicalFile,
) (RegisteredFile, error) {
	existing, err := queries.GetPathByLocation(ctx, librarydb.GetPathByLocationParams{
		RootFolderID: rootFolderID, RelPath: file.RelPath,
	})
	if err == nil {
		// Re-observing a known path is what proves it is still present (section
		// 10.4), whether or not it was ever marked missing.
		if err := queries.TouchLibraryPath(ctx, librarydb.TouchLibraryPathParams{
			LastSeenAt: file.FirstSeenAt.Unix(), ID: existing.ID,
		}); err != nil {
			return RegisteredFile{}, fmt.Errorf("touch path: %w", err)
		}
		return RegisteredFile{MediaObjectID: existing.MediaObjectID, LibraryPathID: existing.ID}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RegisteredFile{}, fmt.Errorf("look up: %w", err)
	}

	mediaObjectID, err := findOrCreateObject(ctx, queries, file)
	if err != nil {
		return RegisteredFile{}, err
	}

	libraryPathID, err := queries.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
		MediaObjectID: mediaObjectID, RootFolderID: rootFolderID, RelPath: file.RelPath,
		LinkKind: "primary", IsPrimary: 1, CreatedAt: file.FirstSeenAt.Unix(),
		LastSeenAt: file.FirstSeenAt.Unix(),
	})
	if err != nil {
		return RegisteredFile{}, fmt.Errorf("add path: %w", err)
	}
	return RegisteredFile{MediaObjectID: mediaObjectID, LibraryPathID: libraryPathID}, nil
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
		FirstSeenAt: file.FirstSeenAt.Unix(), Kind: sql.NullString{String: string(filekind.Of(file.RelPath)), Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("create media object: %w", err)
	}
	return created, nil
}

// classifyPage is how many media objects one transaction of ClassifyKinds sets (Invariant 2).
const classifyPage = 200

// ClassifyKinds gives every media object still without a kind the kind of its path, a page per transaction, and
// answers how many it set: the rows registered before media_object had a kind (revision 2, D3).
func (p Physical) ClassifyKinds(ctx context.Context) (int, error) {
	classified := 0
	var after int64
	for {
		rows, err := librarydb.New(p.store.Read()).ListUnclassifiedMediaObjects(ctx, librarydb.ListUnclassifiedMediaObjectsParams{
			AfterID: after, PageSize: classifyPage,
		})
		if err != nil || len(rows) == 0 {
			return classified, err
		}
		err = p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for _, row := range rows {
				if err := queries.SetMediaObjectKind(ctx, librarydb.SetMediaObjectKindParams{
					Kind: sql.NullString{String: string(filekind.Of(row.RelPath)), Valid: true}, ID: row.ID,
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return classified, fmt.Errorf("library: classify media objects: %w", err)
		}
		after, classified = rows[len(rows)-1].ID, classified+len(rows)
	}
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

// The probe versions: what a probe read of a file, kept beside it in media_probe so a stale one is found by number.
const (
	// ProbeVersionLyrics read the lyrics language too.
	ProbeVersionLyrics = 2
	// ProbeVersionVideo read a video's picture, subtitle tracks and encoder's CRF too; what the probe reads now.
	ProbeVersionVideo   = 3
	ProbeVersionCurrent = ProbeVersionVideo
)

// ListStaleProbes pages through the files probed before the probe read lyrics languages, oldest id first.
func (p Physical) ListStaleProbes(ctx context.Context, afterID int64, pageSize int64) ([]UnmeasuredFile, error) {
	rows, err := librarydb.New(p.store.Read()).ListStaleProbes(ctx, librarydb.ListStaleProbesParams{
		BelowVersion: ProbeVersionLyrics, AfterID: afterID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list stale probes: %w", err)
	}
	files := make([]UnmeasuredFile, len(rows))
	for index, row := range rows {
		files[index] = UnmeasuredFile{MediaObjectID: row.ID, RootFolderID: row.RootFolderID, RelPath: row.RelPath}
	}
	return files, nil
}

// ListStaleVideoProbes pages through the video files probed before the probe read the picture and subtitle tracks.
func (p Physical) ListStaleVideoProbes(ctx context.Context, afterID int64, pageSize int64) ([]UnmeasuredFile, error) {
	rows, err := librarydb.New(p.store.Read()).ListStaleVideoProbes(ctx, librarydb.ListStaleVideoProbesParams{
		BelowVersion: ProbeVersionVideo, AfterID: afterID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list stale video probes: %w", err)
	}
	files := make([]UnmeasuredFile, len(rows))
	for index, row := range rows {
		files[index] = UnmeasuredFile{MediaObjectID: row.ID, RootFolderID: row.RootFolderID, RelPath: row.RelPath}
	}
	return files, nil
}

// FileOnRoot is one real file currently on a root, for ADR-51's drain --
// the primary path only (see ListPathsForRootWithSize's own doc comment for
// the narrow cross-entry-hardlink gap this leaves).
type FileOnRoot struct {
	MediaObjectID int64
	RelPath       string
	SizeBytes     int64
}

// drainPageSize mirrors the other cursor-paginated readers in this file
// (ADR-22): bounded per page, regardless of how many pages PathsForRoot
// itself walks through in one call.
const drainPageSize = 1000

// PathsForRoot returns every real file on a root folder, size included --
// ADR-51's own drain needs the whole list at once to group and place it,
// unlike ListUnmeasured's own per-page caller; paging internally keeps any
// one query bounded (ADR-22) while still handing the caller a complete
// answer.
func (p Physical) PathsForRoot(ctx context.Context, rootFolderID int64) ([]FileOnRoot, error) {
	var files []FileOnRoot
	afterID := int64(0)
	for {
		rows, err := librarydb.New(p.store.Read()).ListPathsForRootWithSize(ctx, librarydb.ListPathsForRootWithSizeParams{
			RootFolderID: rootFolderID, AfterID: afterID, PageSize: drainPageSize,
		})
		if err != nil {
			return nil, fmt.Errorf("library: paths for root %d: %w", rootFolderID, err)
		}
		for _, row := range rows {
			files = append(files, FileOnRoot{
				MediaObjectID: row.MediaObjectID, RelPath: row.RelPath, SizeBytes: row.SizeBytes,
			})
			afterID = row.MediaObjectID
		}
		if int64(len(rows)) < drainPageSize {
			return files, nil
		}
	}
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

// ErrPathNotFound means the (rootFolderID, relPath) pair Relocate was asked
// to move no longer names a library_path row -- a concurrent change since
// Plan ran (a rescan, another move, a manual edit), not a bug in the caller.
var ErrPathNotFound = errors.New("library: no library_path at the given location")

// Relocate is ADR-51's own RECORD step: media_object never changes
// (Invariant 70), only where it lives. Matched by the old
// (root_folder_id, rel_path) pair, which is exactly what a PlannedMove
// already carries -- no lookup needed before the update.
func (p Physical) Relocate(ctx context.Context, oldRootFolderID int64, oldRelPath string, newRootFolderID int64, newRelPath string) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		affected, err := librarydb.New(tx).RelocateLibraryPath(ctx, librarydb.RelocateLibraryPathParams{
			NewRootFolderID: newRootFolderID, NewRelPath: newRelPath,
			OldRootFolderID: oldRootFolderID, OldRelPath: oldRelPath,
		})
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrPathNotFound
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: relocate %d:%s to %d:%s: %w",
			oldRootFolderID, oldRelPath, newRootFolderID, newRelPath, err)
	}
	return nil
}

// DeleteObject removes a media_object outright, cascading to every
// library_path and desired_item_file row that referenced it. Unlike
// MarkMissing (section 10.4's "a vanished path is MISSING, not deleted,
// until the root is confirmed online"), this is for a caller that already
// knows, with certainty, that the bytes are gone -- a deliberate recycle bin,
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

// DeleteLibraryPathRow removes exactly one library_path row, leaving its media_object
// and every other path referencing it untouched -- the half of ADR-11 case C DeleteObject
// cannot give a caller that must remove only its own copy of shared bytes (4.22's own
// linking audit; see ImportRevert.recycleObject).
func (p Physical) DeleteLibraryPathRow(ctx context.Context, libraryPathID int64) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteLibraryPath(ctx, libraryPathID)
	})
	if err != nil {
		return fmt.Errorf("library: delete path %d: %w", libraryPathID, err)
	}
	return nil
}

// ClaimedAttributesFor answers the claimed half of ADR-39 for one file, when
// there is one -- found is false for a file whose release name RecordClaimed
// (todo 4.2) has not been run against yet, or one that predates it.
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
	// ProbeJSON is what the probe read, kept in media_probe under ProbeVersion.
	ProbeJSON    string
	ProbeVersion int64
}

// RecordMeasured writes ADR-39's measured half for one file: interning claim
// into media_attributes and pointing media_object at it, in one transaction so
// a crash between the two never leaves media_object referencing a row that was
// never actually committed. mismatchJSON is empty when there is no claimed
// side to compare against -- a file whose scan predates RecordClaimed
// (todo 4.2), or whose release name claimed nothing at all.
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
		if scalars.ProbeJSON != "" {
			if err := queries.SetMediaProbe(ctx, librarydb.SetMediaProbeParams{
				MediaObjectID: mediaObjectID, ProbeVersion: scalars.ProbeVersion, ProbedAt: verifiedAt, Json: scalars.ProbeJSON,
			}); err != nil {
				return fmt.Errorf("record probe: %w", err)
			}
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
			VerifiedAt:            sql.NullInt64{Int64: verifiedAt, Valid: true},
			ID:                    mediaObjectID,
		})
	})
}

// RecordClaimed writes ADR-39's claimed half for one file (todo 4.2):
// interning claim into media_attributes and pointing media_object at it, the
// same intern-then-point shape RecordMeasured uses. Unlike RecordMeasured,
// there are no scalar fields and no mismatch to record -- those only mean
// something once a measured claim exists to compare against, and claim here
// comes from the release name, not the file's own bytes.
func (p Physical) RecordClaimed(ctx context.Context, mediaObjectID int64, claim AttributeClaim) error {
	return p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		attrRow, err := queries.InternAttributes(ctx, internParams(claim))
		if err != nil {
			return fmt.Errorf("intern claimed attributes: %w", err)
		}
		return queries.SetClaimedAttributes(ctx, librarydb.SetClaimedAttributesParams{
			ClaimedAttributesID: sql.NullInt64{Int64: attrRow.ID, Valid: true},
			ID:                  mediaObjectID,
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

// RecomputeWorkItems brings every desired item of a work back in line with the files that cover it, after files were
// removed (an import reverted): the counts are derived, and a deleted media_object takes its coverage rows with it.
func (p Physical) RecomputeWorkItems(ctx context.Context, workID int64) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		items, err := queries.ListDesiredItemsForWork(ctx, workID)
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.SatisfactionKind == "SET" {
				if err := recomputeAlbumTx(ctx, queries, item.ID, workID); err != nil {
					return err
				}
				continue
			}
			if err := queries.RecomputePresentCount(ctx, item.ID); err != nil {
				return err
			}
			fresh, err := queries.GetDesiredItem(ctx, item.ID)
			if err != nil {
				return err
			}
			if err := queries.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{
				State: albumState(fresh.RequiredCount, fresh.PresentCount), PresentCount: fresh.PresentCount, ID: item.ID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: recompute items of work %d: %w", workID, err)
	}
	return nil
}

// MergeObjectInto makes the dropped media object's paths further paths of the kept one, each recorded with the way
// it now shares bytes (linkKinds, by path id), and moves what the dropped one covered over to the kept one. Whatever
// the dropped object still had is removed with it: this is for a caller that has already linked every path it wants.
func (p Physical) MergeObjectInto(ctx context.Context, keepID, dropID int64, linkKinds map[int64]string) ([]int64, error) {
	var works []int64
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		for pathID, kind := range linkKinds {
			if _, err := tx.ExecContext(ctx,
				`UPDATE library_path SET media_object_id = ?, link_kind = ?, is_primary = 0 WHERE id = ? AND media_object_id = ?`,
				keepID, kind, pathID, dropID); err != nil {
				return err
			}
		}
		var err error
		if works, err = workIDsOfObject(ctx, tx, dropID); err != nil {
			return err
		}
		for _, statement := range []string{
			`UPDATE OR IGNORE desired_item_file SET media_object_id = ? WHERE media_object_id = ?`,
			`UPDATE OR IGNORE work_file SET media_object_id = ? WHERE media_object_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, statement, keepID, dropID); err != nil {
				return err
			}
		}
		return librarydb.New(tx).DeleteMediaObject(ctx, dropID)
	})
	if err != nil {
		return nil, fmt.Errorf("library: merge object %d into %d: %w", dropID, keepID, err)
	}
	return works, nil
}

func workIDsOfObject(ctx context.Context, tx *sql.Tx, mediaObjectID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT work_id FROM work_file WHERE media_object_id = ?`, mediaObjectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var works []int64
	for rows.Next() {
		var workID int64
		if err := rows.Scan(&workID); err != nil {
			return nil, err
		}
		works = append(works, workID)
	}
	return works, rows.Err()
}
