package discover_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/service/discover"
)

type autoPool struct {
	items []library.PoolRecommendation
	// byList, when set, answers each list its own items instead of `items`.
	byList   map[string][]library.PoolRecommendation
	recorded []library.AutoAddRecord
	today    int64
	before   map[string]bool
}

func (p *autoPool) List(_ context.Context, query library.RecommendationQuery) ([]library.PoolRecommendation, error) {
	if p.byList != nil {
		return p.byList[query.Type], nil
	}
	return p.items, nil
}
func (p *autoPool) RecordAutoAdd(_ context.Context, record library.AutoAddRecord) error {
	p.recorded = append(p.recorded, record)
	return nil
}
func (p *autoPool) AutoAddedSince(context.Context, int64, int64) (int64, error) { return p.today, nil }
func (p *autoPool) WasAutoAdded(_ context.Context, _ int64, _, _, externalID string) (bool, error) {
	return p.before[externalID], nil
}

type adder struct {
	media.Adding
	requests []media.AddRequest
}

func (a *adder) AddWork(_ context.Context, request media.AddRequest) (media.AddResult, error) {
	a.requests = append(a.requests, request)
	return media.AddResult{WorkID: int64(len(a.requests))}, nil
}

func pooled(id string, rating float64, votes int64, genres ...string) library.PoolRecommendation {
	return library.PoolRecommendation{PoolItem: library.PoolItem{
		Kind: "movie", Provider: "tmdb:movie", ExternalID: id, Title: "Film " + id, Rating: rating, Votes: votes, OriginalLanguage: "en", Genres: genres,
	}}
}

func autoFixture(items ...library.PoolRecommendation) (discover.Service, *autoPool, *adder, config) {
	store, add := &autoPool{items: items, before: map[string]bool{}}, &adder{}
	c := enabledLibrary(10, "trending")
	c.libraries[0].MinRating, c.libraries[0].MinVotes, c.libraries[0].AutoAddPerDay = 7, 100, 2
	c.libraries[0].AutoAdd = true
	m := &module{feeds: []media.RecommendationFeed{{Type: media.RecommendTrending, Kind: "movie", Source: "Example", HasRating: true}}}
	svc := service(c, &pool{}, m, judge{})
	svc.AutoAdd = &discover.AutoAdd{Pool: store, Adders: func(string) (media.Adding, bool) { return add, true }}
	return svc, store, add, c
}

func ids(requests []media.AddRequest) []string {
	var out []string
	for _, request := range requests {
		out = append(out, request.ExternalID)
	}
	return out
}

func TestAutoAddNeedsBothGatesAndStopsAtTheDailyCeiling(t *testing.T) {
	svc, pool, add, _ := autoFixture(
		pooled("low", 6.5, 5000), pooled("few", 8, 20), pooled("a", 8, 500), pooled("b", 7.5, 900), pooled("c", 9, 9000),
	)
	if err := svc.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"c", "a"}; !reflect.DeepEqual(ids(add.requests), want) {
		t.Errorf("added %v, want %v", ids(add.requests), want)
	}
	if len(pool.recorded) != 2 || pool.recorded[0].ExternalID != "c" || pool.recorded[0].WorkID != 1 {
		t.Errorf("recorded %+v", pool.recorded)
	}
	if !add.requests[0].Monitored || add.requests[0].Search {
		t.Errorf("request %+v: want monitored, no search", add.requests[0])
	}
}

func TestAutoAddCountsWhatWasAddedToday(t *testing.T) {
	svc, pool, add, _ := autoFixture(pooled("a", 8, 500), pooled("b", 8, 500))
	pool.today = 1
	if err := svc.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"a"}; !reflect.DeepEqual(ids(add.requests), want) {
		t.Errorf("added %v, want %v", ids(add.requests), want)
	}
}

func TestAutoAddSkipsTheRatingGateForAFeedWithoutRatings(t *testing.T) {
	svc, _, add, _ := autoFixture(pooled("unrated", 0, 500), pooled("fewvotes", 0, 5))
	svc.Sources = sources{&module{feeds: []media.RecommendationFeed{{Type: media.RecommendTrending, Kind: "movie", Source: "Example"}}}}
	if err := svc.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"unrated"}; !reflect.DeepEqual(ids(add.requests), want) {
		t.Errorf("added %v, want %v", ids(add.requests), want)
	}
}

