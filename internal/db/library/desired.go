package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ErrPartNotFound is a track request names no real work_part (section 19.6, never a guessed index); wraps sql.ErrNoRows so apiserver can map it to 404.
var ErrPartNotFound = fmt.Errorf("library: work part not found: %w", sql.ErrNoRows)

// Desired maintains desired_item and desired_item_file rows (section 19.6: SET, SINGLE and discovered); which file covers which part is the media type's algorithm (ADR-23), this type persists the decision.
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
	// RequiredWithoutParts is how many files the work needs while it has no parts (a movie: 1); 0 leaves it unknown
	// until its parts are known (an album before its tracklist).
	RequiredWithoutParts int64
}

// EnsureAlbumItem finds or creates the work's SET item; required_count is derived from the parts, and an existing item keeps its policy so discovery never overwrites a decision (Invariant 71).
func (d Desired) EnsureAlbumItem(ctx context.Context, request AlbumRequest) (librarydb.DesiredItem, error) {
	var item librarydb.DesiredItem
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		existing, err := queries.ListDesiredItemsForWorkInCategory(ctx, librarydb.ListDesiredItemsForWorkInCategoryParams{WorkID: request.WorkID, CategoryID: request.CategoryID})
		if err != nil {
			return fmt.Errorf("list existing: %w", err)
		}
		for _, candidate := range existing {
			if candidate.SatisfactionKind == "SET" && !candidate.EditionID.Valid && candidate.MediaRef == "" {
				item = candidate
				return nil
			}
		}
		created, err := createDesiredItem(ctx, queries, librarydb.CreateDesiredItemParams{
			WorkID: request.WorkID, EditionID: nullInt64(request.EditionID),
			MediaRef: "", MediaSortKey: "",
			CategoryID: request.CategoryID, ProfileID: request.ProfileID,
			Monitored:        boolToInt(request.Monitored),
			SatisfactionKind: "SET",
			RequiredCount:    sql.NullInt64{Int64: request.RequiredWithoutParts, Valid: request.RequiredWithoutParts > 0},
			PresentCount:     0, State: "MISSING",
			Dirty: 1, Generation: 0,
			AddedBy: nullInt64(request.AddedBy),
		})
		if err != nil {
			return fmt.Errorf("create: %w", err)
		}
		if err := recomputeAlbumTx(ctx, queries, created.ID, request.WorkID, d.store.Now()); err != nil {
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

		existing, err := queries.ListDesiredItemsForWorkInCategory(ctx, librarydb.ListDesiredItemsForWorkInCategoryParams{WorkID: request.WorkID, CategoryID: request.CategoryID})
		if err != nil {
			return fmt.Errorf("list existing: %w", err)
		}
		for _, candidate := range existing {
			if candidate.SatisfactionKind == "SINGLE" && candidate.MediaRef == request.MediaRef {
				item = candidate
				return nil
			}
		}

		created, err := createDesiredItem(ctx, queries, librarydb.CreateDesiredItemParams{
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

// EnsureTrackItemByPart resolves a part id to its media_ref after checking it belongs to workID, then finds or creates the SINGLE item; the API-facing entry point.
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

// UnmonitorWork stops every item of the work being searched for, ahead of removing it.
func (d Desired) UnmonitorWork(ctx context.Context, workID int64) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).UnmonitorItemsOfWork(ctx, workID)
	})
	if err != nil {
		return fmt.Errorf("library: unmonitor work %d: %w", workID, err)
	}
	return nil
}

// RecordSearched notes that itemID was searched at, found anything or not, and has the planner
// work out when to search it next.
func (d Desired) RecordSearched(ctx context.Context, itemID, at int64) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.RecordItemSearched(ctx,
			librarydb.RecordItemSearchedParams{DesiredItemID: itemID, LastSearchedAt: at}); err != nil {
			return err
		}
		return queries.MarkDesiredItemDirty(ctx, itemID)
	})
	if err != nil {
		return fmt.Errorf("library: record item searched: %w", err)
	}
	return nil
}

