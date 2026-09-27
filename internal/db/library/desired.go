package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ErrPartNotFound reports a track request naming a media_ref (or, from
// EnsureTrackItemByPart, a part id) with no matching work_part -- section
// 19.6 requires resolving against a real part, never a guessed index.
// Wraps sql.ErrNoRows so apiserver, which cannot import this package, can
// still map it to 404 the same way SetPartMonitored's own cross-work id
// check already does.
var ErrPartNotFound = fmt.Errorf("library: work part not found: %w", sql.ErrNoRows)

// Desired creates and maintains desired_item / desired_item_file rows --
// section 19.6's "wanted, at three levels": SET for an album, SINGLE for a
// track, plus what a scan discovers on disk. Deciding *which* file covers
// *which* part is a media type's own algorithm (ADR-23 -- internal/media/music's
// Align is the first); this type only persists what a caller already decided.
type Desired struct{ store *store.Store }

// NewDesired builds it over an open library store.
func NewDesired(opened *store.Store) Desired { return Desired{store: opened} }

// AlbumRequest identifies, and if needed creates, the album-level SET item for
// a work: edition_id null, media_ref ”. Null EditionID means any pressing
// satisfies; a caller sets it to pin one specific edition.
type AlbumRequest struct {
	WorkID     int64
	EditionID  *int64
	CategoryID int64
	ProfileID  int64
	Monitored  bool
	AddedBy    *int64
}

// EnsureAlbumItem finds the work's album-level SET item, or creates it from
// request -- required_count is always derived from the work's own parts
// (recomputeAlbumTx), never taken on faith from a caller. An item that already
// exists keeps its own recorded policy untouched -- discovery must never
// overwrite what a user (or an earlier discovery pass) already decided, the
// same idempotent shape Invariant 71 requires of a subscription one level up.
func (d Desired) EnsureAlbumItem(ctx context.Context, request AlbumRequest) (librarydb.DesiredItem, error) {
	var item librarydb.DesiredItem
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		existing, err := queries.ListDesiredItemsForWork(ctx, request.WorkID)
		if err != nil {
			return fmt.Errorf("list existing: %w", err)
		}
		for _, candidate := range existing {
			if candidate.SatisfactionKind == "SET" && !candidate.EditionID.Valid && candidate.MediaRef == "" {
				item = candidate
				return nil
			}
		}
		created, err := queries.CreateDesiredItem(ctx, librarydb.CreateDesiredItemParams{
			WorkID: request.WorkID, EditionID: nullInt64(request.EditionID),
			MediaRef: "", MediaSortKey: "",
			CategoryID: request.CategoryID, ProfileID: request.ProfileID,
			Monitored:        boolToInt(request.Monitored),
			SatisfactionKind: "SET",
			RequiredCount:    sql.NullInt64{},
			PresentCount:     0, State: "MISSING",
			Dirty: 1, Generation: 0,
			AddedBy: nullInt64(request.AddedBy),
		})
		if err != nil {
			return fmt.Errorf("create: %w", err)
		}
		if err := recomputeAlbumTx(ctx, queries, created.ID, request.WorkID); err != nil {
			return fmt.Errorf("derive required count: %w", err)
		}
		item, err = queries.GetDesiredItem(ctx, created.ID)
		return err
	})
	if err != nil {
		return librarydb.DesiredItem{}, fmt.Errorf("library: ensure album item: %w", err)
	}
	return item, nil
}

// TrackRequest identifies, and if needed creates, a track-level SINGLE item.
type TrackRequest struct {
	WorkID     int64
	MediaRef   string
	CategoryID int64
	ProfileID  int64
	AddedBy    *int64
}

