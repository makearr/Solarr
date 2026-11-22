package model

import "context"

// ExternalID is one provider's identity for a work; its Confidence is the provider's mapping confidence, distinct from an Identification's.
type ExternalID struct {
	Provider   string
	ExternalID string
	Confidence float64
}

// FileRef is what an Identifier needs to locate bytes and look up prior identity, with no behaviour of its own. MediaObjectID and RootFolderID are optional (ADR-48) because import units are identified before either exists; RelPath is always set.
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
