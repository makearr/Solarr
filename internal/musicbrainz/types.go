// Package musicbrainz is ADR-29's provider: a Servarr metadata proxy tried first,
// direct musicbrainz.org as the fallback ADR-13's circuit breaker falls over to,
// both normalised to the same shape and cached behind internal/metacache. Direct-
// wired into internal/music until stage 9 extracts the MetadataProvider interface
// (architecture section 8.2), the way internal/music itself is.
package musicbrainz

import "context"

// ReleaseGroup is work-level data: MediaNexus's work_external_id provider
// "musicbrainz:release-group" (internal/music).
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
// credited artist name(s), which internal/parse's Identity does not carry an
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
