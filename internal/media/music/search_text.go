package music

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// searchTextVariants is a text query and the spellings indexers file the same release under, as
// Lidarr cleans its queries: punctuation as spaces ("K!ra" is found as "K ra", 2026-09-29), then
// German umlauts spelt out ("Voernue"), then accents dropped. The original comes first; a
// variant equal to one before it is left out.
func searchTextVariants(query string) []string {
	cleaned := cleanSearchText(query)
	variants := []string{query}
	for _, variant := range []string{cleaned, germanSpelling(cleaned), withoutAccents(cleaned)} {
		if variant != "" && !containsFold(variants, variant) {
			variants = append(variants, variant)
		}
	}
	return variants
}

// cleanSearchText drops apostrophes, reads "&" as "and", and turns every other non-letter,
// non-digit into a space, collapsing runs of them.
func cleanSearchText(query string) string {
	var cleaned strings.Builder
	for _, r := range strings.ReplaceAll(query, "&", " and ") {
		switch {
		case r == '\'' || r == '’' || r == '`':
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cleaned.WriteRune(r)
		default:
			cleaned.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(cleaned.String()), " ")
}

var germanUmlauts = strings.NewReplacer(
	"ä", "ae", "ö", "oe", "ü", "ue", "Ä", "Ae", "Ö", "Oe", "Ü", "Ue", "ß", "ss",
)

func germanSpelling(query string) string { return germanUmlauts.Replace(query) }

// withoutAccents decomposes each letter and drops its marks: "Björk" becomes "Bjork".
func withoutAccents(query string) string {
	stripped, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), query)
	if err != nil {
		return query
	}
	return stripped
}

func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(value, wanted) {
			return true
		}
	}
	return false
}
