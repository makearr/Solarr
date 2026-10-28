package adapters

import (
	"context"
	"errors"
	"slices"

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
	// MonitorWork monitors a member of a hand-made collection; nil offers no monitoring of one.
	MonitorWork func(ctx context.Context, workID int64) error
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
			if (row.PosterURL == "" || row.Total == 0) && row.Kind == library.FranchiseKind && len(asked) < picturesPerList {
				asked = append(asked, media.FranchiseRef{ID: row.ID, ExternalID: row.ExternalID})
			}
		}
		if len(asked) > 0 {
			facts := provider.FranchiseFacts(ctx, asked)
			for index := range rows {
				if !slices.ContainsFunc(asked, func(ref media.FranchiseRef) bool { return ref.ID == rows[index].ID }) {
					continue
				}
				fact := facts[rows[index].ID]
				if fact.Total > 0 && int64(fact.Total) != rows[index].Total {
					if err := a.Store.SetTotal(ctx, rows[index].ID, int64(fact.Total)); err == nil {
						rows[index].Total = int64(fact.Total)
					}
				}
				// The provider had no picture: the first of its works that has one stands in, and is kept so it is not asked again.
				poster := fact.PosterURL
				if poster == "" && rows[index].PosterURL == "" {
					poster = firstPoster(rows[index].Members)
				}
				if poster != "" && rows[index].PosterURL == "" {
					if err := a.Store.SetPoster(ctx, rows[index].ID, poster); err == nil {
						rows[index].PosterURL = poster
					}
				}
			}
		}
	}
	views := make([]gen.Franchise, len(rows))
	for index, row := range rows {
		if row.PosterURL == "" {
			row.PosterURL = firstPoster(row.Members)
		}
		views[index] = franchiseView(row, nil)
	}
	return views, nil
}

// OfWork implements apiserver.Franchises.
func (a LibraryFranchises) OfWork(ctx context.Context, categoryID, workID int64) ([]gen.Franchise, error) {
	category, err := a.category(categoryID)
	if err != nil {
		return nil, err
	}
	rows, err := a.Store.InCategory(ctx, category.MediaType, categoryID)
	if err != nil {
		return nil, err
	}
	views := []gen.Franchise{}
	for _, row := range rows {
		if !slices.ContainsFunc(row.Members, func(member library.FranchiseMember) bool { return member.WorkID == workID }) {
			continue
		}
		if row.PosterURL == "" {
			row.PosterURL = firstPoster(row.Members)
		}
		views = append(views, franchiseView(row, nil))
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
		// What the provider lists is how many there are: keep it, so the list says "3 / 7" too.
		if known := int64(len(row.Members) + len(missing)); len(missing) > 0 && known != row.Total {
			if err := a.Store.SetTotal(ctx, row.ID, known); err == nil {
				row.Total = known
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

// inLibrary answers ErrNoFranchise for a collection the library does not list, the same set Get reads.
func (a LibraryFranchises) inLibrary(ctx context.Context, categoryID, franchiseID int64) error {
	category, err := a.category(categoryID)
	if err != nil {
		return err
	}
	rows, err := a.Store.InCategory(ctx, category.MediaType, categoryID)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(rows, func(row library.FranchiseRow) bool { return row.ID == franchiseID }) {
		return apiserver.ErrNoFranchise
	}
	return nil
}

// Rename implements apiserver.Franchises.
func (a LibraryFranchises) Rename(ctx context.Context, categoryID, franchiseID int64, name string) error {
	if err := a.inLibrary(ctx, categoryID, franchiseID); err != nil {
		return err
	}
	return editable(a.Store.Rename(ctx, franchiseID, name))
}

// Delete implements apiserver.Franchises: a collection made by hand is deleted; a provider's or Solarr's is deleted too,
// and its works are remembered not to belong together, so no pass makes it again.
func (a LibraryFranchises) Delete(ctx context.Context, categoryID, franchiseID int64) error {
	if err := a.inLibrary(ctx, categoryID, franchiseID); err != nil {
		return err
	}
	err := a.Store.Delete(ctx, franchiseID)
	if errors.Is(err, library.ErrNotCustomCollection) {
		return a.Store.DeleteFranchise(ctx, franchiseID)
	}
	return err
}

// AddMember implements apiserver.Franchises.
func (a LibraryFranchises) AddMember(ctx context.Context, categoryID, franchiseID, workID int64) error {
	if err := a.inLibrary(ctx, categoryID, franchiseID); err != nil {
		return err
	}
	if err := editable(a.Store.AddMember(ctx, franchiseID, workID)); err != nil {
		return err
	}
	if row, err := a.Store.Get(ctx, franchiseID); err == nil && row.Monitored && a.MonitorWork != nil {
		return a.MonitorWork(ctx, workID)
	}
	return nil
}

// RemoveMember implements apiserver.Franchises: out of a collection made by hand, or out of a provider's or Solarr's with
// a note that it does not belong with the rest.
func (a LibraryFranchises) RemoveMember(ctx context.Context, categoryID, franchiseID, workID int64) error {
	if err := a.inLibrary(ctx, categoryID, franchiseID); err != nil {
		return err
	}
	err := a.Store.RemoveMember(ctx, franchiseID, workID)
	if errors.Is(err, library.ErrNotCustomCollection) {
		return a.Store.Dismiss(ctx, franchiseID, workID)
	}
	return err
}

// Monitor implements apiserver.Franchises.
func (a LibraryFranchises) Monitor(ctx context.Context, categoryID, franchiseID int64, monitored bool) (int, error) {
	category, err := a.category(categoryID)
	if err != nil {
		return 0, err
	}
	if err := a.inLibrary(ctx, categoryID, franchiseID); err != nil {
		return 0, err
	}
	if row, err := a.Store.Get(ctx, franchiseID); err != nil {
		return 0, err
	} else if row.Kind == library.CustomKind {
		return a.monitorCustom(ctx, franchiseID, monitored)
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

// monitorCustom monitors every work of a hand-made collection, any media type, and answers how many; works put in it
// while it is monitored are monitored too. Unmonitoring leaves the works as they are, as a provider's collection does.
func (a LibraryFranchises) monitorCustom(ctx context.Context, franchiseID int64, monitored bool) (int, error) {
	if a.MonitorWork == nil {
		return 0, apiserver.ErrNoFranchise
	}
	if err := a.Store.SetMonitored(ctx, franchiseID, monitored, 0); err != nil || !monitored {
		return 0, err
	}
	members, err := a.Store.MembersOf(ctx, franchiseID)
	if err != nil {
		return 0, err
	}
	for index, workID := range members {
		if err := a.MonitorWork(ctx, workID); err != nil {
			return index, err
		}
	}
	return len(members), nil
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
		if member.Year > 0 && int(member.Year) > derefOr(view.NewestYear, 0) {
			year := int(member.Year)
			view.NewestYear = &year
		}
	}
	for _, member := range row.Members {
		workID := member.WorkID
		entry := gen.FranchiseMember{WorkId: &workID, Title: member.Title, Monitored: member.Monitored, HasFile: member.Files > 0}
		setString(&entry.PosterUrl, member.PosterURL)
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

func firstPoster(members []library.FranchiseMember) string {
	for _, member := range members {
		if member.PosterURL != "" {
			return member.PosterURL
		}
	}
	return ""
}

func derefOr(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}
