package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// PlaceholderEdition is one edition an Identifier chain resolved by external id but
// had no real metadata for yet -- kind 'unknown' is the marker any chain may leave
// (EditionLink's doc comment), not specific to music.
type PlaceholderEdition struct {
	EditionID int64
	WorkID    int64
	// ExternalID is the id the resolving link recorded as the edition's label
	// (EditionLink, resolveEdition) -- whatever a provider needs to look the real
	// data up.
	ExternalID string
}

// CountPlaceholderEditions is how many editions still wait on real metadata.
func (i Identity) CountPlaceholderEditions(ctx context.Context) (int64, error) {
	return librarydb.New(i.store.Read()).CountPlaceholderEditions(ctx)
}

// PlaceholderEditions returns up to limit editions still waiting on real metadata after
// afterEditionID, oldest first; the cursor walks past editions that failed, which stay placeholders.
func (i Identity) PlaceholderEditions(ctx context.Context, afterEditionID int64, limit int) ([]PlaceholderEdition, error) {
	rows, err := librarydb.New(i.store.Read()).ListPlaceholderEditions(ctx, librarydb.ListPlaceholderEditionsParams{
		AfterID: afterEditionID, PageSize: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("library: placeholder editions: %w", err)
	}
	out := make([]PlaceholderEdition, len(rows))
	for index, row := range rows {
		out[index] = PlaceholderEdition{EditionID: row.ID, WorkID: row.WorkID, ExternalID: row.Label}
	}
	return out, nil
}

// RootFoldersForWork lists the root folders work_root_folder recorded for a work
// (identity.go's resolveWork writes it as a side effect of ResolveWorks). The
// temporary bridge category filtering uses until desired_item exists for music.
func (i Identity) RootFoldersForWork(ctx context.Context, workID int64) ([]int64, error) {
	ids, err := librarydb.New(i.store.Read()).ListRootFoldersForWork(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: root folders for work: %w", err)
	}
	return ids, nil
}

// WorkExternalID looks up a work's external id under one provider, or ok=false if
// the work carries none from that provider.
func (i Identity) WorkExternalID(ctx context.Context, workID int64, provider string) (externalID string, ok bool, err error) {
	row, err := librarydb.New(i.store.Read()).GetWorkExternalIDByProvider(ctx,
		librarydb.GetWorkExternalIDByProviderParams{WorkID: workID, Provider: provider})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("library: work external id: %w", err)
	}
	return row.ExternalID, true, nil
}

// FollowMergedIDs moves a work's and an edition's ids to what the provider now answers with (a
// merged MusicBrainz id), unless another holds the new id. Answers what moved.
func (i Identity) FollowMergedIDs(ctx context.Context, merged MergedIDs) (workMoved, editionMoved bool, err error) {
	err = i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if merged.WorkProvider != "" && merged.OldWorkID != "" && merged.NewWorkID != "" && merged.OldWorkID != merged.NewWorkID {
			rows, err := queries.RepointWorkExternalID(ctx, librarydb.RepointWorkExternalIDParams{
				NewID: merged.NewWorkID, Provider: merged.WorkProvider, OldID: merged.OldWorkID, WorkID: merged.WorkID,
			})
			if err != nil {
				return err
			}
			workMoved = rows > 0
		}
		if merged.EditionProvider != "" && merged.OldEditionID != "" && merged.NewEditionID != "" &&
			merged.OldEditionID != merged.NewEditionID {
			rows, err := queries.RepointEditionExternalID(ctx, librarydb.RepointEditionExternalIDParams{
				NewID: merged.NewEditionID, Provider: merged.EditionProvider, OldID: merged.OldEditionID,
				EditionID: merged.EditionID,
			})
			if err != nil {
				return err
			}
			editionMoved = rows > 0
		}
		return nil
	})
	if err != nil {
		return false, false, fmt.Errorf("library: follow merged ids: %w", err)
	}
	return workMoved, editionMoved, nil
}

// MergedIDs is one enrichment's asked-for and answered ids; an empty pair is left alone.
type MergedIDs struct {
	WorkID                                      int64
	WorkProvider, OldWorkID, NewWorkID          string
	EditionID                                   int64
	EditionProvider, OldEditionID, NewEditionID string
}

// KnownWorkNaming is what an already known work is named by: its canonical title, year and artist.
type KnownWorkNaming struct {
	Title      string
	Year       *int64
	ArtistName string
}

