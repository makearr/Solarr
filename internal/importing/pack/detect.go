// Package pack detects ADR-50's one-source-many-works relationship from a session's decided units; pure and in-memory (ADR-23), persistence is library.Packs' and importjob.Job decides when it runs.
package pack

import (
	"strings"

	"github.com/makearr/Solarr/internal/identify/parse"
)

// Kinds (pack.kind, migration 0026), re-exported by internal/db/library; MULTI_DISC and MULTI_SEASON are absent because this detector cannot reach them.
const (
	Discography = "DISCOGRAPHY"
	BoxSet      = "BOX_SET"
	Anthology   = "ANTHOLOGY"
	Compilation = "COMPILATION"
)

// Detection sources (pack.detected_from).
const (
	// DetectedFromStructure names a pack from sibling structure alone; declared for the schema's vocabulary but never emitted, since every group Detect finds also passes a name check.
	DetectedFromStructure         = "structure"
	DetectedFromStructureAndTitle = "structure_and_name"
)

// discographyConfidence values the two detection paths: SourceDeclaresDiscography (the release name says so) is stronger than SiblingArtistAgreement (folders sharing a parsed artist).
const (
	confidenceSourceDeclared   = 0.85
	confidenceSiblingAgreement = 0.65
)

// Candidate is one decided import_unit as the caller sees it; this package touches no database (ADR-23), and RelPath is relative to the session's source_path as stored.
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

// Detect groups sibling units into packs; only direct children of the walked source (RelPath has no "/") with a unique RelPath take part, because GROUP's track-continuity fallback can split one messy release into several rows sharing a directory (excludeAmbiguousDirectories).
// Two paths feed one result: a source name parsing as a Discography makes every direct child a member, else siblings sharing one parsed artist group (two or more, so the anthology trap, section 19.6, never reads a name); every group passes a name check, so structure alone never decides (ADR-50).
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

// excludeAmbiguousDirectories drops every candidate whose RelPath another shares: those are fragments GROUP could not bound, not sibling releases; order is preserved.
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
