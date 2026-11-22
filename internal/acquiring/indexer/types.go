// Package indexer is stage 5's Newznab/Torznab client (ADR-37, 43, 47) with the Driver, Base and catalog API indexers build on; it speaks documented APIs, not Cardigann (ADR-43), and takes an already-configured httpx.Client.
package indexer

import "time"

// RawItem is one search or caps result before internal/identify/parse reads its title (ADR-18); it holds only what the indexer's protocol said.
type RawItem struct {
	Title string
	GUID  string
	// Link is the enclosure URL -- an .nzb for Newznab, a .torrent or magnet
	// for Torznab. Already carries whatever query parameters (an API key
	// among them) the indexer requires to fetch it.
	Link string
	// Categories are the indexer's own Newznab-style category ids
	// (ADR-43's shared vocabulary), as reported on this item -- a release
	// may carry more than one (e.g. both a specific and a parent category).
	Categories  []int
	SizeBytes   int64
	PublishedAt time.Time
	// Seeders and Peers are Torznab only; both are zero for a Newznab
	// (usenet) result, which has no such concept.
	Seeders int
	Peers   int
	// InfoHash is Torznab only, empty for Newznab.
	InfoHash string
	// IDs are the ids the indexer says the release is for, by provider ("tvdb", "tvmaze", "imdb" as "tt1234567", "tmdb");
	// zero and empty values are dropped.
	IDs map[string]string
	// Season and Episode are as the indexer wrote them; a daily show puts a date in Episode.
	Season, Episode string
	// Passworded is the indexer saying the release needs a password (Newznab password=1); an inner archive that may
	// hold one (2) is not.
	Passworded bool
	// PostedAt is when the release was posted (usenetdate), which PublishedAt is not on every indexer; zero when not given.
	PostedAt time.Time
	// Grabs counts the indexer's downloads of the release.
	Grabs int
	// MagnetURL is a magnet link besides the enclosure, for when the .torrent link is dead.
	MagnetURL string
	// MinimumSeedTime and MinimumRatio are the tracker's seeding obligation; zero when it states none.
	MinimumSeedTime time.Duration
	MinimumRatio    float64
	// DownloadVolumeFactor is 0 for freeleech, 1 for a normal download; nil when the indexer does not say.
	DownloadVolumeFactor *float64
}

// Category is one entry of the t=caps categories block; ADR-43 takes the set from the specification and from what each indexer supports.
type Category struct {
	ID            int
	Name          string
	Subcategories []Category
}

// SearchFunction is one t=caps <searching> entry -- which of the Newznab
// search modes (general, tv, movie, music, book, audio) this indexer
// implements, and what parameters it accepts for it.
type SearchFunction struct {
	Available       bool
	SupportedParams []string
}

// Capabilities is what one indexer's own t=caps response answered, never
// assumed (ADR-43).
type Capabilities struct {
	Categories []Category
	// Search is keyed by Newznab's own function names: "search", "tv-search",
	// "movie-search", "music-search", "book-search", "audio-search".
	Search map[string]SearchFunction
}
