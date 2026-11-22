package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// probeCoverageScope is one library-wide counter scope, since a media_attributes row carries no category.
const probeCoverageScope = "library"

// ProbeCoverage recomputes files with a measured attribute row against all files, drift-and-repair like CategoryCounters; periodic recompute is the design.
type ProbeCoverage struct {
	store *store.Store
	clock clock.Clock
}

// NewProbeCoverage builds it over an open library store.
func NewProbeCoverage(opened *store.Store, cl clock.Clock) ProbeCoverage {
	return ProbeCoverage{store: opened, clock: cl}
}

// Reconcile recomputes "probed" and "total_files" and writes back either
// counter that does not already match, returning every correction made.
func (c ProbeCoverage) Reconcile(ctx context.Context) ([]Correction, error) {
	reader := librarydb.New(c.store.Read())

	measuredRows, err := reader.CountMeasuredByRootFolder(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: probe coverage: %w", err)
	}
	var probed int64
	for _, row := range measuredRows {
		probed += row.Total
	}

	totalRows, err := reader.CountLibraryPathsByRootFolder(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: probe coverage: %w", err)
	}
	var total int64
	for _, row := range totalRows {
		total += row.Total
	}

	truth := map[string]int64{"probed": probed, "total_files": total}

	existingRows, err := reader.ListCountersForScope(ctx, probeCoverageScope)
	if err != nil {
		return nil, fmt.Errorf("library: probe coverage: %w", err)
	}
	stored := make(map[string]int64, len(existingRows))
	for _, row := range existingRows {
		stored[row.Metric] = row.Value
	}

	now := c.clock.Now().Unix()
	var corrections []Correction
	err = c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		writer := librarydb.New(tx)
		for metric, value := range truth {
			was, wasSet := stored[metric]
			if wasSet && was == value {
				continue
			}
			if err := writer.SetCounter(ctx, librarydb.SetCounterParams{
				Scope: probeCoverageScope, Metric: metric, Value: value, UpdatedAt: now,
			}); err != nil {
				return err
			}
			corrections = append(corrections, Correction{
				Scope: probeCoverageScope, Metric: metric, Was: was, WasSet: wasSet, Now: value,
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("library: probe coverage: %w", err)
	}
	return corrections, nil
}
