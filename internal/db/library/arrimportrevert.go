package library

import (
	"context"
	"errors"
	"fmt"
)

// ErrArrImportNotDone is the run is still RUNNING or already REVERTED, so a revert refuses rather than guesses.
var ErrArrImportNotDone = errors.New("library: arr import is not in a revertible state")

// ArrImportRevertResult is what one revert actually undid.
type ArrImportRevertResult struct {
	WorksDeleted         int
	SubscriptionsDeleted int
	PeopleDeleted        int
	// Errors lists per-item failures a best-effort revert tolerated, such as a work still referenced elsewhere or a person still credited; reported, not absorbed.
	Errors []error
}

// ArrImportRevert undoes one *Arr run, touching only rows its arr_import_item records as *_created. Order matters: created works first (clearing work.lead_person_id), then each person's subscription, then the person; a person still credited elsewhere is a tolerated per-item failure.
type ArrImportRevert struct {
	imports       ArrImports
	identity      Identity
	people        People
	subscriptions Subscriptions
}

// NewArrImportRevert builds one over the stores it composes.
func NewArrImportRevert(imports ArrImports, identity Identity, people People, subscriptions Subscriptions) ArrImportRevert {
	return ArrImportRevert{imports: imports, identity: identity, people: people, subscriptions: subscriptions}
}

// Revert undoes importID best-effort per item and marks the run REVERTED regardless; a re-run only retries what is left, as each undo is idempotent.
func (r ArrImportRevert) Revert(ctx context.Context, importID int64) (ArrImportRevertResult, error) {
	run, err := r.imports.Get(ctx, importID)
	if err != nil {
		return ArrImportRevertResult{}, fmt.Errorf("library: revert arr import %d: %w", importID, err)
	}
	if run.State != ArrImportDone && run.State != ArrImportFailed {
		return ArrImportRevertResult{}, fmt.Errorf("library: revert arr import %d: %w", importID, ErrArrImportNotDone)
	}

	items, err := r.imports.ListItems(ctx, importID)
	if err != nil {
		return ArrImportRevertResult{}, fmt.Errorf("library: revert arr import %d: %w", importID, err)
	}

	var result ArrImportRevertResult
	for _, item := range items {
		if !item.WorkCreated || item.WorkID == nil {
			continue
		}
		if err := r.identity.DeleteWork(ctx, *item.WorkID); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("item %d (%s): %w", item.ID, item.Title, err))
			continue
		}
		result.WorksDeleted++
	}
	for _, item := range items {
		if !item.PersonCreated || item.PersonID == nil {
			continue
		}
		if item.SubscriptionID != nil {
			if err := r.subscriptions.Delete(ctx, *item.SubscriptionID); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("item %d (%s) subscription: %w", item.ID, item.Title, err))
				continue
			}
			result.SubscriptionsDeleted++
		}
		if err := r.people.Delete(ctx, *item.PersonID); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("item %d (%s) person: %w", item.ID, item.Title, err))
			continue
		}
		result.PeopleDeleted++
	}

	if err := r.imports.setState(ctx, importID, ArrImportReverted); err != nil {
		return result, fmt.Errorf("library: revert arr import %d: mark reverted: %w", importID, err)
	}
	return result, nil
}
