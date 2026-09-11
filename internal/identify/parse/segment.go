package parse

import (
	"regexp"
	"strings"
)

// discMarkerRe catches a disc-subfolder name on its own ("CD1", "Disc 2") --
// a container internal/importing/inspect's own grouping already merges structurally, but
// one that must still score as non-entity if it ever reaches SegmentConfidence
// (e.g. a lone disc marker with no sibling to merge against).
var discMarkerRe = regexp.MustCompile(`(?i)^(cd|disc|disk)[\s_-]*\d+$`)

// trailingYearRe strips this project's own default "Title (Year)" naming
// shape (internal/moving/naming, proven byte-for-byte against thousands of real album
// folders) so the title is judged on its own -- not the year, and not by
// treating the title's own content as if it were the same kind of technical
// claim a bare year is. Without this, a real, purely numeric album title
// (Nuzes's "19"/"21"/"25"/"30", XT/QK's "1977 (2022)") scores identically to
// a genuine bare-year container ("(1997)" alone) even though only one of
// them is an entity name -- found live against the real production library.
var trailingYearRe = regexp.MustCompile(`\(((?:19|20)\d{2})(?:-(?:19|20)\d{2})?\)\s*$`)

// SegmentConfidence scores a single path segment's chance of being a real
// entity name (an artist, an album) rather than a container or noise word --
// internal/importing/inspect's own SegmentPlausibility extension point (ADR-48), which
// deliberately carries no music vocabulary of its own and asks a caller who
// does. Unlike Parse, this is not a release name: there is no artist/album
// pair to split, so the same vocabulary (edition/source/format words, bare
// years, hashed junk) is checked word-by-word instead.
func SegmentConfidence(segment string) float64 {
	trimmed := strings.TrimSpace(segment)
	if trimmed == "" || isHashedRelease(trimmed) || discMarkerRe.MatchString(trimmed) {
		return 0
	}

	body := trimmed
	stripYear := false
	if loc := trailingYearRe.FindStringIndex(trimmed); loc != nil {
		body = strings.TrimSpace(trimmed[:loc[0]])
		stripYear = true
		if body == "" {
			return 0.1 // nothing but a year in brackets -- a bare-year container
		}
	}

	meaningful := 0
	for _, word := range strings.Fields(Normalize(body)) {
		word = strings.TrimFunc(word, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9')
		})
		if word == "" {
			continue
		}
		// A "Title (Year)" match already accounts for the date, so the title
		// itself is not re-checked against year/digit patterns -- only real
		// edition/source/format noise still disqualifies it (isNoiseWord).
		// Without a year suffix, the full vocabulary applies, including bare
		// years and plain numbers, which alone are still a container.
		if stripYear {
			if isNoiseWord(word) {
				continue
			}
		} else if yearRe.MatchString(word) || isAllDigits(word) || isVocabWord(word) {
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

// isNoiseWord is edition/source/format vocabulary only -- deliberately
// narrower than isVocabWord, which also claims bare years and would
// otherwise disqualify a real numeric album title sitting in front of a
// matched "(Year)" suffix.
func isNoiseWord(word string) bool {
	return editionWords[word] || sourceWords[word] != "" ||
		losslessFormats[word] != "" || lossyFormats[word] != "" || containerFormats[word] != ""
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
