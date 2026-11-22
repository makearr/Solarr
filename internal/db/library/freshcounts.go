package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// freshCountsScope is the library-wide counter scope, like probeCoverageScope, as ADR-55's dashboard panel needs no per-category figure.
const freshCountsScope = "library"

// freshCountsMetric is the counter row's own metric name within that scope.
const freshCountsMetric = "fresh"

// FreshCounts recomputes ADR-55's "new arrivals" figure (works with a release date in the freshness window) as a counter, never a live aggregate (section 21.12).
type FreshCounts struct {
	store *store.Store
	clock clock.Clock
}

// NewFreshCounts builds it over an open library store.
func NewFreshCounts(opened *store.Store, cl clock.Clock) FreshCounts {
	return FreshCounts{store: opened, clock: cl}
}

// Reconcile recomputes "fresh" and writes back a correction, nil when none; since is the caller-resolved cutoff shared with WorkFilter.FreshSince, as time.Now is forbidden here and both must agree.
func (c FreshCounts) Reconcile(ctx context.Context, since string, rssSince int64) ([]Correction, error) {
	reader := librarydb.New(c.store.Read())
	fresh, err := reader.CountFreshWorks(ctx, librarydb.CountFreshWorksParams{Kind: WorkDateStreet, Date: since, RssSince: rssSince})
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
