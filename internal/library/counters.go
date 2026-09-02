// Package library reads and writes library.db. It owns the generated librarydb
// queries, as internal/jobs owns jobsdb and internal/settings owns settingsdb.
package library

import (
	"context"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// Counters reads the counter table. Every library figure a dashboard shows is a
// lookup here, never a live aggregate over the library (section 21.12, ADR-28).
type Counters struct {
	store *store.Store
}

// NewCounters builds a reader over an open library store.
func NewCounters(opened *store.Store) Counters { return Counters{store: opened} }

// Counters returns metric to value for one scope. A metric that has never been
// counted is absent rather than zero, so a caller can tell "none" from "not yet
// counted" if it needs to.
func (c Counters) Counters(ctx context.Context, scope string) (map[string]int64, error) {
	rows, err := librarydb.New(c.store.Read()).ListCountersForScope(ctx, scope)
	if err != nil {
		return nil, err
	}

	values := make(map[string]int64, len(rows))
	for _, row := range rows {
		values[row.Metric] = row.Value
	}
	return values, nil
}