// KnownWork answers how the work an external id already resolves to is named, or ok=false.
func (i Identity) KnownWork(ctx context.Context, provider, externalID string) (KnownWorkNaming, bool, error) {
	row, err := librarydb.New(i.store.Read()).GetWorkNamingByExternalID(ctx,
		librarydb.GetWorkNamingByExternalIDParams{Provider: provider, ExternalID: externalID})
	if errors.Is(err, sql.ErrNoRows) {
		return KnownWorkNaming{}, false, nil
	}
	if err != nil {
		return KnownWorkNaming{}, false, fmt.Errorf("library: known work: %w", err)
	}
	known := KnownWorkNaming{Title: row.CanonicalTitle, ArtistName: row.PrimaryArtistName.String}
	if row.Year.Valid {
		known.Year = &row.Year.Int64
	}
	return known, true, nil
}

// EditionRefinement is what a placeholder edition becomes once a provider has
// answered -- naming only, exactly like EditionIdentity; RefineEdition's job is
// persisting it.
type EditionRefinement struct {
	Kind            string
	Year            *int64
	Format          string
	CatalogueNumber string
	Barcode         string
	RuntimeMs       *int64
}

// PartIdentity is one part (track, episode, chapter, ...) to resolve-or-create
// under a work, mirroring WorkIdentity/EditionIdentity's naming-only shape.
type PartIdentity struct {
	MediaRef  string
	SortKey   string
	Title     string
	RuntimeMs *int64
	// ExternalID links the resolved (or created) part to a provider id, e.g. a
	// MusicBrainz recording MBID. Optional: not every source names one.
	ExternalID *PartExternalID
}

// PartExternalID is one (provider, external id) pairing for a work_part.
type PartExternalID struct {
	Provider   string
	ExternalID string
	Confidence float64
}

// WorkDateStreet is the work_date.kind this package writes a release group's
// first-release-date under -- "street" (the industry term for when something
// first became available), the closest of ADR-23's shared vocabulary
// (theatrical|digital|physical|air|street|publication) to what MusicBrainz's
// FirstReleaseDate actually means for a piece of music, which was never tied
// to one specific format.
const WorkDateStreet = "street"

// RefineEdition replaces a placeholder edition's guessed fields with real ones and
// resolves its parts, all in one transaction: an edition a concurrent scan is still
// resolving must not see half-written enrichment. workYear and workPrimaryType, if
// set, are written to the work only when the column is still empty (Invariant-shaped:
// enrichment fills a gap, it never overwrites a value already there, whoever set it).
// workReleaseDate, when non-empty, is written to work_date unconditionally (SetWorkDate's
// own upsert contract) -- there is no manual-override UI for it yet, so "the last
// provider to enrich this work" is the only source there is to protect against.
func (i Identity) RefineEdition(
	ctx context.Context, at int64, editionID, workID int64, workYear *int64, workReleaseDate, workPrimaryType string,
	refinement EditionRefinement, parts []PartIdentity,
) error {
	return i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)

		if err := queries.UpdateEdition(ctx, librarydb.UpdateEditionParams{
			ID: editionID, Kind: refinement.Kind, Year: nullInt64(refinement.Year),
			Format:          nullString(refinement.Format),
			CatalogueNumber: nullString(refinement.CatalogueNumber),
			Barcode:         nullString(refinement.Barcode),
			RuntimeMs:       nullInt64(refinement.RuntimeMs),
		}); err != nil {
			return fmt.Errorf("update edition: %w", err)
		}

		if err := refineWork(ctx, queries, at, workID, workYear, workReleaseDate, workPrimaryType); err != nil {
			return err
		}

		for _, part := range parts {
			if err := resolvePart(ctx, queries, workID, part); err != nil {
				return fmt.Errorf("part %s: %w", part.MediaRef, err)
			}
		}
		return nil
	})
}

// RefineWork is RefineEdition's work-level half alone, for a work identified
// at release-group level with no edition to refine: year and primary type fill
// only an empty column, the release date is upserted.
func (i Identity) RefineWork(
	ctx context.Context, at, workID int64, workYear *int64, workReleaseDate, workPrimaryType string,
) error {
	return i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return refineWork(ctx, librarydb.New(tx), at, workID, workYear, workReleaseDate, workPrimaryType)
	})
}

func refineWork(
	ctx context.Context, queries *librarydb.Queries, at, workID int64,
	workYear *int64, workReleaseDate, workPrimaryType string,
) error {
	if workYear != nil {
		if err := queries.UpdateWorkYearIfNull(ctx, librarydb.UpdateWorkYearIfNullParams{
			ID: workID, Year: nullInt64(workYear), UpdatedAt: at,
		}); err != nil {
			return fmt.Errorf("update work year: %w", err)
		}
	}
	if workPrimaryType != "" {
		if err := queries.UpdateWorkPrimaryTypeIfNull(ctx, librarydb.UpdateWorkPrimaryTypeIfNullParams{
			ID: workID, PrimaryType: nullString(workPrimaryType), UpdatedAt: at,
		}); err != nil {
			return fmt.Errorf("update work primary type: %w", err)
		}
	}
	if workReleaseDate != "" {
		if err := queries.SetWorkDate(ctx, librarydb.SetWorkDateParams{
			WorkID: workID, Kind: WorkDateStreet, Region: "",
			Date: workReleaseDate, Source: "musicbrainz",
		}); err != nil {
			return fmt.Errorf("set work date: %w", err)
		}
	}
	return nil
}