// RecordSearchUnanswered notes that itemID's search at was never answered (every indexer failed, or none was asked): the
// search count stays, and the item is due again retryIn from now rather than after its backoff.
func (d Desired) RecordSearchUnanswered(ctx context.Context, itemID, at int64, backoff func(searchCount int64) time.Duration, retryIn time.Duration) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		var count int64
		entry, err := queries.GetHuntEntry(ctx, itemID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			count = entry.SearchCount
		}
		due := at
		if count > 0 {
			due = at - int64((backoff(count)-retryIn)/time.Second)
		}
		if err := queries.RecordItemSearchUnanswered(ctx,
			librarydb.RecordItemSearchUnansweredParams{DesiredItemID: itemID, LastSearchedAt: due}); err != nil {
			return err
		}
		return queries.MarkDesiredItemDirty(ctx, itemID)
	})
	if err != nil {
		return fmt.Errorf("library: record search unanswered: %w", err)
	}
	return nil
}

// RecordDownloadFailed caps itemID's search backoff at a day from now on.
func (d Desired) RecordDownloadFailed(ctx context.Context, itemID int64) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RecordItemDownloadFailed(ctx, itemID)
	})
	if err != nil {
		return fmt.Errorf("library: record download failed: %w", err)
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

// SetPartMonitored toggles a part and recomputes the work's album items, so unmonitoring a bonus track shrinks required_count instead of leaving it PARTIAL (section 19.6, D3).
func (d Desired) SetPartMonitored(ctx context.Context, workID, partID int64, monitored bool) error {
	return d.SetPartsMonitored(ctx, workID, []int64{partID}, monitored)
}

// partsPerWrite bounds the parts one write transaction changes (ADR-3's 200-row chunk).
const partsPerWrite = 200

// SetPartsMonitored is SetPartMonitored for many parts of one work: one transaction per chunk, and each affected item is
// recomputed once, not once per part. Items the parts do not belong to are left alone.
func (d Desired) SetPartsMonitored(ctx context.Context, workID int64, partIDs []int64, monitored bool) error {
	for start := 0; start < len(partIDs); start += partsPerWrite {
		chunk := partIDs[start:min(start+partsPerWrite, len(partIDs))]
		err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			refs := make([]string, 0, len(chunk))
			// One read of the work's parts answers every id of the chunk, instead of one read per part.
			own, err := queries.ListWorkParts(ctx, workID)
			if err != nil {
				return fmt.Errorf("list parts: %w", err)
			}
			ownByID := make(map[int64]librarydb.WorkPart, len(own))
			for _, part := range own {
				ownByID[part.ID] = part
			}
			for _, partID := range chunk {
				// SetWorkPartMonitored is a bare UPDATE ... WHERE id = ? -- it would silently affect zero rows for a
				// bogus or cross-work id rather than erroring, so existence and ownership are checked here first.
				part, found := ownByID[partID]
				if !found {
					return sql.ErrNoRows
				}
				if err := queries.SetWorkPartMonitored(ctx,
					librarydb.SetWorkPartMonitoredParams{Monitored: boolToInt(monitored), ID: partID}); err != nil {
					return fmt.Errorf("set part monitored: %w", err)
				}
				refs = append(refs, part.MediaRef)
			}
			items, err := queries.ListDesiredItemsForWork(ctx, workID)
			if err != nil {
				return fmt.Errorf("list items: %w", err)
			}
			for _, item := range items {
				if item.SatisfactionKind != "SET" || !slices.ContainsFunc(refs, func(ref string) bool { return partInScope(ref, item.MediaRef) }) {
					continue
				}
				if item.MediaRef != "" {
					if err := syncSeasonMonitoring(ctx, queries, workID, item); err != nil {
						return err
					}
				}
				if err := recomputeAlbumTx(ctx, queries, item.ID, workID, d.store.Now()); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("library: set part monitored: %w", err)
		}
	}
	return nil
}

// UnmonitorPartsLosingFiles unmonitors the parts of categoryID whose only files, on rootFolderID, were marked missing
// at missingSince (Sonarr's "Unmonitor Deleted Episodes": a file deleted on purpose is not fetched again), and answers
// how many.
func (d Desired) UnmonitorPartsLosingFiles(ctx context.Context, categoryID, rootFolderID, missingSince int64) (int, error) {
	total := 0
	var afterID int64
	for {
		page, err := librarydb.New(d.store.Read()).ListMonitoredPartsLosingFiles(ctx, librarydb.ListMonitoredPartsLosingFilesParams{
			CategoryID: categoryID, RootFolderID: rootFolderID,
			MissingSince: sql.NullInt64{Int64: missingSince, Valid: true}, AfterID: afterID, PageSize: dirtyBatchSize,
		})
		if err != nil {
			return total, fmt.Errorf("library: parts losing files: %w", err)
		}
		// A work's parts are unmonitored together, so its items are recomputed once for the page.
		byWork := map[int64][]int64{}
		var works []int64
		for _, row := range page {
			afterID = row.PartID
			if _, seen := byWork[row.WorkID]; !seen {
				works = append(works, row.WorkID)
			}
			byWork[row.WorkID] = append(byWork[row.WorkID], row.PartID)
		}
		for _, workID := range works {
			if err := d.SetPartsMonitored(ctx, workID, byWork[workID], false); err != nil {
				return total, err
			}
			total += len(byWork[workID])
		}
		if len(page) < dirtyBatchSize {
			return total, nil
		}
	}
}

// UnmonitorWorksLosingFiles unmonitors the whole-work items (a movie's) of categoryID whose only files, on rootFolderID,
// were marked missing at missingSince, and answers how many.
func (d Desired) UnmonitorWorksLosingFiles(ctx context.Context, categoryID, rootFolderID, missingSince int64) (int, error) {
	total := 0
	var afterID int64
	for {
		page, err := librarydb.New(d.store.Read()).ListMonitoredItemsLosingFiles(ctx, librarydb.ListMonitoredItemsLosingFilesParams{
			CategoryID: categoryID, RootFolderID: rootFolderID,
			MissingSince: sql.NullInt64{Int64: missingSince, Valid: true}, AfterID: afterID, PageSize: dirtyBatchSize,
		})
		if err != nil {
			return total, fmt.Errorf("library: items losing files: %w", err)
		}
		for _, itemID := range page {
			afterID = itemID
			if err := d.SetItemMonitored(ctx, itemID, false); err != nil {
				return total, err
			}
			total++
		}
		if len(page) < dirtyBatchSize {
			return total, nil
		}
	}
}

// syncSeasonMonitoring makes a season item monitored exactly when one of its episodes is, so wanting one episode of an
// unmonitored season is not silently ignored.
func syncSeasonMonitoring(ctx context.Context, queries *librarydb.Queries, workID int64, item librarydb.DesiredItem) error {
	parts, err := queries.ListWorkParts(ctx, workID)
	if err != nil {
		return fmt.Errorf("list parts: %w", err)
	}
	wanted := false
	for _, part := range parts {
		wanted = wanted || (part.Monitored == 1 && partInScope(part.MediaRef, item.MediaRef))
	}
	if (item.Monitored == 1) == wanted {
		return nil
	}
	return queries.SetDesiredItemMonitored(ctx, librarydb.SetDesiredItemMonitoredParams{Monitored: boolToInt(wanted), ID: item.ID})
}

// AllParts answers every part of a work, covered or not: what a replacement file is aligned against.
func (d Desired) AllParts(ctx context.Context, workID int64) ([]librarydb.WorkPart, error) {
	parts, err := librarydb.New(d.store.Read()).ListWorkParts(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: parts of work %d: %w", workID, err)
	}
	return parts, nil
}

// UnfilledParts returns a work's parts not yet covered by itemID's files, in part order, for alignment to try against new candidates.
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

// AttachedFiles is every media object already attached to one of the item's parts.
func (d Desired) AttachedFiles(ctx context.Context, itemID int64) (map[int64]bool, error) {
	files, err := librarydb.New(d.store.Read()).ListFilesForDesiredItem(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("library: attached files: %w", err)
	}
	attached := make(map[int64]bool, len(files))
	for _, file := range files {
		if file.Role == "primary" {
			attached[file.MediaObjectID] = true
		}
	}
	return attached, nil
}

// ExternalIDsFor returns parts' external ids for one provider keyed by part id, provider-agnostic (ADR-23): the media type names the provider.
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
		return recomputeAlbumTx(ctx, queries, itemID, workID, d.store.Now())
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

// WantedIn reports whether a library has any wanted item of the work, monitored or not.
func (d Desired) WantedIn(ctx context.Context, workID, categoryID int64) (bool, error) {
	items, err := librarydb.New(d.store.Read()).CountDesiredItemsInCategory(ctx, librarydb.CountDesiredItemsInCategoryParams{WorkID: workID, CategoryID: categoryID})
	if err != nil {
		return false, fmt.Errorf("library: items of work %d in library %d: %w", workID, categoryID, err)
	}
	return items > 0, nil
}

// AlbumItemsForWork answers the album-level (SET) items of a work, one per category that wants it.
func (d Desired) AlbumItemsForWork(ctx context.Context, workID int64) ([]librarydb.DesiredItem, error) {
	items, err := librarydb.New(d.store.Read()).ListDesiredItemsForWork(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: album items for work %d: %w", workID, err)
	}
	return slices.DeleteFunc(items, func(item librarydb.DesiredItem) bool { return item.SatisfactionKind != "SET" }), nil
}

// GroupsToSearch answers whether a work is wanted as a whole and, when it is wanted group by group, the refs of its
// monitored groups that are not complete: what a search of the whole work searches.
func (d Desired) GroupsToSearch(ctx context.Context, workID int64) (whole bool, refs []string, err error) {
	items, err := d.AlbumItemsForWork(ctx, workID)
	if err != nil {
		return false, nil, err
	}
	for _, item := range items {
		if item.MediaRef == "" {
			return true, nil, nil
		}
		if item.Monitored == 1 && item.State != "COMPLETE" {
			refs = append(refs, item.MediaRef)
		}
	}
	return len(items) == 0, refs, nil
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

// ItemsForFile answers every desired_item a file satisfies, for scans recognising known bytes and for removals (section 5.2).
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

// FileIdentity is a file's editorial position (work, edition or nil for any pressing, part) from its coverage, so rename previews need no media-type knowledge.
type FileIdentity struct {
	WorkID    int64
	EditionID *int64
	MediaRef  string
}

// IdentityForFile answers a file's editorial identity, or found=false when it has no coverage; only primary coverage counts, since a trailer or bonus does not name the file for renaming.
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

// HuntPriority is an item's composite priority (ADR-36); an item not in the hunt queue has 0.
func (d Desired) HuntPriority(ctx context.Context, itemID int64) (int64, error) {
	priority, err := librarydb.New(d.store.Read()).GetHuntPriority(ctx, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return priority, err
}

// ReleaseDate is an item's street date as work_date holds it ("2024-03-01", "2024"), empty when unknown.
func (d Desired) ReleaseDate(ctx context.Context, itemID int64) (string, error) {
	return librarydb.New(d.store.Read()).GetItemReleaseDate(ctx, librarydb.GetItemReleaseDateParams{
		Kind: WorkDateStreet, ItemID: itemID,
	})
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

// DetachAndRecompute removes a file under every listed role and recomputes counts and state in one transaction, as RecordAlignment does in reverse; detaching an absent role is a no-op.
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
		return recomputeAlbumTx(ctx, queries, itemID, workID, d.store.Now())
	})
	if err != nil {
		return fmt.Errorf("library: detach file %d from item %d: %w", mediaObjectID, itemID, err)
	}
	return nil
}

// RecomputeAlbum re-derives required_count, present_count and state and marks the item dirty for the planner; idempotent, since periodic recompute is the design.
func (d Desired) RecomputeAlbum(ctx context.Context, itemID, workID int64) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return recomputeAlbumTx(ctx, librarydb.New(tx), itemID, workID, d.store.Now())
	})
	if err != nil {
		return fmt.Errorf("library: recompute album: %w", err)
	}
	return nil
}

