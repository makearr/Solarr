// Package musicbrainz is ADR-29's provider: a Servarr metadata proxy tried first,
// direct musicbrainz.org as the fallback ADR-13's circuit breaker falls over to,
// both normalised to the same shape and cached behind internal/metadata/metacache. Direct-
// wired into internal/media/music until stage 9 extracts the MetadataProvider interface
// (architecture section 8.2), the way internal/media/music itself is.
package musicbrainz

import "context"

// ReleaseGroup is work-level data: Solarr's work_external_id provider
// "musicbrainz:release-group" (internal/media/music).
type ReleaseGroup struct {
	MBID  string
	Title string
	// PrimaryType is MusicBrainz's own vocabulary (Album, EP, Single, ...),
	// stored as-is -- interpreting it into work_classification is the caller's
	// job (ADR-23: core carries no media-type logic).
	PrimaryType string
	// FirstReleaseDate is a partial date (YYYY, YYYY-MM or YYYY-MM-DD), exactly
	// as MusicBrainz's own work_date convention allows.
	FirstReleaseDate string
	// SecondaryTypes is MusicBrainz's own vocabulary ("Compilation", "Live",
	// "Remix", "DJ-mix", "Mixtape/Street", "Soundtrack", ...), stored as-is --
	// what subscription.expand (section 19.6) filters a discography on.
	SecondaryTypes []string
	// ArtistCredit is who the release group credits, in credit order --
	// ADR-32's role="artist" credit (§19.5's "role-rich credits").
	ArtistCredit []ArtistCredit
	// Genres are the release group's own genre tags with MusicBrainz's vote
	// count, best first -- ADR-33's tag system, source "musicbrainz".
	Genres []Genre
	// WikidataQID is extracted from the release group's own "wikidata" URL
	// relation, when one exists -- ADR-8's bridge, same reasoning as
	// Artist.WikidataQID, for a work-level description rather than a
	// person's biography.
	WikidataQID string
}

// ArtistCredit is one credited artist on a release group.
type ArtistCredit struct {
	MBID string
	Name string
}

// Genre is one release group's genre tag, with MusicBrainz's own vote count --
// the closest thing to a ranking a caller has.
type Genre struct {
	Name  string
	Count int
}

// Artist is a MusicBrainz artist's own detail: ADR-32's person facts. Fetched
// separately from ReleaseGroup.ArtistCredit, which only carries a name and id
// -- person_external_id provider "musicbrainz:artist".
type Artist struct {
	MBID           string
	Name           string
	SortName       string
	Disambiguation string
	// Type is MusicBrainz's own vocabulary ("Person", "Group", "Orchestra",
	// "Choir", ...), stored as-is, same reasoning as ReleaseGroup.PrimaryType.
	Type    string
	Gender  string
	Country string
	// Area is MusicBrainz's broadest place (usually a country-level area);
	// BeginArea is the more specific "born in" location, when known.
	Area      string
	BeginArea string
	// Born and Died are partial dates, MusicBrainz's life-span begin/end.
	Born string
	Died string
	// WikidataQID is extracted from the artist's "wikidata" URL relation, when
	// one exists -- the cross-provider identity bridge (ADR-8) a real
	// biography needs, since MusicBrainz itself has no biography field.
	WikidataQID string
}

// Release is edition-level data: work_external_id provider "musicbrainz:release".
type Release struct {
	MBID  string
	Title string
	// Status is MusicBrainz's own vocabulary (official, promotion, bootleg,
	// pseudo-release), lowercased. It is the closest thing a Release has to a
	// "kind of edition" and becomes edition.kind.
	Status string
	// Date, Country, LabelName, CatalogueNumber and Barcode are release
	// attributes as MusicBrainz or the proxy reported them -- not every source
	// carries every field (the proxy in particular has no catalogue number or
	// barcode in its shape), so a field a source did not answer is simply empty.
	Date            string
	Country         string
	LabelName       string
	CatalogueNumber string
	Barcode         string
	Media           []Medium
}

// Medium is one disc or side within a Release.
type Medium struct {
	// Position is the disc number, 1-based.
	Position int
	Format   string
	Tracks   []Track
}

// Track is one recording's appearance on one medium: work_part_external_id
// provider "musicbrainz:recording" once resolved to a work_part.
type Track struct {
	// Position is the track's 1-based place on its medium -- what work_part's
	// sort_key is padded from, never the printed Number, which is a vinyl side
	// letter as often as a plain digit ("A1").
	Position      int
	Number        string
	Title         string
	DurationMs    int64
	RecordingMBID string
}

