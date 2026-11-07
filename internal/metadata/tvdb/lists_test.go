package tvdb_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/tvdb"
)

// The shapes of TheTVDB's answers as read live 2026-11-06 (a franchise list of series and one film), names invented.
func TestSeriesListsAndListEntriesReadTheExtendedAnswers(t *testing.T) {
	transport := &routed{answers: map[string]string{
		"/v4/login": loginAnswer,
		"/v4/series/42/extended?short=true": `{"data":{"id":42,"name":"Example Show","lists":[
			{"id":7022,"name":"Example Franchise","isOfficial":true,"url":"example-franchise"},
			{"id":8761,"name":"Crime Shows","isOfficial":false}]}}`,
		"/v4/lists/7022/extended": `{"data":{"id":7022,"name":"Example Franchise","isOfficial":true,"entities":[
			{"order":2,"seriesId":42,"movieId":null},{"order":1,"seriesId":41},{"order":3,"seriesId":null,"movieId":900001}]}}`,
	}}
	client := newClient(t, transport, "project-key")
	lists, err := client.SeriesLists(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if want := []tvdb.List{{ID: 7022, Name: "Example Franchise", IsOfficial: true}, {ID: 8761, Name: "Crime Shows"}}; !reflect.DeepEqual(lists, want) {
		t.Errorf("lists = %+v, want %+v", lists, want)
	}
	entries, err := client.ListEntries(t.Context(), 7022)
	if err != nil {
		t.Fatal(err)
	}
	var got [][3]int64
	for _, entry := range entries {
		got = append(got, [3]int64{entry.SeriesID, entry.MovieID, int64(entry.Order)})
	}
	if want := [][3]int64{{42, 0, 2}, {41, 0, 1}, {0, 900001, 3}}; !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}
