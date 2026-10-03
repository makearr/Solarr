package wikidata_test

import (
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

func TestMusicBrainzArtists_ReadsP434OfTheItemsThatHaveOne(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://www.wikidata.org/w/api.php?action=wbgetentities&format=json&ids=Q1%7CQ2&props=claims": {status: http.StatusOK, body: `{
		  "entities": {
		    "Q1": {"claims": {"P434": [{"mainsnak": {"datavalue": {"value": "0383dadf-2a4e-4d10-a46a-e9e041da8eb3"}}}]}},
		    "Q2": {"claims": {"P18": []}}
		  }}`},
	}}
	fetcher := wikidata.New(wikidata.Config{WikidataClient: newTestClient(t, transport, "wikidata"), WikipediaClient: newTestClient(t, transport, "wikipedia")})

	got, err := fetcher.MusicBrainzArtists(t.Context(), []string{"Q1", "Q2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["Q1"] != "0383dadf-2a4e-4d10-a46a-e9e041da8eb3" {
		t.Errorf("artists = %v, want only Q1 with its MusicBrainz id", got)
	}
}