// Source is one place to ask (ADR-29): the Servarr proxy or direct MusicBrainz.
// Album returns both pieces in one call because that is how the cheaper source
// (the proxy) actually answers -- one request for a release group returns every
// release under it, tracks included. releaseMBID selects which of those releases
// the caller wants back; a source with no matching release under the group is an
// error, not a fabricated empty Release.
type Source interface {
	// Name is the ADR-13 rate-limit and circuit-breaker bucket key.
	Name() string
	Album(ctx context.Context, releaseGroupMBID, releaseMBID string) (ReleaseGroup, Release, error)
}

// ReleaseGroupMatch is one search result: a plausible release group plus the
// credited artist name(s), which internal/identify/parse's Identity does not carry an
// MBID for and a caller needs to weigh a match against (section 10.2).
type ReleaseGroupMatch struct {
	ReleaseGroup
	ArtistCredit string
}

// Searcher is an optional Source capability -- text search, not MBID lookup.
// Not every Source implements it: ADR-29's Servarr proxy shape is unverified
// for search (only direct musicbrainz.org has been confirmed live), so
// Provider.Search only ever tries sources that assert this interface.
type Searcher interface {
	SearchReleaseGroups(ctx context.Context, artist, title string, limit int) ([]ReleaseGroupMatch, error)
}

// RecordingSearcher is an optional Source capability: the release groups carrying a
// recording that matches a track title, for folders whose album tags are missing.
type RecordingSearcher interface {
	SearchRecordingReleaseGroups(ctx context.Context, artist, title string, limit int) ([]RecordingMatch, error)
}

// RecordingMatch is one recording search result and every release group it appears on.
type RecordingMatch struct {
	RecordingMBID string
	Title         string
	ArtistCredit  string
	DurationMs    int64
	ReleaseGroups []ReleaseGroup
}

// QuerySearcher is Searcher's own free-text counterpart (todo 4.17, ADR-56):
// a user typing into a Discover search box has not split their query into
// artist and title fields the way a file's own claimed tags let
// SearchIdentifier do, and running the same raw string through both of
// Searcher's fields would AND two broad phrases against different fields and
// mostly miss. Same optional-capability shape as Searcher, ArtistLookup and
// Discography -- not every Source implements it.
type QuerySearcher interface {
	SearchReleaseGroupsByQuery(ctx context.Context, query string, limit int) ([]ReleaseGroupMatch, error)
}

// ArtistMatch is one hit from an artist search: enough to show a person in a
// result list and to look the rest up once chosen, never the full Artist.
type ArtistMatch struct {
	Artist
	// Score is MusicBrainz's own 0-100 relevance for the query. Kept as given
	// rather than normalised: it orders one search's results and means nothing
	// across searches.
	Score int
}

// ArtistSearcher is an optional Source capability: finding an artist by name
// for someone adding one that is not in the library yet (ADR-56, revision
// 7.1). Same optional shape as QuerySearcher -- ADR-29's proxy artist search
// is unverified live, so not every Source implements it.
type ArtistSearcher interface {
	SearchArtists(ctx context.Context, query string, limit int) ([]ArtistMatch, error)
}

// ArtistLookup is an optional Source capability: full artist detail beyond the
// name and id ReleaseGroup.ArtistCredit already carries. Not every Source
// implements it -- same reasoning as Searcher, the proxy's artist shape is
// unverified live.
type ArtistLookup interface {
	Artist(ctx context.Context, mbid string) (Artist, error)
}

// ReleaseLister is an optional Source capability: every release of a release
// group, each with its tracklist -- what choosing an edition for an album
// identified only at release-group level compares the files against.
type ReleaseLister interface {
	Releases(ctx context.Context, releaseGroupMBID string) ([]Release, error)
}

// Discography is an optional Source capability: every release group credited
// to an artist, browsed rather than looked up one release group at a time --
// what subscription.expand (section 19.6, todo 4.4) needs to decide, per
// release group, whether it is admitted. Not every Source implements it --
// same reasoning as Searcher and ArtistLookup, ADR-29's proxy shape is
// unverified live for this.
type Discography interface {
	ArtistReleaseGroups(ctx context.Context, artistMBID string) ([]ReleaseGroup, error)
}