// EnsureTrackItem resolves request.MediaRef against a real work_part -- never
// a guessed index -- and finds or creates the SINGLE item for it.
func (d Desired) EnsureTrackItem(ctx context.Context, request TrackRequest) (librarydb.DesiredItem, error) {
	var item librarydb.DesiredItem
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		part, err := queries.FindWorkPart(ctx,
			librarydb.FindWorkPartParams{WorkID: request.WorkID, MediaRef: request.MediaRef})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrPartNotFound
			}
			return fmt.Errorf("find part: %w", err)
		}

		existing, err := queries.ListDesiredItemsForWork(ctx, request.WorkID)
		if err != nil {
			return fmt.Errorf("list existing: %w", err)
		}
		for _, candidate := range existing {
			if candidate.SatisfactionKind == "SINGLE" && candidate.MediaRef == request.MediaRef {
				item = candidate
				return nil
			}
		}

		created, err := queries.CreateDesiredItem(ctx, librarydb.CreateDesiredItemParams{
			WorkID: request.WorkID, EditionID: sql.NullInt64{},
			MediaRef: request.MediaRef, MediaSortKey: part.SortKey,
			CategoryID: request.CategoryID, ProfileID: request.ProfileID,
			Monitored:        1,
			SatisfactionKind: "SINGLE",
			RequiredCount:    sql.NullInt64{Int64: 1, Valid: true},
			PresentCount:     0, State: "MISSING",
			Dirty: 1, Generation: 0,
			AddedBy: nullInt64(request.AddedBy),
		})
		if err != nil {
			return fmt.Errorf("create: %w", err)
		}
		item = created
		return nil
	})
	if err != nil {
		return librarydb.DesiredItem{}, fmt.Errorf("library: ensure track item: %w", err)
	}
	return item, nil
}

// EnsureTrackItemByPart resolves partID to its own media_ref -- checking it
// belongs to workID, the same existence/ownership guard SetPartMonitored
// uses -- and finds or creates that track's own SINGLE item. The API-facing
// entry point: a caller has the part id from the tracklist, not the raw
// media_ref EnsureTrackItem itself requires.
func (d Desired) EnsureTrackItemByPart(
	ctx context.Context, workID, partID, categoryID, profileID int64, addedBy *int64,
) (librarydb.DesiredItem, error) {
	part, err := librarydb.New(d.store.Read()).GetWorkPart(ctx, partID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return librarydb.DesiredItem{}, ErrPartNotFound
		}
		return librarydb.DesiredItem{}, fmt.Errorf("library: ensure track item by part: get part: %w", err)
	}
	if part.WorkID != workID {
		return librarydb.DesiredItem{}, ErrPartNotFound
	}
	return d.EnsureTrackItem(ctx, TrackRequest{
		WorkID: workID, MediaRef: part.MediaRef,
		CategoryID: categoryID, ProfileID: profileID, AddedBy: addedBy,
	})
}

// SetItemMonitored toggles an item's own monitored flag -- album or track
// level. Unmonitoring never deletes the row (Invariant 71's shape): a later
// discovery or subscription pass must find it and leave it alone.
func (d Desired) SetItemMonitored(ctx context.Context, itemID int64, monitored bool) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetDesiredItemMonitored(ctx,
			librarydb.SetDesiredItemMonitoredParams{Monitored: boolToInt(monitored), ID: itemID})
	})
	if err != nil {
		return fmt.Errorf("library: set item monitored: %w", err)
	}
	return nil
}

// RecordSearched notes that itemID was searched at, found anything or not.
func (d Desired) RecordSearched(ctx context.Context, itemID, at int64) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RecordItemSearched(ctx,
			librarydb.RecordItemSearchedParams{DesiredItemID: itemID, LastSearchedAt: at})
	})
	if err != nil {
		return fmt.Errorf("library: record item searched: %w", err)
	}
	return nil
}

