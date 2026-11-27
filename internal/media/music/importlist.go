package music

import (
	"context"

	"github.com/makearr/Solarr/internal/media"
)

// ImportListTypes implements media.ListResolving: the albums a ListenBrainz user listened to most.
func (Module) ImportListTypes() []media.ListType {
	return []media.ListType{media.ListListenBrainzTop, media.ListMusicBrainzCollection}
}

// ResolveListEntry implements media.ListResolving: a release group is added by its MusicBrainz id.
func (Module) ResolveListEntry(_ context.Context, entry media.ListEntry) (media.ListMatch, error) {
	for _, id := range entry.IDs {
		if id.Provider == ProviderReleaseGroup && id.ExternalID != "" {
			return media.ListMatch{ProviderID: id, Found: true}, nil
		}
	}
	return media.ListMatch{}, nil
}
