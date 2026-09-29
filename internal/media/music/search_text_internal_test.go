package music

import (
	"reflect"
	"testing"
)

// 2026-09-29: the spellings an indexer files a release under, found live against a real indexer.
func TestSearchTextVariants(t *testing.T) {
	for query, want := range map[string][]string{
		"Nosi Zada Pasulo 2003":      {"Nosi Zada Pasulo 2003"},
		"K!ra Zelu 2003":             {"K!ra Zelu 2003", "K ra Zelu 2003"},
		"Heka And Winter Fell… 2008": {"Heka And Winter Fell… 2008", "Heka And Winter Fell 2008"},
		"Vörnü Best of Vörnü":        {"Vörnü Best of Vörnü", "Voernue Best of Voernue", "Vornu Best of Vornu"},
		"Kátya Don't Stop & Go":      {"Kátya Don't Stop & Go", "Kátya Dont Stop and Go", "Katya Dont Stop and Go"},
	} {
		if got := searchTextVariants(query); !reflect.DeepEqual(got, want) {
			t.Errorf("searchTextVariants(%q) = %q, want %q", query, got, want)
		}
	}
}
