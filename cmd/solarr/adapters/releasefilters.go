package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
)

// WorkReleaseFilters implements apiserver.WorkReleaseFilters for TV works.
type WorkReleaseFilters struct {
	Works   library.Works
	Filters library.ReleaseFilters
}

func (a WorkReleaseFilters) requireSeries(ctx context.Context, workID int64) error {
	mediaType, err := a.Works.MediaTypeOf(ctx, workID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && mediaType != "tv" {
		return apiserver.ErrSeriesNotFound
	}
	return err
}

// List implements apiserver.WorkReleaseFilters.
func (a WorkReleaseFilters) List(ctx context.Context, workID int64) ([]gen.ReleaseFilter, error) {
	if err := a.requireSeries(ctx, workID); err != nil {
		return nil, err
	}
	stored, err := a.Filters.List(ctx, workID)
	if err != nil {
		return nil, err
	}
	views := make([]gen.ReleaseFilter, len(stored))
	for i, filter := range stored {
		regex, score, prefix := filter.IsRegex, int(filter.Score), filter.Prefix
		views[i] = gen.ReleaseFilter{Kind: gen.ReleaseFilterKind(filter.Kind), Value: filter.Value, IsRegex: &regex, Score: &score, SeasonPrefix: &prefix}
	}
	return views, nil
}

// Replace implements apiserver.WorkReleaseFilters.
func (a WorkReleaseFilters) Replace(ctx context.Context, workID int64, filters []gen.ReleaseFilter) error {
	if err := a.requireSeries(ctx, workID); err != nil {
		return err
	}
	stored := make([]library.ReleaseFilter, len(filters))
	for i, filter := range filters {
		stored[i] = library.ReleaseFilter{Kind: string(filter.Kind), Value: filter.Value}
		if filter.SeasonPrefix != nil {
			stored[i].Prefix = *filter.SeasonPrefix
		}
		if filter.IsRegex != nil {
			stored[i].IsRegex = *filter.IsRegex
		}
		if filter.Score != nil {
			stored[i].Score = int64(*filter.Score)
		}
		if problem := judge.ValidFilter(judge.ReleaseFilter{Kind: stored[i].Kind, Value: stored[i].Value, IsRegex: stored[i].IsRegex}); problem != "" {
			return fmt.Errorf("%w: rule %d: %s", apiserver.ErrInvalidChoice, i+1, problem)
		}
	}
	return a.Filters.Replace(ctx, workID, stored)
}
