package media

import "context"

// ListType names one kind of import list source (ADR-38); documentation/import-lists.md lists them.
type ListType string

// The import list sources this build reads; Trakt and IMDb lists are a later source behind the same seam.
const (
	ListAniListSeason ListType = "anilist_season"
	ListAniListUser   ListType = "anilist_user"
	ListTMDBList      ListType = "tmdb_list"
	ListTMDBTrending  ListType = "tmdb_trending_tv"
	ListTMDBPopular   ListType = "tmdb_popular_tv"
	// ListStashFavourites is the newest scenes of an account's favourites at a stash-box server (adult libraries).
	ListStashFavourites ListType = "stashbox_favourites"
)

// ProviderID is one id a provider gives an entry.
type ProviderID struct {
	Provider, ExternalID string
}

// ListEntry is one entry an import list names, as its provider gives it.
type ListEntry struct {
	Title string
	Year  int
	// Form is the provider's own word for what the entry is: TMDB's "tv" or "movie", AniList's "TV" or "MOVIE".
	Form string
	// IDs are the entry's ids, the list provider's own first.
	IDs []ProviderID
	// AltTitles are the other names the provider gives it, which a lookup by name may need.
	AltTitles []string
	PosterURL string
}

// Key is the entry's identity within its list: the list provider's own id.
func (e ListEntry) Key() ProviderID {
	if len(e.IDs) == 0 {
		return ProviderID{}
	}
	return e.IDs[0]
}

// ListMatch is what a media type made of a list entry: the id it adds the work by, or why there is none.
type ListMatch struct {
	ProviderID
	Found bool
	// Foreign says the entry is not the type's at all (a film on a mixed list read into a TV library).
	Foreign bool
}

// ListResolving is the capability of a media type whose libraries import lists add to: the list types it reads and
// the work each entry is.
type ListResolving interface {
	ImportListTypes() []ListType
	// ResolveListEntry names the work the entry is, by an id the type's AddWork takes; an unknown entry is not an error.
	ResolveListEntry(ctx context.Context, entry ListEntry) (ListMatch, error)
}
