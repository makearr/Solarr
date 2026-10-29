package tv

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

// maxAniListSearches bounds the Skyhook searches one AniList entry may cost: its English and romaji titles.
const maxAniListSearches = 2

// Lists is TV's part of an import list: AniList's and TMDB's lists, each entry named by TheTVDB's or TMDB's id.
type Lists struct {
	// Service finds an AniList entry's series at Skyhook; its zero value resolves only TMDB entries.
	Service Service
}

var _ media.ListResolving = Lists{}

// ImportListTypes implements media.ListResolving.
func (Lists) ImportListTypes() []media.ListType {
	return []media.ListType{media.ListAniListSeason, media.ListAniListUser, media.ListTMDBList, media.ListTMDBTrending, media.ListTMDBPopular}
}

// aniListFilms are AniList's formats that are no series.
var aniListFilms = []string{"MOVIE", "MUSIC"}

// ResolveListEntry implements media.ListResolving: a TMDB show is added by its TMDB id, an AniList entry by the
// TheTVDB series Skyhook lists it under.
func (l Lists) ResolveListEntry(ctx context.Context, entry media.ListEntry) (media.ListMatch, error) {
	for _, id := range entry.IDs {
		switch id.Provider {
		case ProviderTVDB:
			return media.ListMatch{ProviderID: id, Found: true}, nil
		case ProviderTMDB:
			if entry.Form != tmdb.MediaTV {
				return media.ListMatch{Foreign: true}, nil
			}
			return media.ListMatch{ProviderID: id, Found: true}, nil
		case ProviderAniList:
			if slices.Contains(aniListFilms, entry.Form) {
				return media.ListMatch{Foreign: true}, nil
			}
			return l.resolveAniList(ctx, id.ExternalID, entry)
		}
	}
	return media.ListMatch{}, nil
}

// resolveAniList searches Skyhook by the entry's names for the series whose AniList ids hold the entry's.
func (l Lists) resolveAniList(ctx context.Context, externalID string, entry media.ListEntry) (media.ListMatch, error) {
	id, valid := ParseID(externalID)
	if !valid {
		return media.ListMatch{}, nil
	}
	if l.Service.Skyhook == nil {
		return media.ListMatch{}, fmt.Errorf("%w: no series source to find an AniList entry by", media.ErrSourceUnavailable)
	}
	var terms []string
	for _, name := range append([]string{entry.Title}, entry.AltTitles...) {
		if name != "" && !slices.Contains(terms, name) && len(terms) < maxAniListSearches {
			terms = append(terms, name)
		}
	}
	for _, term := range terms {
		hits, err := l.Service.Skyhook.Search(ctx, term)
		if err != nil {
			return media.ListMatch{}, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
		}
		for _, hit := range hits {
			if hit.TVDBID > 0 && slices.Contains(hit.AniListIDs, id) {
				return media.ListMatch{ProviderID: media.ProviderID{Provider: ProviderTVDB, ExternalID: strconv.FormatInt(hit.TVDBID, 10)}, Found: true}, nil
			}
		}
	}
	return media.ListMatch{}, nil
}
