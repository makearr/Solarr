// Package pack detects ADR-50's own "one source, many works" relationship
// from a session's already-decided units. Pure and in-memory (ADR-23), the
// same layering inspect's own package doc comment describes -- persistence
// is internal/db/library.Packs' job, and the caller (importjob.Job) owns
// when this runs.
package pack

import (
	"strings"

	"github.com/makearr/Solarr/internal/identify/parse"
)

// Kinds (pack.kind, migration 0026). Re-exported by internal/db/library the
// same way inspect.Shape's own values are (library.UnitSingle etc.).
// MULTI_DISC and MULTI_SEASON are deliberately absent -- neither is
// reachable from this package's own detector (private/questions.md).
const (
	Discography = "DISCOGRAPHY"
	BoxSet      = "BOX_SET"
	Anthology   = "ANTHOLOGY"
	Compilation = "COMPILATION"
)

// Detection sources (pack.detected_from).
const (
	// DetectedFromStructure names a pack found from sibling structure alone,
	// with no name-based confirmation -- declared for the schema's own full
	// vocabulary; this package's own detector never emits it (every group it
	// finds also passes a name check, see Detect's own doc comment).
	DetectedFromStructure         = "structure"
	DetectedFromStructureAndTitle = "structure_and_name"
)

// discographyConfidence values this pass's own two detection paths --
// SourceDeclaresDiscography names the release itself ("Artist-Discography-
// 1990-2020-FLAC"), a stronger signal than SiblingArtistAgreement noticing
// after the fact that several unrelated-looking folders happen to share a
// parsed artist.
const (
	confidenceSourceDeclared   = 0.85
	confidenceSiblingAgreement = 0.65
)

// Candidate is one already-decided import_unit, the caller's own view --
// this package touches no database (ADR-23), only what a caller already has
// in hand after DECIDE. RelPath is relative to the session's own
// source_path, exactly as import_unit.rel_path stores it.
type Candidate struct {
	UnitID  int64
	RelPath string
}

// Group is one detected pack: which candidates belong together and why.
type Group struct {
	Kind         string
	DetectedFrom string
	Confidence   float64
	Title        string
	Members      []Candidate // input order preserved
}

// Detect groups sibling units into packs. Only a unit that is a direct
// child of the walked source participates (RelPath has no "/") -- a unit
// nested deeper (already merged into its own parent by GROUP, or one member
// of a genuine subfolder structure this pass's own detector does not reach)
// never does. Nor does a unit whose own RelPath is not unique among direct
// children: GROUP's track-continuity structural fallback can misread one
// real release's own messy filenames (found live, against a real scene
// fixture: a single album, no clean sequential numbering, split into five
// import_unit rows that all report the identical directory) as several
// separate units sharing one directory -- excludeAmbiguousDirectories drops
// every one of them rather than let several fragments of the same release
// masquerade as several different releases by the same artist. Two paths
// feed one result over what remains, structure always the gate:
//
//   - The source itself declares what it is: sourceName (the session's own
//     source_path basename) parses with a real Discography flag and artist
//     (identify/parse.Identity.Discography -- "Artist-Discography-1990-2020-
//     FLAC" is exactly this). Every direct-child unit becomes one member,
//     regardless of how each is itself named -- the container's own claim is
//     the confirming signal here, matching ADR-50's own worked example.
//   - No such declaration, so siblings are grouped by their own parsed
//     artist agreeing with each other (two or more sharing one artist).
//     Every remaining direct-child unit with an empty or a lone parsed
//     artist stays ungrouped -- a single unit, named "Anthology" or
//     anything else, never becomes a pack: the anthology trap (§19.6) falls
//     out of requiring at least two siblings, never from reading a name.
//
// Every group Detect returns cleared a real, if imperfect, name check, so
// DetectedFromStructure (structure with no name confirmation at all) is
// never actually produced -- ADR-50 wants both signals feeding one
// confidence, not structure alone deciding.
func Detect(sourceName string, candidates []Candidate) []Group {
	var direct []Candidate
	for _, c := range candidates {
		if !strings.Contains(c.RelPath, "/") {
			direct = append(direct, c)
		}
	}
	direct = excludeAmbiguousDirectories(direct)
	if len(direct) < 2 {
		return nil
	}

	if identity := parse.Parse(sourceName).Identity; identity.Discography && identity.Artist != "" {
		return []Group{{
			Kind: Discography, DetectedFrom: DetectedFromStructureAndTitle,
			Confidence: confidenceSourceDeclared, Title: identity.Artist, Members: direct,
		}}
	}

	return siblingArtistGroups(direct)
}

// excludeAmbiguousDirectories drops every candidate whose own RelPath is
// shared with another candidate -- two or more import_unit rows reporting
// the identical directory are fragments GROUP could not cleanly bound into
// one unit, not real sibling releases (Detect's own doc comment has the
// live-found case). Order preserved among what remains.
func excludeAmbiguousDirectories(candidates []Candidate) []Candidate {
	counts := make(map[string]int, len(candidates))
	for _, c := range candidates {
		counts[c.RelPath]++
	}
	unambiguous := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if counts[c.RelPath] == 1 {
			unambiguous = append(unambiguous, c)
		}
	}
	return unambiguous
}

// siblingArtistGroups is Detect's fallback path: no declared-discography
// container, so group direct children by their own parsed artist agreeing.
func siblingArtistGroups(direct []Candidate) []Group {
	type bucket struct {
		key     string // lowercased, trimmed, for comparison only
		display string // as parsed, for Title
		members []Candidate
	}
	var buckets []*bucket
	for _, c := range direct {
		artist := parse.Parse(c.RelPath).Identity.Artist
		key := strings.ToLower(strings.TrimSpace(artist))
		if key == "" {
			continue
		}
		var found *bucket
		for _, b := range buckets {
			if b.key == key {
				found = b
				break
			}
		}
		if found == nil {
			found = &bucket{key: key, display: artist}
			buckets = append(buckets, found)
		}
		found.members = append(found.members, c)
	}

	var groups []Group
	for _, b := range buckets {
		if len(b.members) < 2 {
			continue
		}
		groups = append(groups, Group{
			Kind: Discography, DetectedFrom: DetectedFromStructureAndTitle,
			Confidence: confidenceSiblingAgreement, Title: b.display, Members: b.members,
		})
	}
	return groups
}
