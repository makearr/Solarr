package works

import "strings"

// IDVerdict is what the ids an indexer attached to a release say about whether it is for the wanted item.
type IDVerdict int

// The verdicts: the indexer named no id the item has, the same id, or another one.
const (
	IDsUnknown IDVerdict = iota
	IDsSame
	IDsDiffer
)

// idProviders is the order ids are trusted in: the first provider both sides know decides.
var idProviders = []string{"tvdb", "tvmaze", "tmdb", "imdb"}

// CompareIDs holds the ids a release carries against the wanted item's, both by provider ("tvdb", "tvmaze", "tmdb", "imdb").
func CompareIDs(release, wanted map[string]string) IDVerdict {
	for _, provider := range idProviders {
		has, wants := strings.TrimPrefix(release[provider], "tt"), strings.TrimPrefix(wanted[provider], "tt")
		if has == "" || wants == "" {
			continue
		}
		if has == wants {
			return IDsSame
		}
		return IDsDiffer
	}
	return IDsUnknown
}