func TestAutoAddNeverAddsWhatItAddedBeforeOrAnExcludedGenre(t *testing.T) {
	svc, pool, add, c := autoFixture(pooled("again", 8, 500), pooled("gory", 8, 500, "Horror"), pooled("fine", 8, 500, "Drama"))
	pool.before["again"] = true
	c.libraries[0].ExcludeGenres = []string{"horror"}
	svc.Config = c
	if err := svc.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"fine"}; !reflect.DeepEqual(ids(add.requests), want) {
		t.Errorf("added %v, want %v", ids(add.requests), want)
	}
}

func TestAutoAddSearchesOnlyWhenAskedAndAllowed(t *testing.T) {
	for _, test := range []struct {
		name    string
		search  bool
		allowed func() bool
		want    bool
	}{
		{"asked and allowed", true, func() bool { return true }, true},
		{"asked, mode stops searches", true, func() bool { return false }, false},
		{"not asked", false, nil, false},
	} {
		svc, _, add, c := autoFixture(pooled("a", 8, 500))
		c.libraries[0].AutoAddSearch = test.search
		svc.Config = c
		svc.AutoAdd.SearchAllowed = test.allowed
		if err := svc.Run(t.Context(), jobs.Job{}); err != nil {
			t.Fatal(err)
		}
		if len(add.requests) != 1 || add.requests[0].Search != test.want {
			t.Errorf("%s: requests %+v, want search=%v", test.name, add.requests, test.want)
		}
	}
}

func TestAutoAddPreviewNamesEveryReasonAndAddsNothing(t *testing.T) {
	svc, pool, add, c := autoFixture(pooled("ok", 8, 500), pooled("weak", 5, 10, "Drama"))
	preview, err := svc.AutoAddPreview(t.Context(), c.libraries[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(add.requests) != 0 || len(pool.recorded) != 0 {
		t.Fatalf("preview wrote: %+v %+v", add.requests, pool.recorded)
	}
	want := []discover.Rejection{{Reason: discover.ReasonBelowRating, Value: "5.0"}, {Reason: discover.ReasonBelowVotes, Value: "10"}}
	if len(preview) != 2 || !preview[0].Decision.Admitted || !reflect.DeepEqual(preview[1].Decision.Rejections, want) {
		t.Errorf("preview = %+v", preview)
	}
}

func TestAutoAddMergesEveryEnabledListAndTakesTheBestRatedFirst(t *testing.T) {
	svc, pool, add, c := autoFixture()
	c.libraries[0].Types = []settings.DiscoverTypeSetting{
		{Type: "trending", Enabled: true, Amount: 20}, {Type: "classics", Enabled: true, Amount: 20}, {Type: "upcoming", Enabled: false, Amount: 20},
	}
	svc.Config = c
	pool.byList = map[string][]library.PoolRecommendation{
		"trending": {pooled("popular", 7.2, 8000), pooled("shared", 8.1, 400)},
		"classics": {pooled("best", 9.1, 700), pooled("shared", 8.1, 400), pooled("tie", 8.1, 900)},
		"upcoming": {pooled("hidden", 9.9, 9000)},
	}
	if err := svc.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"best", "tie"}; !reflect.DeepEqual(ids(add.requests), want) {
		t.Errorf("added %v, want %v: best rated first, votes breaking the tie, the disabled list left out", ids(add.requests), want)
	}
	preview, err := svc.AutoAddPreview(t.Context(), c.libraries[0])
	if err != nil {
		t.Fatal(err)
	}
	var previewed []string
	for _, item := range preview {
		if item.Decision.Admitted {
			previewed = append(previewed, item.Item.ExternalID)
		}
	}
	if want := []string{"best", "tie"}; !reflect.DeepEqual(previewed, want) {
		t.Errorf("preview admits %v, want %v", previewed, want)
	}
}