func recomputeAlbumTx(ctx context.Context, queries *librarydb.Queries, itemID, workID int64, now time.Time) error {
	item, err := queries.GetDesiredItem(ctx, itemID)
	if err != nil {
		return fmt.Errorf("get item: %w", err)
	}
	parts, err := queries.ListWorkParts(ctx, workID)
	if err != nil {
		return fmt.Errorf("list parts: %w", err)
	}
	required := requiredOf(item, parts, now)
	if err := queries.SetDesiredItemRequiredCount(ctx,
		librarydb.SetDesiredItemRequiredCountParams{RequiredCount: required, ID: itemID}); err != nil {
		return fmt.Errorf("set required count: %w", err)
	}
	next, basis := nextOut(item, parts, now), availableFromParts
	if next == 0 && !anyPartTimed(item, parts) {
		// A work whose parts carry no time of their own (an album) is out on its release day.
		if next, err = releaseAhead(ctx, queries, workID, now); err != nil {
			return fmt.Errorf("release day: %w", err)
		}
		basis = availableFromRelease
	}
	if err := stampComingOut(ctx, queries, item, required, next, basis); err != nil {
		return fmt.Errorf("set coming out: %w", err)
	}
	if err := queries.RecomputePresentCount(ctx, itemID); err != nil {
		return fmt.Errorf("recompute present count: %w", err)
	}
	item, err = queries.GetDesiredItem(ctx, itemID)
	if err != nil {
		return fmt.Errorf("get item: %w", err)
	}
	return queries.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{
		State: recountedState(item.State, item.RequiredCount, item.PresentCount), PresentCount: item.PresentCount, ID: itemID,
	})
}

