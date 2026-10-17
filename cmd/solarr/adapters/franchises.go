package adapters

import (
	"context"
	"errors"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// picturesPerList bounds the provider calls one listing makes for franchise pictures; the rest follow next time.
const picturesPerList = 15

// LibraryFranchises implements apiserver.Franchises: collections are one table for every media type, the provider's
// part (what a franchise lacks, its picture, following it) comes from the media type when it has one.
type LibraryFranchises struct {
	Cache    *settings.Cache
	Registry *media.Registry
	Store    library.Franchises
}

func (a LibraryFranchises) category(categoryID int64) (settings.Category, error) {
	category, ok := a.Cache.Snapshot().Category(categoryID)
	if !ok {
		return settings.Category{}, apiserver.ErrNoFranchise
	}
	return category, nil
}

func (a LibraryFranchises) capability(category settings.Category) (media.Franchising, bool) {
	return media.Capability[media.Franchising](a.Registry, media.Key(category.MediaType))
}

// List implements apiserver.Franchises.
func (a LibraryFranchises) List(ctx context.Context, categoryID int64) ([]gen.Franchise, error) {
	category, err := a.category(categoryID)
	if errors.Is(err, apiserver.ErrNoFranchise) {
		return []gen.Franchise{}, nil
	}
	rows, err := a.Store.InCategory(ctx, category.MediaType, categoryID)
	if err != nil {
		return nil, err
	}
	if provider, ok := a.capability(category); ok {
		var asked []media.FranchiseRef
		for _, row := range rows {
			if row.PosterURL == "" && row.Kind == library.FranchiseKind && len(asked) < picturesPerList {
				asked = append(asked, media.FranchiseRef{ID: row.ID, ExternalID: row.ExternalID})
			}
		}
		if len(asked) > 0 {
			pictures := provider.FranchisePictures(ctx, asked)
			for index := range rows {
				if poster := pictures[rows[index].ID]; poster != "" {
					if err := a.Store.SetPoster(ctx, rows[index].ID, poster); err == nil {
						rows[index].PosterURL = poster
					}
				}
			}
		}
	}
	views := make([]gen.Franchise, len(rows))
	for index, row := range rows {
		views[index] = franchiseView(row, nil)
	}
	return views, nil
}

// Get implements apiserver.Franchises.
func (a LibraryFranchises) Get(ctx context.Context, categoryID, franchiseID int64) (gen.Franchise, error) {
	category, err := a.category(categoryID)
	if err != nil {
		return gen.Franchise{}, err
	}
	rows, err := a.Store.InCategory(ctx, category.MediaType, categoryID)
	if err != nil {
		return gen.Franchise{}, err
	}
	for _, row := range rows {
		if row.ID != franchiseID {
			continue
		}
		var missing []media.FranchiseMember
		if provider, ok := a.capability(category); ok && row.Kind == library.FranchiseKind {
			if missing, err = provider.MissingFranchiseMembers(ctx, categoryID, franchiseID); err != nil && !errors.Is(err, media.ErrNotWired) {
				return gen.Franchise{}, err
			}
		}
		return franchiseView(row, missing), nil
	}
	return gen.Franchise{}, apiserver.ErrNoFranchise
}

// Create implements apiserver.Franchises.
func (a LibraryFranchises) Create(ctx context.Context, categoryID int64, name string) (gen.Franchise, error) {
	category, err := a.category(categoryID)
	if err != nil {
		return gen.Franchise{}, err
	}
	id, err := a.Store.Create(ctx, category.MediaType, categoryID, name)
	if err != nil {
		return gen.Franchise{}, err
	}
	return franchiseView(library.FranchiseRow{ID: id, Name: name, Kind: library.CustomKind}, nil), nil
}

func editable(err error) error {
	if errors.Is(err, library.ErrNotCustomCollection) {
		return apiserver.ErrFranchiseNotEditable
	}
	return err
}

// Rename implements apiserver.Franchises.
func (a LibraryFranchises) Rename(ctx context.Context, _, franchiseID int64, name string) error {
	return editable(a.Store.Rename(ctx, franchiseID, name))
}

// Delete implements apiserver.Franchises.
func (a LibraryFranchises) Delete(ctx context.Context, _, franchiseID int64) error {
	return editable(a.Store.Delete(ctx, franchiseID))
}

// AddMember implements apiserver.Franchises.
func (a LibraryFranchises) AddMember(ctx context.Context, _, franchiseID, workID int64) error {
	return editable(a.Store.AddMember(ctx, franchiseID, workID))
}

// RemoveMember implements apiserver.Franchises.
func (a LibraryFranchises) RemoveMember(ctx context.Context, _, franchiseID, workID int64) error {
	return editable(a.Store.RemoveMember(ctx, franchiseID, workID))
}

// Monitor implements apiserver.Franchises.
func (a LibraryFranchises) Monitor(ctx context.Context, categoryID, franchiseID int64, monitored bool) (int, error) {
	category, err := a.category(categoryID)
	if err != nil {
		return 0, err
	}
	provider, ok := a.capability(category)
	if !ok {
		return 0, apiserver.ErrNoFranchise
	}
	added, err := provider.MonitorFranchise(ctx, categoryID, franchiseID, monitored)
	if errors.Is(err, media.ErrNoFranchise) {
		return 0, apiserver.ErrNoFranchise
	}
	return added, err
}

// franchiseView maps a collection with the works the library holds, and those it lacks when the provider lists them.
func franchiseView(row library.FranchiseRow, missing []media.FranchiseMember) gen.Franchise {
	kind := gen.FranchiseKind(row.Kind)
	if kind != gen.FranchiseKindCustom {
		kind = gen.FranchiseKindFranchise
	}
	view := gen.Franchise{Id: row.ID, Name: row.Name, Kind: kind, Monitored: row.Monitored, Members: make([]gen.FranchiseMember, 0, len(row.Members)+len(missing))}
	setString(&view.PosterUrl, row.PosterURL)
	if row.Total > 0 {
		total := int(row.Total)
		view.Total = &total
	}
	for _, member := range row.Members {
		workID := member.WorkID
		entry := gen.FranchiseMember{WorkId: &workID, Title: member.Title, Monitored: member.Monitored, HasFile: member.Files > 0}
		if member.Year > 0 {
			year := int(member.Year)
			entry.Year = &year
		}
		view.Members = append(view.Members, entry)
	}
	for _, member := range missing {
		entry := gen.FranchiseMember{Title: member.Title}
		if member.Year > 0 {
			year := member.Year
			entry.Year = &year
		}
		setString(&entry.Provider, member.Provider)
		setString(&entry.ExternalId, member.ExternalID)
		setString(&entry.PosterUrl, member.PosterURL)
		view.Members = append(view.Members, entry)
	}
	return view
}
