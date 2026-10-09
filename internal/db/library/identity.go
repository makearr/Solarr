package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/model"
)

// WorkIdentity is what a work should look like if one has to be created. Naming only
// -- persistence is library's job, not a media type's (ADR-23: core carries no
// media-type logic, so this struct is the whole surface a media type gets to shape).
type WorkIdentity struct {
	MediaType      string
	CanonicalTitle string
	SortTitle      string
	// Subtype is MusicBrainz's own release-group vocabulary (Album, EP,
	// Single, Broadcast, Other, ...), stored as-is when the caller already
	// knows it. Empty when it doesn't -- findOrCreateWork leaves the column
	// NULL rather than write "", so a later enrichment pass can still fill
	// it in (UpdateWorkSubtypeIfNull's own "fills a gap, never
	// overwrites" rule, work.year's own precedent).
	Subtype string
}

// WorkLink is one caller's Identifier chain result, ready to record: the external id
// it resolved to, and what to create if nothing already claims it.
type WorkLink struct {
	Provider   string
	ExternalID string
	// MediaObjectID is the file that resolved this link, for a caller that
	// wants to know which work each of its files ended up under (ResolveWorks'
	// returned ids line up with links positionally). Zero means the caller has
	// no single file to attribute this link to.
	MediaObjectID int64
	// Confidence is the provider's own confidence in the (Provider, ExternalID)-to-work
	// mapping (ADR-8), not the chain's confidence in having read it correctly.
	Confidence float64
	Work       WorkIdentity
	// Edition is optional: when set, an edition is resolved (or created) under the
	// work this link resolves to, in the same transaction.
	Edition *EditionLink
	// RootFolderID records which root folder this link's identifying file came
	// from (work_root_folder) -- on-disk presence, one half of ADR-27
	// visibility (WorkFilter.RootFolderIDs' own doc comment has the other
	// half, desired_item.category_id). Zero means the caller has no root
	// folder to report.
	RootFolderID int64
}

// EditionIdentity is what an edition should look like if one has to be created.
// Naming only, exactly like WorkIdentity.
type EditionIdentity struct {
	Kind  string
	Label string
}

// EditionLink is one edition's resolved identity -- an edition_external_id row to
// find or create, scoped to whatever work its WorkLink resolves to.
type EditionLink struct {
	Provider   string
	ExternalID string
	// Confidence is the provider's own confidence in the mapping, as WorkLink's.
	Confidence float64
	Edition    EditionIdentity
}

// Identity resolves external ids to works (ADR-24's "core owns the chain, a strategy
// only answers for itself" -- persisting what it decided is core's job too).
type Identity struct{ store *store.Store }

// NewIdentity builds it over an open library store.
func NewIdentity(opened *store.Store) Identity { return Identity{store: opened} }

// ResolveWorks finds or creates the work behind each link's (Provider, ExternalID),
// linking it when new, all in one transaction (Invariant 2). A link naming a pair
// already resolved earlier in the same batch reuses that work rather than creating a
// second one: writes are visible to reads within the same transaction. The
// returned ids line up with links, positionally, so a caller that also carries
// each link's MediaObjectID can pair a file back to the work it resolved to.
func (i Identity) ResolveWorks(ctx context.Context, at time.Time, links []WorkLink) ([]int64, error) {
	if len(links) == 0 {
		return nil, nil
	}
	var workIDs []int64
	err := i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		ids, _, err := findOrCreateWorks(ctx, tx, queries, at, links)
		if err != nil {
			return err
		}
		for index, link := range links {
			if err := linkResolvedWork(ctx, queries, at, ids[index], link); err != nil {
				return fmt.Errorf("library: resolve work %s:%s: %w", link.Provider, link.ExternalID, err)
			}
		}
		workIDs = ids
		return nil
	})
	if err != nil {
		return nil, err
	}
	return workIDs, nil
}

