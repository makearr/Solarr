package tv_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

type fakeFranchises struct {
	ties    map[wikidata.SeriesRef]wikidata.SeriesTies
	members map[string][]wikidata.FranchiseSeries
}

func (f fakeFranchises) TiesOf(_ context.Context, refs []wikidata.SeriesRef) (map[wikidata.SeriesRef]wikidata.SeriesTies, error) {
	out := map[wikidata.SeriesRef]wikidata.SeriesTies{}
	for _, ref := range refs {
		if ties, ok := f.ties[ref]; ok {
			out[ref] = ties
		}
	}
	return out, nil
}

func (f fakeFranchises) FranchiseMembers(_ context.Context, qids []string) (map[string][]wikidata.FranchiseSeries, error) {
	out := map[string][]wikidata.FranchiseSeries{}
	for _, qid := range qids {
		out[qid] = f.members[qid]
	}
	return out, nil
}

func TestTheFranchisePassGathersTheSeriesTheLibraryHoldsAndOffersTheOthers(t *testing.T) {
	service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: 1, Monitor: tv.MonitorNone})
	if err != nil {
		t.Fatal(err)
	}
	source := fakeFranchises{
		ties: map[wikidata.SeriesRef]wikidata.SeriesTies{{TVDB: 1234}: {
			Franchises: []wikidata.Franchise{{QID: "Q4321", Label: "Example Franchise", Named: true}},
			Related:    []wikidata.FranchiseSeries{{QID: "Q55", Label: "Example: Origins", TVDB: 888, Year: 2026}},
		}},
		members: map[string][]wikidata.FranchiseSeries{"Q4321": {
			{QID: "Q1", Label: "Example Show", TVDB: 1234, Year: 2020},
			{QID: "Q9", Label: "Example: Next Generation", TVDB: 999, Year: 2024},
		}},
	}
	franchises := library.NewFranchises(opened)
	pass := tv.FranchisePass{Source: source, Franchises: franchises, Crossovers: library.NewCrossovers(opened), Series: service.Series, Logger: slog.New(slog.DiscardHandler)}
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}

	listed, err := franchises.InCategory(t.Context(), "tv", 7)
	if err != nil || len(listed) != 1 || listed[0].Name != "Example Franchise" || len(listed[0].Members) != 1 || listed[0].Members[0].WorkID != added.WorkID {
		t.Fatalf("collections = %+v, %v; want the franchise holding the series the library has", listed, err)
	}
	if listed[0].Total != 3 {
		t.Errorf("total = %d; want the series held, the franchise's other one and the spin-off Wikidata ties to it", listed[0].Total)
	}

	provider := tv.Franchising{Source: source, Franchises: franchises, Series: service.Series}
	missing, err := provider.MissingFranchiseMembers(t.Context(), 7, listed[0].ID)
	if err != nil || len(missing) != 2 || missing[0].Provider != "tvdb" || missing[0].ExternalID != "888" || missing[1].ExternalID != "999" {
		t.Errorf("missing = %+v, %v; want the series the library lacks, to add by their TheTVDB ids", missing, err)
	}
}

// failingAnime reads the first series' relations and then fails, as AniList does when it answers 429.
type failingAnime struct{ calls *int }

func (f failingAnime) Related(context.Context, int64) ([]anilist.Relation, error) {
	*f.calls++
	if *f.calls > 1 {
		return nil, errors.New("anilist returned 429")
	}
	return nil, nil
}

func TestTheFranchisePassAsksForAnotherWhenAniListLeavesAnimeUnread(t *testing.T) {
	service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	for _, id := range []string{"1", "2"} {
		if _, err := service.Series.Upsert(t.Context(), addNow, library.SeriesWrite{
			Work: library.WorkIdentity{MediaType: tv.MediaType, CanonicalTitle: "Anime " + id, SortTitle: "anime " + id},
			IDs:  []library.ExternalIDWrite{{Provider: tv.ProviderAniList, ExternalID: id}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	calls, asked := 0, 0
	pass := tv.FranchisePass{
		Source: fakeFranchises{}, Anime: failingAnime{calls: &calls}, Franchises: library.NewFranchises(opened), Crossovers: library.NewCrossovers(opened),
		Series: service.Series, Logger: slog.New(slog.DiscardHandler), Again: func(context.Context) { asked++ },
	}
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if asked != 1 {
		t.Errorf("another pass asked %d times, want once: one anime was read and one was not", asked)
	}
}
