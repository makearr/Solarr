package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
)

// WorkCoverage implements apiserver.WorkCoverage for the works of media types whose parts can stand for others.
type WorkCoverage struct {
	Works    library.Works
	Coverage library.Coverage
	Registry *media.Registry
}

func (a WorkCoverage) requireCoverage(ctx context.Context, workID int64) error {
	mediaType, err := a.Works.MediaTypeOf(ctx, workID)
	if errors.Is(err, sql.ErrNoRows) {
		return apiserver.ErrSeriesNotFound
	}
	if err != nil {
		return err
	}
	if _, covers := media.Capability[media.PartCoverage](a.Registry, media.Key(mediaType)); !covers {
		return apiserver.ErrSeriesNotFound
	}
	return nil
}

// Get implements apiserver.WorkCoverage.
func (a WorkCoverage) Get(ctx context.Context, workID int64) (gen.WorkCoverage, error) {
	if err := a.requireCoverage(ctx, workID); err != nil {
		return gen.WorkCoverage{}, err
	}
	refs, err := a.Coverage.PartRefs(ctx, workID)
	if err != nil {
		return gen.WorkCoverage{}, err
	}
	rows, err := a.Coverage.OfEdition(ctx, workID)
	if err != nil {
		return gen.WorkCoverage{}, err
	}
	view := gen.WorkCoverage{EditionParts: refs, Items: make([]gen.CoverageItem, len(rows))}
	for i, row := range rows {
		view.Items[i] = gen.CoverageItem{EditionRef: row.EditionRef, WorkId: row.CoveredWorkID, Title: row.CoveredTitle, MediaRef: row.CoveredRef, Source: row.Source}
	}
	return view, nil
}

// Replace implements apiserver.WorkCoverage.
func (a WorkCoverage) Replace(ctx context.Context, workID int64, editionRef string, covered []gen.CoveredPartRef) (int, error) {
	if err := a.requireCoverage(ctx, workID); err != nil {
		return 0, err
	}
	parts := make([]library.CoveredPart, len(covered))
	for i, one := range covered {
		parts[i] = library.CoveredPart{WorkID: one.WorkId, MediaRef: one.MediaRef}
	}
	skipped, err := a.Coverage.Replace(ctx, workID, editionRef, library.CoverageSourceHand, parts)
	if errors.Is(err, library.ErrNoEditionPart) {
		return 0, fmt.Errorf("%w: this work has no part %q", apiserver.ErrInvalidChoice, editionRef)
	}
	return skipped, err
}