// UnmonitorItemsLosingFiles unmonitors categoryID's items that hold a file on rootFolderID marked
// missing at missingSince (a library's "Unmonitor Deleted Tracks"), in batches; answers how many.
func (d Desired) UnmonitorItemsLosingFiles(ctx context.Context, categoryID, rootFolderID, missingSince int64) (int, error) {
	total := 0
	for {
		var changed int64
		err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			var batchErr error
			changed, batchErr = librarydb.New(tx).UnmonitorDesiredItemsWithMissingFiles(ctx,
				librarydb.UnmonitorDesiredItemsWithMissingFilesParams{
					CategoryID: categoryID, RootFolderID: rootFolderID,
					MissingSince: sql.NullInt64{Int64: missingSince, Valid: true}, Limit: dirtyBatchSize,
				})
			return batchErr
		})
		if err != nil {
			return total, fmt.Errorf("library: unmonitor items losing files: %w", err)
		}
		if changed == 0 {
			return total, nil
		}
		total += int(changed)
	}
}

// SetPartMonitored toggles one work_part's monitored flag, then recomputes
// every album-level SET item for that work: unmonitoring a bonus track must
// shrink the album's required_count rather than holding it PARTIAL forever
// (section 19.6, Appendix E D3).
func (d Desired) SetPartMonitored(ctx context.Context, workID, partID int64, monitored bool) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		// SetWorkPartMonitored is a bare UPDATE ... WHERE id = ? -- it would
		// silently affect zero rows for a bogus or cross-work id rather than
		// erroring, so existence and ownership are checked here first.
		part, err := queries.GetWorkPart(ctx, partID)
		if err != nil {
			return fmt.Errorf("get part: %w", err)
		}
		if part.WorkID != workID {
			return sql.ErrNoRows
		}
		if err := queries.SetWorkPartMonitored(ctx,
			librarydb.SetWorkPartMonitoredParams{Monitored: boolToInt(monitored), ID: partID}); err != nil {
			return fmt.Errorf("set part monitored: %w", err)
		}
		items, err := queries.ListDesiredItemsForWork(ctx, workID)
		if err != nil {
			return fmt.Errorf("list items: %w", err)
		}
		for _, item := range items {
			if item.SatisfactionKind != "SET" {
				continue
			}
			if err := recomputeAlbumTx(ctx, queries, item.ID, workID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: set part monitored: %w", err)
	}
	return nil
}

// UnfilledParts returns every one of a work's parts not yet covered by
// itemID's desired_item_file rows, in part order -- what a media type's own
// alignment (internal/media/music's Align is the first) should try to match new
// candidates against, so a part a previous batch already covered is never
// reconsidered.
func (d Desired) UnfilledParts(ctx context.Context, itemID, workID int64) ([]librarydb.WorkPart, error) {
	queries := librarydb.New(d.store.Read())
	parts, err := queries.ListWorkParts(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: unfilled parts: list parts: %w", err)
	}
	files, err := queries.ListFilesForDesiredItem(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("library: unfilled parts: list files: %w", err)
	}
	covered := make(map[string]bool, len(files))
	for _, file := range files {
		if file.Role == "primary" {
			covered[file.MediaRef] = true
		}
	}
	unfilled := make([]librarydb.WorkPart, 0, len(parts))
	for _, part := range parts {
		if !covered[part.MediaRef] {
			unfilled = append(unfilled, part)
		}
	}
	return unfilled, nil
}

// ExternalIDsFor returns the given parts' external ids for one provider, keyed
// by part id -- a part with none is simply absent. Kept provider-agnostic
// (ADR-23): a media type names which provider it means (internal/media/music's
// ProviderRecording, for Align's recording-MBID pass).
func (d Desired) ExternalIDsFor(ctx context.Context, parts []librarydb.WorkPart, provider string) (map[int64]string, error) {
	queries := librarydb.New(d.store.Read())
	out := make(map[int64]string, len(parts))
	for _, part := range parts {
		externalIDs, err := queries.ListWorkPartExternalIDs(ctx, part.ID)
		if err != nil {
			return nil, fmt.Errorf("library: external ids for part %d: %w", part.ID, err)
		}
		for _, external := range externalIDs {
			if external.Provider == provider {
				out[part.ID] = external.ExternalID
				break
			}
		}
	}
	return out, nil
}