// requiredOf is what a SET item owes at now: its monitored parts that are out. A part not out yet is not owed, so a
// season still airing is complete between its episodes; a work without parts keeps what its type said it needs.
func requiredOf(item librarydb.DesiredItem, parts []librarydb.WorkPart, now time.Time) sql.NullInt64 {
	var scoped, owed int64
	for _, part := range parts {
		if !partInScope(part.MediaRef, item.MediaRef) {
			continue
		}
		if part.Optional == 1 {
			// A pregap is recorded and matched, but the work never owes it.
			continue
		}
		scoped++
		if part.Monitored == 1 && PartOut(part.AvailableAt.Int64, PartGrace(part.AvailableGrace, part.AvailableConfidence), part.Date.String, part.AvailableBasis, now) {
			owed++
		}
	}
	if scoped == 0 {
		return item.RequiredCount
	}
	return sql.NullInt64{Int64: owed, Valid: true}
}

const (
	// availableFromParts marks an item's available_from as the next part out, kept only while the item owes nothing.
	availableFromParts = "parts"
	// availableFromRelease marks it as the work's release day, for a work whose parts carry no time.
	availableFromRelease = "release"
)

// nextOut is when the earliest monitored part of the item not out yet comes out; zero when none has a time.
func nextOut(item librarydb.DesiredItem, parts []librarydb.WorkPart, now time.Time) int64 {
	var next int64
	for _, part := range parts {
		if part.Monitored != 1 || !partInScope(part.MediaRef, item.MediaRef) ||
			PartOut(part.AvailableAt.Int64, PartGrace(part.AvailableGrace, part.AvailableConfidence), part.Date.String, part.AvailableBasis, now) {
			continue
		}
		at := part.AvailableAt.Int64
		if at == 0 {
			if day, err := time.Parse("2006-01-02", part.Date.String); err == nil {
				at = day.Unix()
			}
		}
		if at > 0 && (next == 0 || at < next) {
			next = at
		}
	}
	return next
}

