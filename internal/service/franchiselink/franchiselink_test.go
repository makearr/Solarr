package franchiselink

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

type fakeStore struct {
	candidates []library.LinkCandidate
	refused    map[[2]string]bool
	made       []library.PlannedLink
}

func (f *fakeStore) LinkCandidates(context.Context) ([]library.LinkCandidate, error) {
	return f.candidates, nil
}

func (f *fakeStore) LinkState(context.Context) (map[[2]int64]bool, map[[2]string]bool, error) {
	return map[[2]int64]bool{}, f.refused, nil
}

func (f *fakeStore) AutoLink(_ context.Context, link library.PlannedLink) (bool, error) {
	f.made = append(f.made, link)
	return true, nil
}

func TestLink_LinksSeriesFilmsAndSoundtracksOfOneFranchiseAndSkipsWhatWasUndone(t *testing.T) {
	store := &fakeStore{candidates: []library.LinkCandidate{
		{ID: 1, MediaType: "tv", Members: 2, Keys: []string{"wikidata:Q462"}, Identity: []string{"wikidata:Q462"}},
		{ID: 2, MediaType: "movie", Members: 3, Keys: []string{"tmdb:collection:10", "wikidata:Q462"}, Identity: []string{"tmdb:collection:10"}},
		{ID: 3, MediaType: "music", Members: 1, Keys: []string{"wikidata:Q462"}, Identity: []string{"wikidata@music:Q462"}},
	}, refused: map[[2]string]bool{}}
	service := Service{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	made, err := service.Link(t.Context())
	want := []library.PlannedLink{
		{A: 1, B: 2, Reason: library.LinkReasonProvider, Detail: "wikidata:Q462"},
		{A: 1, B: 3, Reason: library.LinkReasonProvider, Detail: "wikidata:Q462"},
		{A: 2, B: 3, Reason: library.LinkReasonProvider, Detail: "wikidata:Q462"},
	}
	if err != nil || made != 3 || !reflect.DeepEqual(store.made, want) {
		t.Fatalf("Link = %d, %v; linked %+v, want %+v", made, err, store.made, want)
	}

	store.made = nil
	store.refused = map[[2]string]bool{{"tmdb:collection:10", "wikidata:Q462"}: true}
	if made, err := service.Link(t.Context()); err != nil || made != 2 {
		t.Errorf("Link with one pair undone = %d, %v; want the other two", made, err)
	}
}
