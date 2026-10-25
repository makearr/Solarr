package indexer_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
)

func TestText_SpellsStructuredParametersForAWordsOnlyIndexer(t *testing.T) {
	cases := []struct {
		request indexer.SearchRequest
		want    string
	}{
		{indexer.SearchRequest{Query: "words", Extra: map[string]string{"artist": "x"}}, "words"},
		{indexer.SearchRequest{Extra: map[string]string{"artist": "Heka", "album": "Zanuvode", "year": "1988"}}, "Heka Zanuvode 1988"},
		{indexer.SearchRequest{Extra: map[string]string{"q": "Show", "season": "1", "ep": "2"}}, "Show S01E02"},
		{indexer.SearchRequest{Extra: map[string]string{"q": "Show", "season": "3"}}, "Show S03"},
	}
	for _, test := range cases {
		if got := indexer.Text(test.request); got != test.want {
			t.Errorf("Text(%+v) = %q, want %q", test.request, got, test.want)
		}
	}
}

func TestKeep_DropsOtherTopCategoriesAndBoundsTheLimit(t *testing.T) {
	items := []indexer.RawItem{{Title: "a", Categories: []int{3040}}, {Title: "b", Categories: []int{5000}}, {Title: "c"}, {Title: "d", Categories: []int{3010}}}
	got := indexer.Keep(indexer.SearchRequest{Categories: []int{3000}, Limit: 2}, items)
	var titles []string
	for _, item := range got {
		titles = append(titles, item.Title)
	}
	if !reflect.DeepEqual(titles, []string{"a", "c"}) {
		t.Errorf("kept = %v", titles)
	}
}

func TestProtocolOf_KnowsEveryImplementation(t *testing.T) {
	for impl, want := range map[string]string{
		indexer.ImplNewznab: "usenet", indexer.ImplTorznab: "torrent", indexer.ImplRedacted: "torrent", indexer.ImplSoulseek: "ddl",
		indexer.ImplThePirateBay: "torrent",
	} {
		if got := indexer.ProtocolOf(impl); got != want {
			t.Errorf("ProtocolOf(%s) = %s, want %s", impl, got, want)
		}
	}
}

func TestFlexInt_ReadsNumbersAndStrings(t *testing.T) {
	var parsed struct{ A, B, C indexer.FlexInt }
	if err := json.Unmarshal([]byte(`{"A":12,"B":"34","C":""}`), &parsed); err != nil || parsed.A != 12 || parsed.B != 34 || parsed.C != 0 {
		t.Errorf("parsed = %+v, %v", parsed, err)
	}
}

func TestMagnet_CarriesTheHashAndTrackers(t *testing.T) {
	link := indexer.Magnet("ABCDEF", "Album", []string{"udp://b:1", "udp://a:1"})
	if !strings.HasPrefix(link, "magnet:?") || !strings.Contains(link, "xt=urn%3Abtih%3Aabcdef") || !strings.Contains(link, "&tr=udp%3A%2F%2Fa%3A1&tr=udp%3A%2F%2Fb%3A1") {
		t.Errorf("magnet = %s", link)
	}
}

// Every catalog preset is unique and has an address, except the generic entries.
func TestNewznabDefinitions_AreUniqueAndAddressed(t *testing.T) {
	seen := map[string]bool{}
	for _, definition := range indexer.NewznabDefinitions() {
		if seen[definition.ID] {
			t.Errorf("%s twice", definition.ID)
		}
		seen[definition.ID] = true
		generic := definition.ID == "newznab" || definition.ID == "torznab"
		if generic == (len(definition.BaseURLs) > 0) {
			t.Errorf("%s: base URLs %v", definition.ID, definition.BaseURLs)
		}
	}
	if len(seen) < 20 {
		t.Errorf("only %d presets", len(seen))
	}
}