// AlignedFile is one file a media type's own matcher has already decided
// covers a specific part.
type AlignedFile struct {
	MediaObjectID int64
	MediaRef      string
}

// RecordAlignment attaches every aligned file to itemID (idempotent -- an
// already-attached file is a no-op) and recomputes the item's
// required/present count and state, all in one transaction.
func (d Desired) RecordAlignment(ctx context.Context, itemID, workID int64, files []AlignedFile, at int64) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, file := range files {
			if err := queries.AttachFile(ctx, librarydb.AttachFileParams{
				DesiredItemID: itemID, MediaObjectID: file.MediaObjectID, Role: "primary",
				MediaRef: file.MediaRef, AddedAt: at,
			}); err != nil {
				return fmt.Errorf("attach media object %d: %w", file.MediaObjectID, err)
			}
		}
		return recomputeAlbumTx(ctx, queries, itemID, workID)
	})
	if err != nil {
		return fmt.Errorf("library: record alignment: %w", err)
	}
	return nil
}

// TrackClaim is where a scan or import placed one identified file on its album.
type TrackClaim struct {
	MediaObjectID int64
	Disc, Track   int64
	Title         string
	RecordingMBID string
}

// RecordTrackClaims keeps where each file sits, replacing an earlier claim for the same file.
func (d Desired) RecordTrackClaims(ctx context.Context, claims []TrackClaim) error {
	for chunk := range slices.Chunk(claims, insertChunk) {
		err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for _, claim := range chunk {
				if err := queries.UpsertFileTrackClaim(ctx, librarydb.UpsertFileTrackClaimParams{
					MediaObjectID: claim.MediaObjectID, Disc: claim.Disc, Track: claim.Track,
					Title: claim.Title, RecordingMbid: claim.RecordingMBID,
				}); err != nil {
					return fmt.Errorf("claim for media object %d: %w", claim.MediaObjectID, err)
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("library: record track claims: %w", err)
		}
	}
	return nil
}

// TrackClaimsForWork answers the claims of every file identified as workID.
func (d Desired) TrackClaimsForWork(ctx context.Context, workID int64) ([]TrackClaim, error) {
	rows, err := librarydb.New(d.store.Read()).ListFileTrackClaimsForWork(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: track claims for work %d: %w", workID, err)
	}
	claims := make([]TrackClaim, len(rows))
	for i, row := range rows {
		claims[i] = TrackClaim{
			MediaObjectID: row.MediaObjectID, Disc: row.Disc, Track: row.Track,
			Title: row.Title, RecordingMBID: row.RecordingMbid,
		}
	}
	return claims, nil
}

// AlbumItemsForWork answers the album-level (SET) items of a work, one per category that wants it.
func (d Desired) AlbumItemsForWork(ctx context.Context, workID int64) ([]librarydb.DesiredItem, error) {
	items, err := librarydb.New(d.store.Read()).ListDesiredItemsForWork(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: album items for work %d: %w", workID, err)
	}
	return slices.DeleteFunc(items, func(item librarydb.DesiredItem) bool { return item.SatisfactionKind != "SET" }), nil
}

// FileMembership is one desired_item_file row: which item and role a file
// covers.
type FileMembership struct {
	DesiredItemID int64
	MediaObjectID int64
	Role          string
	MediaRef      string
	AddedAt       int64
}

// ItemsForFile answers every desired_item a file currently satisfies -- what
// a scan needs to recognise bytes it has seen (section 5.2's own doc
// comment on the query), and what a removal needs to know before the file
// is gone.
func (d Desired) ItemsForFile(ctx context.Context, mediaObjectID int64) ([]FileMembership, error) {
	rows, err := librarydb.New(d.store.Read()).ListDesiredItemsForFile(ctx, mediaObjectID)
	if err != nil {
		return nil, fmt.Errorf("library: items for file %d: %w", mediaObjectID, err)
	}
	memberships := make([]FileMembership, len(rows))
	for i, row := range rows {
		memberships[i] = FileMembership{
			DesiredItemID: row.DesiredItemID, MediaObjectID: row.MediaObjectID,
			Role: row.Role, MediaRef: row.MediaRef, AddedAt: row.AddedAt,
		}
	}
	return memberships, nil
}

// FileIdentity is a file's own editorial position -- which work, which
// edition (nil for "any pressing"), and which part -- resolved from its
// desired_item_file coverage. What a rename preview needs to know "which
// track is this" without any media-type knowledge of its own.
type FileIdentity struct {
	WorkID    int64
	EditionID *int64
	MediaRef  string
}

// IdentityForFile answers the editorial identity backing one file, or
// found=false when the file has no desired_item_file coverage at all --
// not yet aligned to a desired_item (section 19.6's own Align), or an
// import that never went through discovery. Only 'primary' coverage
// counts, matching CountPrimaryFiles' own reasoning: a trailer or bonus
// role does not name the file for renaming purposes.
func (d Desired) IdentityForFile(ctx context.Context, mediaObjectID int64) (FileIdentity, bool, error) {
	memberships, err := d.ItemsForFile(ctx, mediaObjectID)
	if err != nil {
		return FileIdentity{}, false, err
	}
	for _, m := range memberships {
		if m.Role != "primary" {
			continue
		}
		item, err := d.Get(ctx, m.DesiredItemID)
		if err != nil {
			return FileIdentity{}, false, fmt.Errorf("library: get desired item %d: %w", m.DesiredItemID, err)
		}
		identity := FileIdentity{WorkID: item.WorkID, MediaRef: m.MediaRef}
		if item.EditionID.Valid {
			id := item.EditionID.Int64
			identity.EditionID = &id
		}
		return identity, true, nil
	}
	return FileIdentity{}, false, nil
}

// Get answers one desired_item's own row, including its WorkID -- needed by
// a caller that only has an item id and must recompute or detach against it.
func (d Desired) Get(ctx context.Context, itemID int64) (librarydb.DesiredItem, error) {
	row, err := librarydb.New(d.store.Read()).GetDesiredItem(ctx, itemID)
	if err != nil {
		return librarydb.DesiredItem{}, fmt.Errorf("library: get desired item %d: %w", itemID, err)
	}
	return row, nil
}

// DetachAndRecompute removes a file from an item under every role listed and
// recomputes required/present count and state in the same transaction -- the
// same attach+recompute shape RecordAlignment already uses for the opposite
// direction. Detaching a role the item never actually had is a no-op, not an
// error, the same idempotence every other repair path in this codebase has.
func (d Desired) DetachAndRecompute(ctx context.Context, itemID, workID, mediaObjectID int64, roles []string) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, role := range roles {
			if err := queries.DetachFile(ctx, librarydb.DetachFileParams{
				DesiredItemID: itemID, MediaObjectID: mediaObjectID, Role: role,
			}); err != nil {
				return fmt.Errorf("detach role %q: %w", role, err)
			}
		}
		return recomputeAlbumTx(ctx, queries, itemID, workID)
	})
	if err != nil {
		return fmt.Errorf("library: detach file %d from item %d: %w", mediaObjectID, itemID, err)
	}
	return nil
}