// WorkResolution is one link's ResolveWorksDetailed result: the ids
// ResolveWorks itself already returns, plus whether each row was created new
// by this call or matched something that already existed. A caller with
// nothing to undo (scan's own consumer.go) has no use for the distinction;
// one that must later revert only "the rows the session created" (ADR-48)
// does, so it is a separate, additive method rather than a breaking change
// to ResolveWorks' signature.
type WorkResolution struct {
	WorkID         int64
	WorkCreated    bool
	EditionID      *int64
	EditionCreated bool
}

// ResolveWorksDetailed is ResolveWorks plus the created/matched distinction,
// for a caller that must know precisely which rows it is responsible for
// (import commit's own revert bookkeeping). Shares the same per-link
// resolution as ResolveWorks -- a link naming a pair already resolved
// earlier in the same batch still reuses that work rather than creating a
// second one.
func (i Identity) ResolveWorksDetailed(ctx context.Context, at time.Time, links []WorkLink) ([]WorkResolution, error) {
	if len(links) == 0 {
		return nil, nil
	}
	resolutions := make([]WorkResolution, len(links))
	err := i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		ids, created, err := findOrCreateWorks(ctx, tx, queries, at, links)
		if err != nil {
			return err
		}
		for index, link := range links {
			workID := ids[index]
			resolution := WorkResolution{WorkID: workID, WorkCreated: created[index]}
			if link.Edition != nil {
				editionID, editionCreated, err := resolveEdition(ctx, queries, workID, *link.Edition)
				if err != nil {
					return fmt.Errorf("library: resolve edition for work %d: %w", workID, err)
				}
				resolution.EditionID = &editionID
				resolution.EditionCreated = editionCreated
			}
			if link.RootFolderID != 0 {
				if err := queries.LinkWorkRootFolder(ctx, librarydb.LinkWorkRootFolderParams{
					WorkID: workID, RootFolderID: link.RootFolderID, FirstSeenAt: at.Unix(),
				}); err != nil {
					return fmt.Errorf("library: link work %d to root folder %d: %w", workID, link.RootFolderID, err)
				}
			}
			if err := linkWorkFile(ctx, queries, at, workID, link); err != nil {
				return err
			}
			resolutions[index] = resolution
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resolutions, nil
}

// DeleteWork removes a work outright, cascading to every table that
// references it (edition, desired_item, credits, genres, alt titles,
// work_root_folder, external ids -- every REFERENCES work(id) in the schema
// is ON DELETE CASCADE). Import revert's own use (todo 4.9): only ever
// called for a work CommitUnit itself created (import_unit.work_created),
// and only after every import_unit referencing it -- created or only
// matched -- has been cleared (ImportSessions.ClearUnitPlacement), since
// import_unit.work_id carries no cascade of its own.
func (i Identity) DeleteWork(ctx context.Context, workID int64) error {
	err := i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		members, err := membersOfWork(ctx, queries, workID)
		if err != nil {
			return err
		}
		if err := queries.DeleteWork(ctx, workID); err != nil {
			return err
		}
		return members.refresh(ctx, queries)
	})
	if err != nil {
		return fmt.Errorf("library: delete work %d: %w", workID, err)
	}
	return nil
}

// DeleteEdition removes an edition outright, cascading edition_external_id.
// The same import-revert-only use as DeleteWork, one level down ADR-7's
// Work -> Edition chain.
func (i Identity) DeleteEdition(ctx context.Context, editionID int64) error {
	err := i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteEdition(ctx, editionID)
	})
	if err != nil {
		return fmt.Errorf("library: delete edition %d: %w", editionID, err)
	}
	return nil
}

// linkResolvedWork records what else a link says about its resolved work: its edition, root
// folder and file.
func linkResolvedWork(ctx context.Context, queries *librarydb.Queries, at time.Time, workID int64, link WorkLink) error {
	if link.Edition != nil {
		if _, _, err := resolveEdition(ctx, queries, workID, *link.Edition); err != nil {
			return fmt.Errorf("edition: %w", err)
		}
	}
	if link.RootFolderID != 0 {
		if err := queries.LinkWorkRootFolder(ctx, librarydb.LinkWorkRootFolderParams{
			WorkID: workID, RootFolderID: link.RootFolderID, FirstSeenAt: at.Unix(),
		}); err != nil {
			return fmt.Errorf("root folder: %w", err)
		}
	}
	return linkWorkFile(ctx, queries, at, workID, link)
}

