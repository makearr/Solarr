// Package model holds the pure domain types shared across media types: no I/O, no
// database tags, no plugin imports (architecture.md section 6). It exists so a type
// that must mean the same thing for music, TV and a ROM has exactly one definition.
package model

import "time"

// Coverage says whether a Match closes every gap it addresses or only some of them.
type Coverage string

const (
	// CoverageComplete means the match closes every gap it addresses.
	CoverageComplete Coverage = "COMPLETE"
	// CoveragePartial means some of Match.DesiredItemIDs are still open; see Missing.
	CoveragePartial Coverage = "PARTIAL"
)

// MatchEvidence is why a Match or Identification was made -- data, never a formatted
// string (ADR-10), so F1's "why?" can render it directly and a translation never has
// to touch matching logic.
type MatchEvidence struct {
	Kind   string // "external_id" | "embedded_tag" | "hash" | "fingerprint" | "parse" | "provenance"
	Detail string // "MUSICBRAINZ_TRACKID=abc-123" -- human-readable, not user input
}

// Match is one candidate satisfying a set of desired items at once -- a season pack
// closing ten gaps, a box set, a various-artists compilation (ADR-23 extension point
// 1). A per-item loop cannot express this, which is why Evaluate returns matches over
// a set rather than a single item.
type Match struct {
	DesiredItemIDs []int64
	Coverage       Coverage
	Missing        []int64 // still open when Coverage is PARTIAL
	Confidence     float64
	Evidence       []MatchEvidence
}

// GroupHint is shown in the UI beside a SearchGroup; it carries no behaviour of its
// own.
type GroupHint string

const (
	// GroupHintSeasonPack is a TV season grouped into one search.
	GroupHintSeasonPack GroupHint = "season_pack"
	// GroupHintSingle is one desired item, searched on its own.
	GroupHintSingle GroupHint = "single"
	// GroupHintAlbum is a music release grouped into one search.
	GroupHintAlbum GroupHint = "album"
	// GroupHintBoxset is a multi-work collection grouped into one search.
	GroupHintBoxset GroupHint = "boxset"
)

// SearchTier is how widely a search casts (section 10.2). Widening is progressive:
// a higher tier runs only once a lower one has produced no acceptable match, so the
// ordering is meaningful and not just a label.
type SearchTier int

const (
	// SearchTierExact searches the canonical title only.
	SearchTierExact SearchTier = iota
	// SearchTierStandard searches the canonical title plus year.
	SearchTierStandard
	// SearchTierAlias searches alias and alternate titles too.
	SearchTierAlias
	// SearchTierFuzzy searches normalised forms with relaxed thresholds.
	SearchTierFuzzy
	// SearchTierMetadata searches by external ID, where the indexer supports it.
	SearchTierMetadata
)

// SearchGroup is one search covering many desired items -- a season, an album, a box
// set -- so core issues one indexer search per group instead of one per item and
// never has to know what a season is (ADR-23 extension point 1).
type SearchGroup struct {
	DesiredItemIDs []int64
	Terms          []string // several forms: "S03E10", absolute "#062", alias titles
	Tier           SearchTier
	Hint           GroupHint
}

// Confidence widens the grace period an AvailabilityWindow allows (section 13): a
// window based on a firm release date needs less slack than one guessed from a
// typical cadence.
type Confidence string

const (
	// ConfidenceHigh needs the shortest grace period around AvailableFrom.
	ConfidenceHigh Confidence = "high"
	// ConfidenceMedium needs a moderate grace period.
	ConfidenceMedium Confidence = "medium"
	// ConfidenceLow needs the widest grace period a plugin defines.
	ConfidenceLow Confidence = "low"
	// ConfidenceUnknown means the plugin could not compute AvailableFrom at all.
	ConfidenceUnknown Confidence = "unknown"
)

// AvailabilityWindow is when a desired item becomes worth searching for -- computed
// by the plugin from whatever release-date concept its media type actually has (air
// date and network timezone for TV, theatrical/digital/physical for movies, release
// date for music). Core does not plan a search before AvailableFrom plus the grace
// period Confidence implies, and never learns what an air date is (ADR-23 extension
// point 2).
type AvailabilityWindow struct {
	AvailableFrom time.Time // zero value means not yet known
	Confidence    Confidence
	Basis         string // "network=BBC One tz=Europe/Lerapo" -- shown in the UI
}

// SatisfactionKind says whether a desired item is satisfied by one file or by a set
// of them.
type SatisfactionKind string

const (
	// SatisfactionSingle means one file satisfies the desired item.
	SatisfactionSingle SatisfactionKind = "SINGLE"
	// SatisfactionSet means Required files are needed to satisfy the desired item.
	SatisfactionSet SatisfactionKind = "SET"
)

// ItemState is what the planner, hunt queue, UI and discovery all plan against.
// PARTIAL is first-class everywhere, not a music special case -- TV seasons use it
// too.
type ItemState string

const (
	// ItemStateMissing means Present is zero.
	ItemStateMissing ItemState = "MISSING"
	// ItemStatePartial means some but not all of a SET is present.
	ItemStatePartial ItemState = "PARTIAL"
	// ItemStateComplete means everything required is present.
	ItemStateComplete ItemState = "COMPLETE"
	// ItemStateUpgradable means complete, but a better version is being sought.
	ItemStateUpgradable ItemState = "UPGRADABLE"
)

// Satisfaction is how close a desired item is to complete -- a plugin call, not a
// core rule, because a movie is satisfied by one file and an album by N of them, and
// "75% complete" has to be a meaningful, plannable, searchable state rather than a
// music special case (ADR-23 extension point 4).
type Satisfaction struct {
	Kind     SatisfactionKind
	Required int // meaningful for SatisfactionSet only
	Present  int
	State    ItemState
}
