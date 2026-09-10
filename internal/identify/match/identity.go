package match

// ExternalID is one provider's id for the thing being matched. Distinct from
// model.ExternalID: this package only ever needs identity, never a provider's own
// confidence in the mapping.
type ExternalID struct {
	Provider string
	ID       string
}

// Identity is every field Distance can compare. Claimed and Candidate share it so
// the two sides of a comparison can never drift out of shape with each other.
type Identity struct {
	ExternalIDs     []ExternalID
	Title           string
	AlternateTitles []string
	Year            int
	// ItemCount is tracks for an album, episodes for a season -- whichever
	// countable unit the media type has; both compare via DimensionTrackCount.
	ItemCount int
	// Numbering is every known scheme at once: "S03E10", "#062", a disc/track pair.
	Numbering []string
	// PathSegments are folder names, root-relative, most specific last.
	PathSegments []string
}

// Claimed is the identity being matched: a search result's title/year, or a
// file's parsed-from-path guess.
type Claimed struct{ Identity }

// Candidate is one known work being matched against. ID is the caller's own key
// (a work id, an edition id) -- this package never interprets it, only carries it
// through to RankedCandidate.
type Candidate struct {
	ID string
	Identity
}
