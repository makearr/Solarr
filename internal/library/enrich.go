package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
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

// PlaceholderEditions returns up to limit editions still waiting on real metadata,
// oldest first. There is no cursor: a successfully refined edition's kind moves
// away from 'unknown', which is what makes the query's own result set shrink as
// enrichment succeeds, so nothing needs remembering between calls.
func (i Identity) PlaceholderEditions(ctx context.Context, limit int) ([]PlaceholderEdition, error) {
	rows, err := librarydb.New(i.store.Read()).ListPlaceholderEditions(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("library: placeholder editions: %w", err)
	}
	out := make([]PlaceholderEdition, len(rows))
	for index, row := range rows {
		out[index] = PlaceholderEdition{EditionID: row.ID, WorkID: row.WorkID, ExternalID: row.Label}
	}
	return out, nil
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

// RefineEdition replaces a placeholder edition's guessed fields with real ones and
// resolves its parts, all in one transaction: an edition a concurrent scan is still
// resolving must not see half-written enrichment. workYear, if non-nil, is set on
// the work only when the column is still empty (Invariant-shaped: enrichment fills
// a gap, it never overwrites a value already there, whoever set it).
func (i Identity) RefineEdition(
	ctx context.Context, at int64, editionID, workID int64, workYear *int64,
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

		if workYear != nil {
			if err := queries.UpdateWorkYearIfNull(ctx, librarydb.UpdateWorkYearIfNullParams{
				ID: workID, Year: nullInt64(workYear), UpdatedAt: at,
			}); err != nil {
				return fmt.Errorf("update work year: %w", err)
			}
		}

		for _, part := range parts {
			if err := resolvePart(ctx, queries, workID, part); err != nil {
				return fmt.Errorf("part %s: %w", part.MediaRef, err)
			}
		}
		return nil
	})
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
