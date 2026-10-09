package library

import (
	"context"
	"errors"
	"fmt"
)

// ErrArrImportNotDone means Revert was asked to undo a run that is not in a
// state a revert makes sense for (still RUNNING, or already REVERTED) --
// the same "refuse rather than guess" rule library.ImportRevert already
// applies to a session's own state.
var ErrArrImportNotDone = errors.New("library: arr import is not in a revertible state")

// ArrImportRevertResult is what one revert actually undid.
type ArrImportRevertResult struct {
	WorksDeleted         int
	SubscriptionsDeleted int
	PeopleDeleted        int
	// Errors is every per-item failure a best-effort revert tolerated rather
	// than aborting on -- the same shape ManualCommitter.PlaceUnplaced
	// already returns for a bulk, best-effort operation. A work still
	// referenced elsewhere, or a person still credited or still named as
	// some untouched work's primary artist, is the expected shape of such a
	// failure -- reported, not silently absorbed.
	Errors []error
}

// ArrImportRevert undoes one *Arr import run: only rows the run's own
// arr_import_item records as *_created, never one it only matched -- the
// same distinction library.ImportRevert already draws between "this
// session's own work_created" and a work it merely resolved to. Composes
// ArrImports, Identity, People and Subscriptions the same way ImportRevert
// composes its own four stores, aimed at a run instead of a session.
//
// Deletion order matters: every work this run created is deleted first (so
// any primary-artist credit it set on a person about to be deleted no
// longer references that person, clearing the one foreign key --
// work.lead_person_id -- that would otherwise block it), then each
// person's own subscription (subscription.subject_id carries no FK of its
// own to clean up after), then the person itself. A person still credited
// or still primary-artist on a work this run did not create (a rare mixed
// case: a new artist credited onto an album this run only matched) is a
// real, tolerated per-item failure, not attempted to be untangled further
// here.
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

// Revert undoes importID, best-effort per item, and marks the run REVERTED
// once done regardless of whether every item's own undo succeeded -- a
// partial revert is still a revert, and re-running it would only retry
// exactly the items still left (each undo is naturally idempotent: deleting
// something already gone is the only way it could be asked for twice).
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
