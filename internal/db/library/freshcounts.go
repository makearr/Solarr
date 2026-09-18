package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// freshCountsScope is the counter scope the fresh-works sweep reconciles
// into -- library-wide, the same simplification the dashboard's other
// whole-library tiles (probeCoverageScope) already make rather than a
// per-category breakdown ADR-55's own dashboard panel does not ask for.
const freshCountsScope = "library"

// freshCountsMetric is the counter row's own metric name within that scope.
const freshCountsMetric = "fresh"

// FreshCounts recomputes ADR-55's dashboard "new arrivals" figure: how many
// works carry a release date inside the freshness window. A counter-backed
// lookup, never a live aggregate (section 21.12) -- the same drift-and-repair
// shape ProbeCoverage already uses for its own single library-wide figure.
type FreshCounts struct {
	store *store.Store
	clock clock.Clock
}

// NewFreshCounts builds it over an open library store.
func NewFreshCounts(opened *store.Store, cl clock.Clock) FreshCounts {
	return FreshCounts{store: opened, clock: cl}
}

// Reconcile recomputes "fresh" and writes it back if it does not already
// match, returning the correction made (nil when nothing drifted). since is
// the same already-resolved cutoff date WorkFilter.FreshSince takes -- this
// package never calls time.Now() (forbidigo), so the caller resolves it the
// same way cmd/solarr/adapters.Works already does for the Library
// list's own fresh=true filter, and the two can never disagree about what
// "fresh" means.
func (c FreshCounts) Reconcile(ctx context.Context, since string) ([]Correction, error) {
	reader := librarydb.New(c.store.Read())
	fresh, err := reader.CountFreshWorks(ctx, librarydb.CountFreshWorksParams{Kind: WorkDateStreet, Date: since})
	if err != nil {
		return nil, fmt.Errorf("library: fresh counts: %w", err)
	}

	existingRows, err := reader.ListCountersForScope(ctx, freshCountsScope)
	if err != nil {
		return nil, fmt.Errorf("library: fresh counts: %w", err)
	}
	var was int64
	var wasSet bool
	for _, row := range existingRows {
		if row.Metric == freshCountsMetric {
			was, wasSet = row.Value, true
			break
		}
	}
	if wasSet && was == fresh {
		return nil, nil
	}

	now := c.clock.Now().Unix()
	err = c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetCounter(ctx, librarydb.SetCounterParams{
			Scope: freshCountsScope, Metric: freshCountsMetric, Value: fresh, UpdatedAt: now,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("library: fresh counts: %w", err)
	}
	return []Correction{{Scope: freshCountsScope, Metric: freshCountsMetric, Was: was, WasSet: wasSet, Now: fresh}}, nil
}
