package library

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// ReconcileScope names one counter scope to keep true. Scope is the counter table's
// own key ("library" for everything, "category:7" for one category); CategoryID
// drives the truth query, and zero means every category. library.db does not know
// which categories exist -- that is settings.db policy (ADR-25) -- so the caller
// supplies the scopes to check.
type ReconcileScope struct {
	Scope      string
	CategoryID int64
}

// Correction is one counter whose stored value did not match the truth, before it
// was corrected.
type Correction struct {
	Scope  string
	Metric string
	Was    int64
	WasSet bool // false when the counter had never been written at all
	Now    int64
}

// Reconciler recomputes counter from the library's own truth and corrects whatever
// has drifted. Drift is inevitable: SetCounter/AdjustCounter is never the same
// transaction as the domain write it is counting (Invariant 3 forbids holding a
// write transaction open across unrelated work), so a crash between the two loses
// the counter update but not the domain change. Migration 0004's comment on
// `counter` is explicit that the repair path is therefore not optional.
type Reconciler struct {
	store *store.Store
	clock clock.Clock
}

// NewReconciler builds a reconciler over an open library store.
func NewReconciler(opened *store.Store, cl clock.Clock) Reconciler {
	return Reconciler{store: opened, clock: cl}
}

// itemStateMetrics are the desired_item states as counter metric names, seeded at
// zero so a state with nothing left in it is still reconciled down to 0 rather than
// left however a prior count happened to leave it.
var itemStateMetrics = []string{"missing", "partial", "complete", "upgradable"}

// Reconcile recomputes every desired-item-state metric for each scope and writes
// back any counter that does not already match. It returns every correction made,
// so a caller can log or alert on drift rather than have it silently repaired.
func (r Reconciler) Reconcile(ctx context.Context, scopes []ReconcileScope) ([]Correction, error) {
	var corrections []Correction
	for _, scope := range scopes {
		found, err := r.reconcileScope(ctx, scope)
		if err != nil {
			return nil, fmt.Errorf("library: reconcile %s: %w", scope.Scope, err)
		}
		corrections = append(corrections, found...)
	}
	return corrections, nil
}

func (r Reconciler) reconcileScope(ctx context.Context, scope ReconcileScope) ([]Correction, error) {
	reader := librarydb.New(r.store.Read())

	truth, err := stateTruth(ctx, reader, scope.CategoryID)
	if err != nil {
		return nil, err
	}

	existingRows, err := reader.ListCountersForScope(ctx, scope.Scope)
	if err != nil {
		return nil, err
	}
	stored := make(map[string]int64, len(existingRows))
	for _, row := range existingRows {
		stored[row.Metric] = row.Value
	}

	now := r.clock.Now().Unix()
	var corrections []Correction
	err = r.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		writer := librarydb.New(r.store.Read()).WithTx(tx)
		for metric, value := range truth {
			was, wasSet := stored[metric]
			if wasSet && was == value {
				continue
			}
			if err := writer.SetCounter(ctx, librarydb.SetCounterParams{
				Scope: scope.Scope, Metric: metric, Value: value, UpdatedAt: now,
			}); err != nil {
				return err
			}
			corrections = append(corrections, Correction{
				Scope: scope.Scope, Metric: metric, Was: was, WasSet: wasSet, Now: value,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return corrections, nil
}

// stateTruth is the true count per desired-item-state metric, plus "monitored" as
// their sum -- the total a category's sidebar count means (section 21.1).
func stateTruth(ctx context.Context, queries *librarydb.Queries, categoryID int64) (map[string]int64, error) {
	truth := make(map[string]int64, len(itemStateMetrics)+1)
	for _, metric := range itemStateMetrics {
		truth[metric] = 0
	}
	truth["monitored"] = 0

	rows, err := queries.CountDesiredItemsByState(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		metric := strings.ToLower(row.State)
		truth[metric] = row.Total
		truth["monitored"] += row.Total
	}
	return truth, nil
}