// anyPartTimed says whether a part of the item has a time or a day of its own.
func anyPartTimed(item librarydb.DesiredItem, parts []librarydb.WorkPart) bool {
	for _, part := range parts {
		if partInScope(part.MediaRef, item.MediaRef) && (part.AvailableAt.Valid || part.Date.String != "") {
			return true
		}
	}
	return false
}

// releaseAhead answers the start of the work's release day while it is still ahead, 0 otherwise.
func releaseAhead(ctx context.Context, queries *librarydb.Queries, workID int64, now time.Time) (int64, error) {
	day, err := queries.WorkReleaseDay(ctx, workID)
	if err != nil {
		return 0, err
	}
	// A malformed day is no release ahead: failing here would stop the whole re-count of the item.
	if released, err := time.Parse("2006-01-02", day); err == nil && released.After(now) {
		return released.Unix(), nil
	}
	return 0, nil
}

// stampComingOut gives an item the time it comes out ("Not out yet"): the next part's while the item owes nothing, or
// the work's release day while it is ahead; it takes either away once it no longer holds.
func stampComingOut(ctx context.Context, queries *librarydb.Queries, item librarydb.DesiredItem, required sql.NullInt64, next int64, basis string) error {
	holds := next > 0 && (basis == availableFromRelease || required.Valid && required.Int64 == 0)
	stamped := item.AvailableBasis.String == availableFromParts || item.AvailableBasis.String == availableFromRelease
	switch {
	case holds:
		if item.AvailableFrom.Int64 == next && item.AvailableBasis.String == basis {
			return nil
		}
		return queries.SetDesiredItemAvailability(ctx, librarydb.SetDesiredItemAvailabilityParams{
			AvailableFrom: sql.NullInt64{Int64: next, Valid: true}, AvailableBasis: sql.NullString{String: basis, Valid: true},
			ID: item.ID,
		})
	case stamped:
		return queries.SetDesiredItemAvailability(ctx, librarydb.SetDesiredItemAvailabilityParams{ID: item.ID})
	}
	return nil
}

