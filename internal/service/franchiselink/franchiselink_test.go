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

type fakeShows struct {
	inputs library.ShowFilmInputs
	made   []library.PlannedShowLink
	into   []int64
}

func (f *fakeShows) ShowFilmInputs(context.Context, string, string) (library.ShowFilmInputs, error) {
	return f.inputs, nil
}

func (f *fakeShows) MakeShowLink(_ context.Context, plan library.PlannedShowLink, _, _ string, seriesCollectionID int64) (bool, error) {
	f.made = append(f.made, plan)
	f.into = append(f.into, seriesCollectionID)
	return true, nil
}

func TestLinkShows_LinksTheFilmCollectionNamedForAShowWithTheShowsCollection(t *testing.T) {
	shows := &fakeShows{inputs: library.ShowFilmInputs{
		FilmCollections:  []library.NamedCollection{{ID: 500, Name: "Sex and the City Collection"}},
		Series:           []library.NamedWork{{WorkID: 25857, Title: "Sex and the City"}},
		SeriesCollection: map[int64]int64{},
	}}
	service := Service{Shows: shows, FilmType: "movie", SeriesType: "tv", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	made, err := service.LinkShows(t.Context())
	if err != nil || made != 1 || len(shows.made) != 1 || shows.made[0].SeriesWorkID != 25857 || shows.into[0] != 0 {
		t.Fatalf("LinkShows = %d, %v; made %+v into %v; want the film collection and the show, a collection made for the show", made, err, shows.made, shows.into)
	}
}
