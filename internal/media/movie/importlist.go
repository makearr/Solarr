package movie

import (
	"context"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

// ImportListTypes implements media.ListResolving: TMDB's lists, and MDBList's, which also carry IMDb lists.
func (Module) ImportListTypes() []media.ListType {
	return []media.ListType{media.ListTMDBList, media.ListMDBList}
}

// ResolveListEntry implements media.ListResolving: a film is added by its TMDB id; a show on a mixed list is foreign.
func (Module) ResolveListEntry(_ context.Context, entry media.ListEntry) (media.ListMatch, error) {
	if entry.Form != tmdb.MediaMovie {
		return media.ListMatch{Foreign: true}, nil
	}
	for _, id := range entry.IDs {
		if id.Provider == "tmdb" {
			return media.ListMatch{ProviderID: media.ProviderID{Provider: ProviderTMDB, ExternalID: id.ExternalID}, Found: true}, nil
		}
	}
	return media.ListMatch{}, nil
}
