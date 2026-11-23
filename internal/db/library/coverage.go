package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Coverage is which parts a held part stands for: a collected edition for the issues it collects.
type Coverage struct{ store *store.Store }

// NewCoverage wraps an opened library database.
func NewCoverage(opened *store.Store) Coverage { return Coverage{store: opened} }

// CoverageSourceHand marks a row a person made; a provider's refresh never replaces it.
const CoverageSourceHand = "hand"

// CoveredPart names a part of another work by its media reference.
type CoveredPart struct {
	WorkID   int64
	MediaRef string
}

// CoverageRow is one part an edition covers.
type CoverageRow struct {
	EditionRef    string
	CoveredWorkID int64
	CoveredTitle  string
	CoveredRef    string
	Source        string
}

// ErrNoEditionPart is an edition part ref the work does not have.
var ErrNoEditionPart = errors.New("library: the edition has no such part")

// PartRefs lists the refs of a work's own parts, in order.
func (c Coverage) PartRefs(ctx context.Context, workID int64) ([]string, error) {
	parts, err := librarydb.New(c.store.Read()).ListWorkParts(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: parts of work %d: %w", workID, err)
	}
	refs := make([]string, len(parts))
	for i, part := range parts {
		refs[i] = part.MediaRef
	}
	return refs, nil
}

// Replace makes the edition's part cover exactly the given parts of this source; a part that does not exist is skipped
// and counted.
func (c Coverage) Replace(ctx context.Context, editionWorkID int64, editionRef, source string, covered []CoveredPart) (skipped int, err error) {
	err = c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		edition, err := partIDs(ctx, queries, editionWorkID)
		if err != nil {
			return err
		}
		covering, found := edition[editionRef]
		if !found {
			return fmt.Errorf("%w: work %d, part %q", ErrNoEditionPart, editionWorkID, editionRef)
		}
		if err := queries.ReplaceCoverageSource(ctx, librarydb.ReplaceCoverageSourceParams{CoveringPartID: covering, Source: source}); err != nil {
			return err
		}
		byWork := map[int64]map[string]int64{}
		for _, part := range covered {
			if part.WorkID == editionWorkID {
				skipped++
				continue
			}
			ids, loaded := byWork[part.WorkID]
			if !loaded {
				if ids, err = partIDs(ctx, queries, part.WorkID); err != nil {
					return err
				}
				byWork[part.WorkID] = ids
			}
			id, found := ids[part.MediaRef]
			if !found {
				skipped++
				continue
			}
			if err := queries.AddCoverage(ctx, librarydb.AddCoverageParams{CoveringPartID: covering, CoveredPartID: id, Source: source}); err != nil {
				return err
			}
		}
		return nil
	})
	return skipped, err
}

func partIDs(ctx context.Context, queries *librarydb.Queries, workID int64) (map[string]int64, error) {
	parts, err := queries.ListWorkParts(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: parts of work %d: %w", workID, err)
	}
	ids := make(map[string]int64, len(parts))
	for _, part := range parts {
		ids[part.MediaRef] = part.ID
	}
	return ids, nil
}

// OfEdition lists what the edition's parts cover.
func (c Coverage) OfEdition(ctx context.Context, editionWorkID int64) ([]CoverageRow, error) {
	rows, err := librarydb.New(c.store.Read()).ListCoverageByEdition(ctx, editionWorkID)
	if err != nil {
		return nil, fmt.Errorf("library: coverage of edition %d: %w", editionWorkID, err)
	}
	out := make([]CoverageRow, len(rows))
	for i, row := range rows {
		out[i] = CoverageRow{EditionRef: row.CoveringRef, CoveredWorkID: row.CoveredWorkID, CoveredTitle: row.CoveredTitle, CoveredRef: row.CoveredRef, Source: row.Source}
	}
	return out, nil
}
