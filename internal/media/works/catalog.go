package works

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// Categories is the library settings an add reads.
type Categories interface {
	Category(id int64) (settings.Category, bool)
	ProfileFor(category settings.Category, fallback int64) int64
}

// Source is a media type's part of an add: looking its works up at the providers, knowing which it holds, and storing
// one with what the add dialog chose. Its errors are media's (ErrInvalidAdd, ErrSourceUnavailable).
type Source interface {
	// Lookup finds works for a query; the candidates name the provider id they are added by and need no LibraryID.
	Lookup(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error)
	// Held is the library's work a provider id names, false when there is none.
	Held(ctx context.Context, provider, externalID string) (int64, bool)
	// Monitor is the type's monitor options: the one an add takes when none is chosen, the one that monitors nothing,
	// and whether an option is the type's.
	Monitor() (fallback, none string, valid func(option string) bool)
	// Add stores the work the request names.
	Add(ctx context.Context, request Add) (media.AddResult, error)
}

// Add is one add, with what the catalog worked out of the request.
type Add struct {
	media.AddRequest
	Category  settings.Category
	ProfileID int64
	// Option is the monitor option, the type's own, "none" when the add is not monitored.
	Option  string
	AddedBy *int64
}

// Catalog is a media type's media.Adding: the lookup and the add dialog's choices are read the same for every type, the
// Source does the rest.
type Catalog struct {
	Source           Source
	Categories       Categories
	DefaultProfileID int64
	// AllowAgain lifts the exclusion of a work a person removed and now adds on purpose; nil lifts nothing.
	AllowAgain func(ctx context.Context, provider, externalID string) error
}

var _ media.Adding = Catalog{}

// LookupWorks implements media.Adding: the providers' works for a query, each marked when the library holds it.
func (c Catalog) LookupWorks(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	if len([]rune(lookup.Query)) < 2 {
		return nil, media.ErrQueryTooShort
	}
	candidates, err := c.Source.Lookup(ctx, lookup)
	if err != nil {
		return nil, err
	}
	for index, candidate := range candidates {
		if id, held := c.Source.Held(ctx, candidate.Provider, candidate.ExternalID); held {
			candidates[index].LibraryID = id
		}
	}
	return candidates, nil
}

// AddWork implements media.Adding: the work with the dialog's monitor option and profile. It starts no search; the
// caller searches the work when asked to.
func (c Catalog) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	category, ok := c.Categories.Category(request.CategoryID)
	if !ok {
		return media.AddResult{}, fmt.Errorf("%w: no such library", media.ErrInvalidAdd)
	}
	fallback, none, valid := c.Source.Monitor()
	option := fallback
	if request.Option != "" {
		option = request.Option
	}
	if !request.Monitored {
		option = none
	}
	if !valid(option) {
		return media.AddResult{}, fmt.Errorf("%w: %q is not a monitor choice", media.ErrInvalidAdd, option)
	}
	profile := c.Categories.ProfileFor(category, c.DefaultProfileID)
	if request.ProfileID != nil {
		profile = *request.ProfileID
	}
	if c.AllowAgain != nil {
		if err := c.AllowAgain(ctx, request.Provider, request.ExternalID); err != nil {
			return media.AddResult{}, err
		}
	}
	var addedBy *int64
	if request.UserID != 0 {
		addedBy = &request.UserID
	}
	return c.Source.Add(ctx, Add{AddRequest: request, Category: category, ProfileID: profile, Option: option, AddedBy: addedBy})
}