// EditionlessWork is a work identified at release-group level only -- by the
// text search, which never picks an edition -- that has not been enriched yet.
type EditionlessWork struct {
	WorkID           int64
	ReleaseGroupMBID string
}

// EditionlessWorks returns up to pageSize works after afterWorkID with a
// release-group id, no edition and no credit yet. Enrichment used to walk
// placeholder editions only, so these never gained an artist, genres or a
// description (found 2026-09-17).
func (i Identity) EditionlessWorks(ctx context.Context, afterWorkID, pageSize int64) ([]EditionlessWork, error) {
	rows, err := librarydb.New(i.store.Read()).ListEditionlessWorks(ctx,
		librarydb.ListEditionlessWorksParams{AfterID: afterWorkID, PageSize: pageSize})
	if err != nil {
		return nil, fmt.Errorf("library: editionless works: %w", err)
	}
	works := make([]EditionlessWork, len(rows))
	for index, row := range rows {
		works[index] = EditionlessWork{WorkID: row.WorkID, ReleaseGroupMBID: row.ReleaseGroupMbid}
	}
	return works, nil
}

// SetWorkDescription writes a work's free-text description, unconditionally
// (unlike the year in RefineEdition, nothing else writes this column yet, so
// there is no earlier value to protect). A separate method, not folded into
// RefineEdition: the description is work-level, sourced independently
// (Wikidata, not the release group's own fields), and not every enrichment
// pass has one to write.
func (i Identity) SetWorkDescription(ctx context.Context, at, workID int64, description string) error {
	err := i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetWorkDescription(ctx, librarydb.SetWorkDescriptionParams{
			ID: workID, Description: nullString(description), UpdatedAt: at,
		})
	})
	if err != nil {
		return fmt.Errorf("library: set work %d description: %w", workID, err)
	}
	return nil
}

// WithdrawEdition retires a placeholder edition whose release no longer exists; any other edition is left alone.
func (i Identity) WithdrawEdition(ctx context.Context, editionID int64) error {
	err := i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).WithdrawEdition(ctx, editionID)
	})
	if err != nil {
		return fmt.Errorf("library: withdraw edition %d: %w", editionID, err)
	}
	return nil
}

// RetitleWork names a work after its release group: a first scan names it after the album tag of
// whichever file it met first, one pressing's or one night's title among several.
func (i Identity) RetitleWork(ctx context.Context, at, workID int64, title, sortTitle string) error {
	err := i.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetWorkTitle(ctx, librarydb.SetWorkTitleParams{
			CanonicalTitle: title, SortTitle: sortTitle, UpdatedAt: at, ID: workID,
		})
	})
	if err != nil {
		return fmt.Errorf("library: retitle work %d: %w", workID, err)
	}
	return nil
}

func resolvePart(ctx context.Context, queries *librarydb.Queries, workID int64, part PartIdentity) error {
	partID, err := findOrCreatePart(ctx, queries, workID, part)
	if err != nil {
		return err
	}
	if part.ExternalID == nil {
		return nil
	}
	return queries.LinkWorkPartExternalID(ctx, librarydb.LinkWorkPartExternalIDParams{
		Provider: part.ExternalID.Provider, ExternalID: part.ExternalID.ExternalID,
		PartID: partID, Confidence: part.ExternalID.Confidence,
	})
}

func findOrCreatePart(ctx context.Context, queries *librarydb.Queries, workID int64, part PartIdentity) (int64, error) {
	existing, err := queries.FindWorkPart(ctx, librarydb.FindWorkPartParams{WorkID: workID, MediaRef: part.MediaRef})
	if err == nil {
		return existing.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("find part: %w", err)
	}

	created, err := queries.CreateWorkPart(ctx, librarydb.CreateWorkPartParams{
		WorkID: workID, MediaRef: part.MediaRef, SortKey: part.SortKey,
		Title: nullString(part.Title), RuntimeMs: nullInt64(part.RuntimeMs),
		Monitored: 1,
	})
	if err != nil {
		return 0, fmt.Errorf("create part: %w", err)
	}
	return created.ID, nil
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}