// RecomputeAlbum re-derives an album item's required_count (from the work's
// currently-monitored parts) and present_count/state (from what
// desired_item_file already covers), and marks it dirty for the planner.
// Idempotent -- safe to call any time either side of that comparison might
// have changed, the same "periodic recompute is the design, not a corner cut"
// shape CategoryCounters and Reconciler already use.
func (d Desired) RecomputeAlbum(ctx context.Context, itemID, workID int64) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return recomputeAlbumTx(ctx, librarydb.New(tx), itemID, workID)
	})
	if err != nil {
		return fmt.Errorf("library: recompute album: %w", err)
	}
	return nil
}

func recomputeAlbumTx(ctx context.Context, queries *librarydb.Queries, itemID, workID int64) error {
	parts, err := queries.ListWorkParts(ctx, workID)
	if err != nil {
		return fmt.Errorf("list parts: %w", err)
	}
	var required sql.NullInt64
	if len(parts) > 0 {
		var monitored int64
		for _, part := range parts {
			if part.Monitored == 1 {
				monitored++
			}
		}
		required = sql.NullInt64{Int64: monitored, Valid: true}
	}
	if err := queries.SetDesiredItemRequiredCount(ctx,
		librarydb.SetDesiredItemRequiredCountParams{RequiredCount: required, ID: itemID}); err != nil {
		return fmt.Errorf("set required count: %w", err)
	}
	if err := queries.RecomputePresentCount(ctx, itemID); err != nil {
		return fmt.Errorf("recompute present count: %w", err)
	}
	item, err := queries.GetDesiredItem(ctx, itemID)
	if err != nil {
		return fmt.Errorf("get item: %w", err)
	}
	return queries.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{
		State: albumState(item.RequiredCount, item.PresentCount), PresentCount: item.PresentCount, ID: itemID,
	})
}

