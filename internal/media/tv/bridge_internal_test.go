package tv

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

type fakeBridge struct{ ids wikidata.WorkIDs }

func (f fakeBridge) SeriesIDs(context.Context, wikidata.WorkIDs) (wikidata.WorkIDs, bool, error) {
	return f.ids, f.ids.QID != "", nil
}

func (fakeBridge) FactsOf(context.Context, wikidata.FactKind, []string) (map[string]wikidata.WorkFacts, error) {
	return nil, nil
}

func TestWikidataFirstCorrectsTheIDsItsItemRecordsAndLaterOnlyFillsThem(t *testing.T) {
	service := Service{Wikidata: fakeBridge{ids: wikidata.WorkIDs{QID: "Q1", TVDB: 900, TMDB: 31, TVmaze: 7, IMDb: "tt0000031", AniList: []int64{5}}}}
	held := IDs{TVDB: 900, TMDB: 12, IMDb: "tt0000012"}

	first := service.bridgeIDs(t.Context(), held, []string{"wikidata", "skyhook"})
	if first.TVDB != 900 || first.TMDB != 31 || first.IMDb != "tt0000031" || first.TVmaze != 7 || len(first.AniList) != 1 {
		t.Errorf("first = %+v; want the item's ids where they disagree, TheTVDB's kept", first)
	}
	later := service.bridgeIDs(t.Context(), held, []string{"skyhook", "wikidata"})
	if later.TMDB != 12 || later.IMDb != "tt0000012" || later.TVmaze != 7 {
		t.Errorf("later = %+v; want only the missing ids filled", later)
	}
	if left := service.bridgeIDs(t.Context(), held, []string{"skyhook"}); left.TMDB != 12 || left.TVmaze != 0 {
		t.Errorf("a library without Wikidata does not ask it: %+v", left)
	}
	if unknown := (Service{Wikidata: fakeBridge{}}).bridgeIDs(t.Context(), held, []string{"wikidata"}); unknown.TMDB != 12 {
		t.Errorf("an item Wikidata does not know changes nothing: %+v", unknown)
	}
}