// recountMargin is how far past the longest grace a re-count looks, so a part is seen coming out after a missed run.
const recountMargin = 36 * time.Hour

// RecountComingOut re-counts the items of works with a part that came out lately or is still to come, and answers how
// many changed: time passing writes nothing, so a season would stay as complete as before its next episode aired.
// longestGrace is the longest wait any part has.
func (d Desired) RecountComingOut(ctx context.Context, longestGrace time.Duration) (int, error) {
	now := d.store.Now()
	since := now.Add(-longestGrace - recountMargin)
	reader := librarydb.New(d.store.Read())
	partsOfWork := map[int64][]librarydb.WorkPart{}
	changed := 0
	var afterID int64
	for {
		page, err := reader.ListItemsWithPartsComingOut(ctx, librarydb.ListItemsWithPartsComingOutParams{
			AfterID: afterID, Since: sql.NullInt64{Int64: since.Unix(), Valid: true},
			SinceDate: sql.NullString{String: since.UTC().Format("2006-01-02"), Valid: true}, PageSize: 200,
		})
		if err != nil {
			return changed, fmt.Errorf("library: items with parts coming out: %w", err)
		}
		for _, row := range page {
			afterID = row.ID
			item, err := reader.GetDesiredItem(ctx, row.ID)
			if err != nil {
				return changed, fmt.Errorf("library: get item %d: %w", row.ID, err)
			}
			// A series' seasons share its parts, which a recount does not change: read them once per work.
			parts, read := partsOfWork[row.WorkID]
			if !read {
				var err error
				if parts, err = reader.ListWorkParts(ctx, row.WorkID); err != nil {
					return changed, fmt.Errorf("library: list parts of work %d: %w", row.WorkID, err)
				}
				partsOfWork[row.WorkID] = parts
			}
			if requiredOf(item, parts, now) == item.RequiredCount {
				continue
			}
			if err := d.RecomputeAlbum(ctx, row.ID, row.WorkID); err != nil {
				return changed, err
			}
			changed++
		}
		if len(page) < 200 {
			break
		}
	}
	released, err := d.recountReleases(ctx, now)
	return changed + released, err
}

// recountReleases stamps the albums whose release day is ahead and unstamped, and clears those whose day has come.
func (d Desired) recountReleases(ctx context.Context, now time.Time) (int, error) {
	read := librarydb.New(d.store.Read())
	changed := 0
	var afterID int64
	for {
		page, err := read.ListItemsAwaitingRelease(ctx, librarydb.ListItemsAwaitingReleaseParams{
			AfterID: afterID, Today: now.UTC().Format("2006-01-02"), Now: sql.NullInt64{Int64: now.Unix(), Valid: true}, PageSize: 200,
		})
		if err != nil {
			return changed, fmt.Errorf("library: items awaiting release: %w", err)
		}
		for _, row := range page {
			afterID = row.ID
			if err := d.RecomputeAlbum(ctx, row.ID, row.WorkID); err != nil {
				return changed, err
			}
			changed++
		}
		if len(page) < 200 {
			return changed, nil
		}
	}
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

// CountIncomplete answers how many monitored items are MISSING or PARTIAL -- library health's "missing tracks" figure
// (todo 4.8), a live query on desired_by_category. categoryID zero means every category.
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

// UpgradableItems pages through every monitored UPGRADABLE item, oldest id first; zero categoryID is every category.
func (d Desired) UpgradableItems(ctx context.Context, categoryID, afterID, pageSize int64) ([]IncompleteItem, error) {
	rows, err := librarydb.New(d.store.Read()).ListUpgradableDesiredItems(ctx, librarydb.ListUpgradableDesiredItemsParams{
		CategoryID: categoryID, PersonID: 0, AfterID: afterID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list upgradable desired items: %w", err)
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

// recountedState is the state after a recount: a COMPLETE count leaves an UPGRADABLE item as it is, because only the
// cutoff check judges quality, and a rescan resetting it would make the upgrade it is downloading unwanted at import.
func recountedState(previous string, required sql.NullInt64, present int64) string {
	state := albumState(required, present)
	if previous == "UPGRADABLE" && state == "COMPLETE" {
		return previous
	}
	return state
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