// IncompleteItem is one monitored desired_item still short of its own
// required_count -- library health's own "missing tracks" list (todo 4.8).
type IncompleteItem struct {
	ID                          int64
	WorkID                      int64
	Title                       string
	CategoryID                  int64
	State                       string
	RequiredCount, PresentCount int64
}

// CountIncomplete answers how many monitored items are MISSING or PARTIAL --
// library health's own "missing tracks" figure (todo 4.8), a live query
// (desired_by_category's own index) rather than a cached counter: unlike
// categorycounts.go's own per-category "missing"/"partial" counters, nothing
// today reconciles a library-wide sum of those into the "library" counter
// scope dashboard.go's own libraryCounts already reads a "missing" key from
// -- a pre-existing gap (that dashboard figure has always read zero),
// noted rather than fixed here since fixing it is categorycounts.go's own
// job, not this one's. categoryID zero means every category.
func (d Desired) CountIncomplete(ctx context.Context, categoryID int64) (int64, error) {
	rows, err := librarydb.New(d.store.Read()).CountDesiredItemsByState(ctx, categoryID)
	if err != nil {
		return 0, fmt.Errorf("library: count desired items by state: %w", err)
	}
	var total int64
	for _, row := range rows {
		if row.State == "MISSING" || row.State == "PARTIAL" {
			total += row.Total
		}
	}
	return total, nil
}

// IncompleteItems pages through every monitored MISSING/PARTIAL item, oldest id first. A zero
// categoryID means every category, a zero personID every artist.
func (d Desired) IncompleteItems(ctx context.Context, categoryID, personID, afterID int64, pageSize int64) ([]IncompleteItem, error) {
	rows, err := librarydb.New(d.store.Read()).ListIncompleteDesiredItems(ctx, librarydb.ListIncompleteDesiredItemsParams{
		CategoryID: categoryID, PersonID: personID, AfterID: afterID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list incomplete desired items: %w", err)
	}
	items := make([]IncompleteItem, len(rows))
	for index, row := range rows {
		items[index] = IncompleteItem{
			ID: row.ID, WorkID: row.WorkID, Title: row.CanonicalTitle, CategoryID: row.CategoryID,
			State: row.State, RequiredCount: row.RequiredCount.Int64, PresentCount: row.PresentCount,
		}
	}
	return items, nil
}

// albumState derives MISSING/PARTIAL/COMPLETE from present against required.
// UPGRADABLE is acquisition's own state to set (stage 5); nothing here ever
// produces it.
func albumState(required sql.NullInt64, present int64) string {
	switch {
	case present <= 0:
		return "MISSING"
	case required.Valid && present >= required.Int64:
		return "COMPLETE"
	default:
		return "PARTIAL"
	}
}
