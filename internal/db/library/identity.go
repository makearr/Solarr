package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// WorkIdentity is what a work should look like if one has to be created. Naming only
// -- persistence is library's job, not a media type's (ADR-23: core carries no
// media-type logic, so this struct is the whole surface a media type gets to shape).
type WorkIdentity struct {
	MediaType      string
	CanonicalTitle string
	SortTitle      string
	// PrimaryType is MusicBrainz's own release-group vocabulary (Album, EP,
	// Single, Broadcast, Other, ...), stored as-is when the caller already
	// knows it. Empty when it doesn't -- findOrCreateWork leaves the column
	// NULL rather than write "", so a later enrichment pass can still fill
	// it in (UpdateWorkPrimaryTypeIfNull's own "fills a gap, never
	// overwrites" rule, work.year's own precedent).
	PrimaryType string
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
	workIDs := make([]int64, len(links))
	err := i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for index, link := range links {
			workID, _, err := resolveWork(ctx, queries, at, link)
			if err != nil {
				return fmt.Errorf("library: resolve work %s:%s: %w", link.Provider, link.ExternalID, err)
			}
			workIDs[index] = workID
		}
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
		for index, link := range links {
			workID, workCreated, err := findOrCreateWork(ctx, queries, at, link)
			if err != nil {
				return fmt.Errorf("library: resolve work %s:%s: %w", link.Provider, link.ExternalID, err)
			}
			resolution := WorkResolution{WorkID: workID, WorkCreated: workCreated}
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
		return librarydb.New(tx).DeleteWork(ctx, workID)
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

func resolveWork(
	ctx context.Context, queries *librarydb.Queries, at time.Time, link WorkLink,
) (workID int64, created bool, err error) {
	workID, created, err = findOrCreateWork(ctx, queries, at, link)
	if err != nil {
		return 0, false, err
	}
	if link.Edition != nil {
		if _, _, err := resolveEdition(ctx, queries, workID, *link.Edition); err != nil {
			return 0, false, fmt.Errorf("edition: %w", err)
		}
	}
	if link.RootFolderID != 0 {
		if err := queries.LinkWorkRootFolder(ctx, librarydb.LinkWorkRootFolderParams{
			WorkID: workID, RootFolderID: link.RootFolderID, FirstSeenAt: at.Unix(),
		}); err != nil {
			return 0, false, fmt.Errorf("root folder: %w", err)
		}
	}
	if err := linkWorkFile(ctx, queries, at, workID, link); err != nil {
		return 0, false, err
	}
	return workID, created, nil
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

func findOrCreateWork(
	ctx context.Context, queries *librarydb.Queries, at time.Time, link WorkLink,
) (workID int64, created bool, err error) {
	existing, err := queries.GetWorkByExternalID(ctx, librarydb.GetWorkByExternalIDParams{
		Provider: link.Provider, ExternalID: link.ExternalID,
	})
	if err == nil {
		return existing.ID, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("look up: %w", err)
	}

	work, err := queries.CreateWork(ctx, librarydb.CreateWorkParams{
		MediaType: link.Work.MediaType, CanonicalTitle: link.Work.CanonicalTitle,
		SortTitle: link.Work.SortTitle, AddedAt: at.Unix(), UpdatedAt: at.Unix(),
		PrimaryType: nullString(link.Work.PrimaryType),
	})
	if err != nil {
		return 0, false, fmt.Errorf("create work: %w", err)
	}
	if err := queries.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
		Provider: link.Provider, ExternalID: link.ExternalID, WorkID: work.ID,
		Confidence: link.Confidence, FirstSeenAt: at.Unix(),
	}); err != nil {
		return 0, false, fmt.Errorf("link external id: %w", err)
	}
	return work.ID, true, nil
}

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
