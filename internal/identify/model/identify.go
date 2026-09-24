package model

import "context"

// ExternalID is one provider's identity for a work -- a MusicBrainz release group, a
// TVDB series, a ROM's DAT entry. Confidence is the provider's own confidence in the
// mapping, distinct from an Identification's confidence in having found the right
// ExternalID for a given file.
type ExternalID struct {
	Provider   string
	ExternalID string
	Confidence float64
}

// FileRef is what an Identifier strategy has to work with: enough to locate the
// bytes and enough to look up what the library already knows about them. FileRef
// carries no behaviour of its own -- a strategy combines it with fsx (to read the
// file) or the store (to look up prior identity) to actually do either; this package
// stays free of both.
//
// MediaObjectID and RootFolderID are both optional (ADR-48): a scan-time file
// already has both, but an import-inspection unit (a download folder, a manual
// import target) is identified before it has a media_object row or a root folder
// at all -- that is the entire point of inspecting it first. RelPath is always
// present, relative to whatever base the caller's PathOf/TagSource resolves it
// against, which differs between the two callers rather than this type.
type FileRef struct {
	MediaObjectID *int64
	RootFolderID  *int64
	RelPath       string
}

// Identification is one strategy's answer to "what is this file" -- ranked, not
// asserted, since core decides which of several to believe (ADR-24).
type Identification struct {
	// Strategy names which kind of evidence this came from: "hash" | "embedded-tag" |
	// "fingerprint" | "xml" | "parse" | "provenance".
	Strategy string
	// WorkID is nil when the file's external identity is known but has no local work
	// yet -- the caller resolves ExternalID to a work (or creates one) separately;
	// this package does no such lookup.
	WorkID     *int64
	ExternalID *ExternalID
	// Precedence is what kind of evidence this is, and alone decides that an assertion
	// outranks a guess; Confidence is how likely it is to be right, on one calibrated scale
	// that a single threshold is compared with (2026-09-24).
	Precedence Precedence
	Confidence float64
	Evidence   []MatchEvidence
}

// Precedence ranks kinds of evidence, strongest last so the zero value is the weakest.
type Precedence int

// The kinds of evidence, weakest first.
const (
	// PrecedenceInferred is borrowed from a neighbour, such as a folder sibling.
	PrecedenceInferred Precedence = iota
	// PrecedenceSearched is a lookup by what the file says or sounds like.
	PrecedenceSearched
	// PrecedenceMatched is a whole structure fitting, such as a folder against a tracklist.
	PrecedenceMatched
	// PrecedenceAsserted is an identifier the file or a person states outright.
	PrecedenceAsserted
)

// Identifier is one identification strategy, registered per media type. Core owns
// the chain and the ordering (RunChain); a strategy only ever answers "what do I
// think this file is" for the one thing it knows how to check (ADR-24).
type Identifier interface {
	Name() string
	// Order is where this strategy runs in the chain; lower runs first. Cheaper,
	// more reliable strategies (an embedded MBID) belong before expensive ones
	// (acoustic fingerprinting) so RunChain can stop before paying for the latter.
	Order() int
	Identify(ctx context.Context, f FileRef) ([]Identification, error)
}