// WorkFileLocation is one path of the file recorded for a work.
type WorkFileLocation struct {
	WorkID       int64
	RootFolderID int64
	RelPath      string
}

// WorkFileLocations answers every path of the file recorded for each of these
// works (work_file); a work with none is simply absent.
func (i Identity) WorkFileLocations(ctx context.Context, workIDs []int64) ([]WorkFileLocation, error) {
	if len(workIDs) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(workIDs)
	if err != nil {
		return nil, fmt.Errorf("library: encode work ids: %w", err)
	}
	rows, err := librarydb.New(i.store.Read()).ListWorkFileLocations(ctx, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("library: work file locations: %w", err)
	}
	locations := make([]WorkFileLocation, len(rows))
	for index, row := range rows {
		locations[index] = WorkFileLocation{WorkID: row.WorkID, RootFolderID: row.RootFolderID, RelPath: row.RelPath}
	}
	return locations, nil
}

// linkWorkFile remembers a file a work was resolved from, when the link names
// one, so the work's embedded cover art can be read and its files rescanned.
func linkWorkFile(ctx context.Context, queries *librarydb.Queries, at time.Time, workID int64, link WorkLink) error {
	if link.MediaObjectID == 0 {
		return nil
	}
	if err := queries.LinkWorkFile(ctx, librarydb.LinkWorkFileParams{
		WorkID: workID, MediaObjectID: link.MediaObjectID, FirstSeenAt: at.Unix(),
	}); err != nil {
		return fmt.Errorf("library: link work %d to file %d: %w", workID, link.MediaObjectID, err)
	}
	return nil
}

