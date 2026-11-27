package book

import (
	"context"

	"github.com/makearr/Solarr/internal/media"
)

// ImportListTypes implements media.ListResolving: a reader's Open Library list, for ebook and audiobook libraries alike.
func (Module) ImportListTypes() []media.ListType {
	return []media.ListType{media.ListOpenLibrary, media.ListHardcover}
}

// ResolveListEntry implements media.ListResolving: a work is added by its Open Library id.
func (m Module) ResolveListEntry(_ context.Context, entry media.ListEntry) (media.ListMatch, error) {
	for _, id := range entry.IDs {
		if _, ok := m.Format.NamedIDs(id.Provider, id.ExternalID); ok && (id.Provider == ProviderOpenLibrary || id.Provider == ProviderHardcover) {
			return media.ListMatch{ProviderID: id, Found: true}, nil
		}
	}
	return media.ListMatch{}, nil
}
