package media

import (
	"context"

	"github.com/makearr/Solarr/internal/acquiring/plan"
)

// Wiring is the optional services a media type's module is handed: adding, pictures, refresh and hunt. Each is nil when
// the type is not wired for it, and the module's capability then answers ErrNotWired (hunt: nothing wanted).
type Wiring struct {
	// Adder looks a work up and adds it; nil leaves the type without an add.
	Adder Adding
	// Pictures lists and keeps the providers' pictures of a work; nil leaves the poster unchosen.
	Pictures Picturing
	// Refresh reads one work again from its sources; nil leaves it unrefreshable.
	Refresh func(ctx context.Context, workID int64) (duplicate bool, err error)
	// Hunt searches for one wanted item and sends the best release; nil leaves the type unhunted.
	Hunt func(ctx context.Context, itemID int64) (plan.Outcome, error)
}

// LookupWorks implements Adding.
func (w Wiring) LookupWorks(ctx context.Context, lookup AddLookup) ([]AddCandidate, error) {
	if w.Adder == nil {
		return nil, ErrNotWired
	}
	return w.Adder.LookupWorks(ctx, lookup)
}

// AddWork implements Adding.
func (w Wiring) AddWork(ctx context.Context, request AddRequest) (AddResult, error) {
	if w.Adder == nil {
		return AddResult{}, ErrNotWired
	}
	return w.Adder.AddWork(ctx, request)
}

// WorkPictures implements Picturing.
func (w Wiring) WorkPictures(ctx context.Context, workID int64) ([]ImageOption, error) {
	if w.Pictures == nil {
		return nil, ErrNotWired
	}
	return w.Pictures.WorkPictures(ctx, workID)
}

// PickWorkPicture implements Picturing.
func (w Wiring) PickWorkPicture(ctx context.Context, workID int64, imageURL string) error {
	if w.Pictures == nil {
		return ErrNotWired
	}
	return w.Pictures.PickWorkPicture(ctx, workID, imageURL)
}

// RefreshWork implements Refreshing.
func (w Wiring) RefreshWork(ctx context.Context, workID int64) (bool, error) {
	if w.Refresh == nil {
		return false, ErrNotWired
	}
	return w.Refresh(ctx, workID)
}

// HuntSearch implements Hunting.
func (w Wiring) HuntSearch(ctx context.Context, itemID int64) (plan.Outcome, error) {
	if w.Hunt == nil {
		return plan.OutcomeNotWanted, nil
	}
	return w.Hunt(ctx, itemID)
}