// findOrCreateWorks resolves every link's (Provider, ExternalID) to a work: one lookup per
// distinct key, then every new work in one multi-row INSERT and their external ids in another
// -- per-row inserts were 81% of a scan batch, and one statement is 3.9x faster (notes 8.19).
// A key repeated in the batch reuses its work; only its first link counts as having created it.
func findOrCreateWorks(
	ctx context.Context, tx *sql.Tx, queries *librarydb.Queries, at time.Time, links []WorkLink,
) (ids []int64, created []bool, err error) {
	type key struct{ provider, externalID string }
	ids = make([]int64, len(links))
	created = make([]bool, len(links))
	resolved := make(map[key]int64, len(links))
	var fresh []int // index of the first link of each key not yet in the library
	for index, link := range links {
		k := key{link.Provider, link.ExternalID}
		if _, seen := resolved[k]; seen {
			continue
		}
		existing, err := queries.GetWorkByExternalID(ctx, librarydb.GetWorkByExternalIDParams{
			Provider: link.Provider, ExternalID: link.ExternalID,
		})
		switch {
		case err == nil:
			resolved[k] = existing.ID
		case errors.Is(err, sql.ErrNoRows):
			resolved[k] = 0
			fresh = append(fresh, index)
		default:
			return nil, nil, fmt.Errorf("library: look up work %s:%s: %w", link.Provider, link.ExternalID, err)
		}
	}

	if len(fresh) > 0 {
		// One writer per database (ADR-3): inside this transaction nothing else can take these ids,
		// and explicit ids keep links and rows paired, which RETURNING's order does not promise.
		var maxID int64
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM work").Scan(&maxID); err != nil {
			return nil, nil, fmt.Errorf("library: next work id: %w", err)
		}
		for start := 0; start < len(fresh); start += insertChunk {
			chunk := fresh[start:min(start+insertChunk, len(fresh))]
			works := make([]string, len(chunk))
			externals := make([]string, len(chunk))
			var workArgs, externalArgs []any
			for position, index := range chunk {
				link := links[index]
				id := maxID + int64(start+position) + 1
				resolved[key{link.Provider, link.ExternalID}] = id
				created[index] = true
				works[position] = "(?, ?, ?, ?, ?, ?, ?)"
				workArgs = append(workArgs, id, link.Work.MediaType, link.Work.CanonicalTitle, link.Work.SortTitle,
					at.Unix(), at.Unix(), nullString(link.Work.Subtype))
				externals[position] = "(?, ?, ?, ?, ?)"
				externalArgs = append(externalArgs, link.Provider, link.ExternalID, id, link.Confidence, at.Unix())
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO work (id, media_type, canonical_title, sort_title,"+
				" added_at, updated_at, subtype) VALUES "+strings.Join(works, ", "), workArgs...); err != nil {
				return nil, nil, fmt.Errorf("library: create works: %w", err)
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO work_external_id (provider, external_id, work_id,"+
				" confidence, first_seen_at) VALUES "+strings.Join(externals, ", "), externalArgs...); err != nil {
				return nil, nil, fmt.Errorf("library: link work external ids: %w", err)
			}
		}
	}

	for index, link := range links {
		ids[index] = resolved[key{link.Provider, link.ExternalID}]
	}
	return ids, created, nil
}

// insertChunk keeps one multi-row INSERT at ADR-3's 200-row bulk size.
const insertChunk = 200

// resolveEdition finds or creates the edition behind link's (Provider, ExternalID),
// scoped to workID -- the same find-then-create shape as findOrCreateWork, one level
// down ADR-7's Work -> Edition -> File chain.
func resolveEdition(
	ctx context.Context, queries *librarydb.Queries, workID int64, link EditionLink,
) (editionID int64, created bool, err error) {
	existing, err := queries.GetEditionByExternalID(ctx, librarydb.GetEditionByExternalIDParams{
		Provider: link.Provider, ExternalID: link.ExternalID,
	})
	if err == nil {
		return existing.ID, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("look up: %w", err)
	}

	edition, err := queries.CreateEdition(ctx, librarydb.CreateEditionParams{
		WorkID: workID, Kind: link.Edition.Kind, Label: link.Edition.Label, IsDefault: 0,
	})
	if err != nil {
		return 0, false, fmt.Errorf("create edition: %w", err)
	}
	if err := queries.LinkEditionExternalID(ctx, librarydb.LinkEditionExternalIDParams{
		Provider: link.Provider, ExternalID: link.ExternalID, EditionID: edition.ID,
		Confidence: link.Confidence,
	}); err != nil {
		return 0, false, fmt.Errorf("link external id: %w", err)
	}
	return edition.ID, true, nil
}

// ReturnedToReview is what ReturnToReview did with an album.
type ReturnedToReview struct {
	Files       int
	WorkDeleted bool
}

// ReturnToReview queues an album's files for identification with candidate as a weak hint and detaches
// them (M1f: its release group is gone); the work goes unless a person added it.
func (i Identity) ReturnToReview(ctx context.Context, workID int64, candidate model.Identification, at int64) (ReturnedToReview, error) {
	var result ReturnedToReview
	candidatesJSON, err := encodeCandidates([]model.Identification{candidate})
	if err != nil {
		return result, err
	}
	err = i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		paths, err := queries.ListWorkFilePaths(ctx, workID)
		if err != nil {
			return fmt.Errorf("list files: %w", err)
		}
		for _, step := range []func(context.Context, int64) error{queries.DetachDesiredFilesForWork, queries.UnlinkWorkFiles} {
			if err := step(ctx, workID); err != nil {
				return err
			}
		}
		queued := map[int64]bool{}
		for _, file := range paths {
			if queued[file.MediaObjectID] {
				continue
			}
			queued[file.MediaObjectID] = true
			if err := queries.UpsertUnidentified(ctx, librarydb.UpsertUnidentifiedParams{
				MediaObjectID: file.MediaObjectID, RootFolderID: file.RootFolderID, RelPath: file.RelPath,
				CandidatesJson: candidatesJSON, FirstSeenAt: at, LastSeenAt: at,
			}); err != nil {
				return fmt.Errorf("queue file %d: %w", file.MediaObjectID, err)
			}
		}
		result.Files = len(queued)
		wanted, err := queries.CountPersonAddedItems(ctx, workID)
		if err != nil {
			return err
		}
		if wanted > 0 {
			return queries.ResetDesiredItemsForWork(ctx, workID)
		}
		result.WorkDeleted = true
		return deleteFilelessWork(ctx, queries, workID)
	})
	if err != nil {
		return ReturnedToReview{}, fmt.Errorf("library: return work %d to review: %w", workID, err)
	}
	return result, nil
}

