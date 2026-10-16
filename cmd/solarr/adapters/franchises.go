package adapters

import (
	"context"
	"errors"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// LibraryFranchises implements apiserver.Franchises through the library's media type.
type LibraryFranchises struct {
	Cache    *settings.Cache
	Registry *media.Registry
}

func (a LibraryFranchises) franchising(categoryID int64) (media.Franchising, error) {
	category, ok := a.Cache.Snapshot().Category(categoryID)
	if !ok {
		return nil, apiserver.ErrNoFranchise
	}
	franchising, ok := media.Capability[media.Franchising](a.Registry, media.Key(category.MediaType))
	if !ok {
		return nil, apiserver.ErrNoFranchise
	}
	return franchising, nil
}

// List implements apiserver.Franchises; a library whose type has none lists none.
func (a LibraryFranchises) List(ctx context.Context, categoryID int64) ([]gen.Franchise, error) {
	franchising, err := a.franchising(categoryID)
	if errors.Is(err, apiserver.ErrNoFranchise) {
		return []gen.Franchise{}, nil
	}
	if err != nil {
		return nil, err
	}
	franchises, err := franchising.Franchises(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	views := make([]gen.Franchise, len(franchises))
	for index, franchise := range franchises {
		views[index] = gen.Franchise{Id: franchise.ID, Name: franchise.Name, Monitored: franchise.Monitored, Members: make([]gen.FranchiseMember, len(franchise.Members))}
		if franchise.Total > 0 {
			total := franchise.Total
			views[index].Total = &total
		}
		for i, member := range franchise.Members {
			views[index].Members[i] = gen.FranchiseMember{WorkId: member.WorkID, Title: member.Title, Monitored: member.Monitored, HasFile: member.HasFile}
			if member.Year > 0 {
				year := member.Year
				views[index].Members[i].Year = &year
			}
		}
	}
	return views, nil
}

// Monitor implements apiserver.Franchises.
func (a LibraryFranchises) Monitor(ctx context.Context, categoryID, franchiseID int64, monitored bool) (int, error) {
	franchising, err := a.franchising(categoryID)
	if err != nil {
		return 0, err
	}
	added, err := franchising.MonitorFranchise(ctx, categoryID, franchiseID, monitored)
	if errors.Is(err, media.ErrNoFranchise) {
		return 0, apiserver.ErrNoFranchise
	}
	return added, err
}
