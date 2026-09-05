package parse

import (
	"regexp"
	"strings"
)

// discMarkerRe catches a disc-subfolder name on its own ("CD1", "Disc 2") --
// a container internal/inspect's own grouping already merges structurally, but
// one that must still score as non-entity if it ever reaches SegmentConfidence
// (e.g. a lone disc marker with no sibling to merge against).
var discMarkerRe = regexp.MustCompile(`(?i)^(cd|disc|disk)[\s_-]*\d+$`)

// SegmentConfidence scores a single path segment's chance of being a real
// entity name (an artist, an album) rather than a container or noise word --
// internal/inspect's own SegmentPlausibility extension point (ADR-48), which
// deliberately carries no music vocabulary of its own and asks a caller who
// does. Unlike Parse, this is not a release name: there is no artist/album
// pair to split, so the same vocabulary (edition/source/format words, bare
// years, hashed junk) is checked word-by-word instead.
func SegmentConfidence(segment string) float64 {
	trimmed := strings.TrimSpace(segment)
	if trimmed == "" || isHashedRelease(trimmed) || discMarkerRe.MatchString(trimmed) {
		return 0
	}

	meaningful := 0
	for _, word := range strings.Fields(Normalize(trimmed)) {
		word = strings.TrimFunc(word, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9')
		})
		if word == "" || yearRe.MatchString(word) || isAllDigits(word) || isVocabWord(word) {
			continue
		}
		meaningful++
	}
	if meaningful == 0 {
		return 0.1 // every word was noise, a bare year, or a number -- a container, not a name
	}

	confidence := 0.5 + 0.1*float64(meaningful)
	if confidence > 0.85 {
		confidence = 0.85 // a segment alone never carries Parse's own full-title certainty
	}
	return confidence
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
