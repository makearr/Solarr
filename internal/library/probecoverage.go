package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// probeCoverageScope is the counter scope probing reconciles into -- a single
// library-wide figure rather than per-category, since a media_attributes row
// carries no category of its own (unlike CategoryCounters' per-category
// "works"/"files", which read from work_root_folder).
const probeCoverageScope = "library"

// ProbeCoverage recomputes how many files have a measured attribute row
// (ADR-39) against how many exist at all, the same drift-and-repair shape
// CategoryCounters already uses for "works"/"files" -- periodic recompute is
// the design here too, not a fallback for a write path that forgot to update
// a counter inline.
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