// WorkIdentityOf answers what a work is called, to carry its name onto the album it turns out to be.
func (i Identity) WorkIdentityOf(ctx context.Context, workID int64) (WorkIdentity, error) {
	work, err := librarydb.New(i.store.Read()).GetWork(ctx, workID)
	if err != nil {
		return WorkIdentity{}, fmt.Errorf("library: work %d: %w", workID, err)
	}
	return WorkIdentity{MediaType: work.MediaType, CanonicalTitle: work.CanonicalTitle, SortTitle: work.SortTitle}, nil
}

// FileRoot is one file of a work and the root folder it lives on.
type FileRoot struct{ MediaObjectID, RootFolderID int64 }

// WorkFileRoots answers each of a work's files once, with its root folder.
func (i Identity) WorkFileRoots(ctx context.Context, workID int64) ([]FileRoot, error) {
	rows, err := librarydb.New(i.store.Read()).ListWorkFilePaths(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: work file roots %d: %w", workID, err)
	}
	var locations []FileRoot
	seen := map[int64]bool{}
	for _, row := range rows {
		if !seen[row.MediaObjectID] {
			seen[row.MediaObjectID] = true
			locations = append(locations, FileRoot{MediaObjectID: row.MediaObjectID, RootFolderID: row.RootFolderID})
		}
	}
	return locations, nil
}

// ReleaseWork detaches every file from a work whose files now belong to another (an unverified album
// identified, D6) and deletes it; answers whether it was deleted -- kept when a person added it.
func (i Identity) ReleaseWork(ctx context.Context, workID int64) (deleted bool, err error) {
	err = i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.DetachDesiredFilesForWork(ctx, workID); err != nil {
			return err
		}
		if err := queries.UnlinkWorkFiles(ctx, workID); err != nil {
			return err
		}
		asked, err := queries.CountPersonAddedItems(ctx, workID)
		if err != nil || asked > 0 {
			return err
		}
		deleted = true
		return deleteFilelessWork(ctx, queries, workID)
	})
	if err != nil {
		return false, fmt.Errorf("library: release work %d: %w", workID, err)
	}
	return deleted, nil
}

// AnyUnverified reports whether the library holds any unverified album (D6).
func (i Identity) AnyUnverified(ctx context.Context) (bool, error) {
	present, err := librarydb.New(i.store.Read()).AnyUnverifiedWork(ctx)
	if err != nil {
		return false, fmt.Errorf("library: any unverified: %w", err)
	}
	return present == 1, nil
}

// LeaveUnverified takes identified files off every unverified album they were on (D6), deleting an album
// left with no files unless a person added it; one batch, one transaction. Answers albums deleted.
func (i Identity) LeaveUnverified(ctx context.Context, mediaObjectIDs []int64) (deleted int, err error) {
	if len(mediaObjectIDs) == 0 {
		return 0, nil
	}
	err = i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, mediaObjectID := range mediaObjectIDs {
			n, err := leaveUnverified(ctx, queries, mediaObjectID)
			if err != nil {
				return err
			}
			deleted += n
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("library: leave unverified: %w", err)
	}
	return deleted, nil
}

