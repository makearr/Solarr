// Package indexer is stage 5's own Newznab/Torznab protocol client (ADR-37,
// 43, 47): MediaNexus speaks both natively rather than implementing
// Cardigann (ADR-43), so this package knows only the shared Newznab-style
// XML shape, never a per-site scraping definition. It has no opinion on
// rate limiting, circuit breaking or proxying -- the caller supplies an
// already-configured httpx.Client, the same split internal/importing/
// arrimport's own LidarrClient already uses for a per-connection external
// service.
package indexer

import "time"

// RawItem is one search or caps result before internal/identify/parse
// extracts attributes from its own title (ADR-18) -- this package does no
// interpretation of what a release actually is, only what the indexer's own
// protocol said about it.
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
}

// Category is one entry from the indexer's own t=caps categories block --
// ADR-43: "the exact set must be taken from the specification at
// implementation time rather than assumed", and the same applies to what
// one specific indexer actually supports.
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
