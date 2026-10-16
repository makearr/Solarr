package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ReleaseFilters are a work's own rules for the releases offered for it (work_release_filter).
type ReleaseFilters struct{ store *store.Store }

// NewReleaseFilters builds it over an open library store.
func NewReleaseFilters(opened *store.Store) ReleaseFilters { return ReleaseFilters{store: opened} }

// ReleaseFilter is one stored rule; Prefix narrows it to part of the work ('s03'), "" to all of it.
type ReleaseFilter struct {
	Prefix  string
	Kind    string
	Value   string
	IsRegex bool
	Score   int64
}

// List answers a work's rules in the order they were made.
func (r ReleaseFilters) List(ctx context.Context, workID int64) ([]ReleaseFilter, error) {
	rows, err := librarydb.New(r.store.Read()).ListWorkReleaseFilters(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: list release filters: %w", err)
	}
	filters := make([]ReleaseFilter, len(rows))
	for i, row := range rows {
		filters[i] = ReleaseFilter{Prefix: row.MediaRefPrefix, Kind: row.Kind, Value: row.Value, IsRegex: row.IsRegex == 1, Score: row.Score}
	}
	return filters, nil
}

// Replace makes the given rules the work's whole set.
func (r ReleaseFilters) Replace(ctx context.Context, workID int64, filters []ReleaseFilter) error {
	err := r.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.DeleteWorkReleaseFilters(ctx, workID); err != nil {
			return err
		}
		for _, filter := range filters {
			regex := int64(0)
			if filter.IsRegex {
				regex = 1
			}
			if err := queries.CreateWorkReleaseFilter(ctx, librarydb.CreateWorkReleaseFilterParams{
				WorkID: workID, MediaRefPrefix: filter.Prefix, Kind: filter.Kind, Value: filter.Value, IsRegex: regex,
				Score: filter.Score, CreatedAt: r.store.Now().Unix(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: replace release filters: %w", err)
	}
	return nil
}