func leaveUnverified(ctx context.Context, queries *librarydb.Queries, mediaObjectID int64) (deleted int, err error) {
	workIDs, err := queries.ListUnverifiedWorksForFile(ctx, mediaObjectID)
	if err != nil {
		return 0, err
	}
	for _, workID := range workIDs {
		if err := queries.DetachDesiredFileForWork(ctx, librarydb.DetachDesiredFileForWorkParams{
			MediaObjectID: mediaObjectID, WorkID: workID,
		}); err != nil {
			return deleted, err
		}
		if err := queries.UnlinkWorkFile(ctx, librarydb.UnlinkWorkFileParams{WorkID: workID, MediaObjectID: mediaObjectID}); err != nil {
			return deleted, err
		}
		files, err := queries.CountWorkFiles(ctx, workID)
		if err != nil {
			return deleted, err
		}
		asked, err := queries.CountPersonAddedItems(ctx, workID)
		if err != nil {
			return deleted, err
		}
		if files > 0 || asked > 0 {
			continue
		}
		if err := deleteFilelessWork(ctx, queries, workID); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

// deleteFilelessWork removes a work and everything pointing at it; the caller has checked it has no files.
func deleteFilelessWork(ctx context.Context, queries *librarydb.Queries, workID int64) error {
	members, err := membersOfWork(ctx, queries, workID)
	if err != nil {
		return err
	}
	if err := queries.DetachAcquisitionsForWork(ctx, workID); err != nil {
		return fmt.Errorf("detach acquisitions: %w", err)
	}
	if err := queries.DeleteDesiredItemsForWork(ctx, workID); err != nil {
		return fmt.Errorf("delete desired items: %w", err)
	}
	if err := queries.DetachImportUnitsFromWork(ctx, sql.NullInt64{Int64: workID, Valid: true}); err != nil {
		return fmt.Errorf("detach import units: %w", err)
	}
	if err := queries.DetachPackItemsFromWork(ctx, sql.NullInt64{Int64: workID, Valid: true}); err != nil {
		return fmt.Errorf("detach pack items: %w", err)
	}
	if err := queries.DeleteWork(ctx, workID); err != nil {
		return fmt.Errorf("delete work: %w", err)
	}
	return members.refresh(ctx, queries)
}

// RemoveWorkResult is what removing an album actually did.
type RemoveWorkResult struct {
	// WorkDeleted is false when the work was kept because files remain: the
	// next scan would recreate it, so deleting the row would be a lie.
	WorkDeleted bool
	FilesKept   int64
}

// WorkFiles answers every media object the work has on disk, for a caller
// deleting an album's files along with it (2026-09-19).
func (i Identity) WorkFiles(ctx context.Context, workID int64) ([]int64, error) {
	files, err := librarydb.New(i.store.Read()).ListWorkFiles(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: work files %d: %w", workID, err)
	}
	return files, nil
}

// RemoveWork drops every desired item of a work, and the work itself when it has no files or withFiles says its
// type's scan will not add it back; files are never touched (deleting bytes is the recycle bin's, section 10.8).
func (i Identity) RemoveWork(ctx context.Context, workID int64, withFiles bool) (RemoveWorkResult, error) {
	var result RemoveWorkResult
	err := i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		files, err := queries.CountWorkFiles(ctx, workID)
		if err != nil {
			return fmt.Errorf("count files: %w", err)
		}
		members, err := membersOfWork(ctx, queries, workID)
		if err != nil {
			return err
		}
		// Without this, removing any album that was ever downloaded failed on acquisition's FK.
		if err := queries.DetachAcquisitionsForWork(ctx, workID); err != nil {
			return fmt.Errorf("detach acquisitions: %w", err)
		}
		if err := queries.DeleteDesiredItemsForWork(ctx, workID); err != nil {
			return fmt.Errorf("delete desired items: %w", err)
		}
		result.FilesKept = files
		if files > 0 && !withFiles {
			return members.refresh(ctx, queries)
		}
		if err := queries.DetachImportUnitsFromWork(ctx, sql.NullInt64{Int64: workID, Valid: true}); err != nil {
			return fmt.Errorf("detach import units: %w", err)
		}
		if err := queries.DetachPackItemsFromWork(ctx, sql.NullInt64{Int64: workID, Valid: true}); err != nil {
			return fmt.Errorf("detach pack items: %w", err)
		}
		if err := queries.DeleteWork(ctx, workID); err != nil {
			return fmt.Errorf("delete work: %w", err)
		}
		result.WorkDeleted = true
		return members.refresh(ctx, queries)
	})
	if err != nil {
		return RemoveWorkResult{}, fmt.Errorf("library: remove work %d: %w", workID, err)
	}
	return result, nil
}
