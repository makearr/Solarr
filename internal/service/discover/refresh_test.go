package discover_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/discover"
)

type config struct {
	global    settings.DiscoverGlobal
	libraries []settings.LibraryDiscover
	category  settings.Category
	// disabled are the provider keys switched off.
	disabled []string
}

func (c config) Global() settings.DiscoverGlobal { return c.global }
func (c config) Libraries(context.Context) ([]settings.LibraryDiscover, error) {
	return c.libraries, nil
}
func (c config) Category(id int64) (settings.Category, bool) { return c.category, id == c.category.ID }
func (c config) ProviderEnabled(key string) bool             { return !contains(c.disabled, key) }

type replaced struct {
	ref   library.FeedRef
	items []library.PoolItem
	state library.FeedState
}

type pool struct {
	replaced []replaced
	states   []library.FeedState
	// stored is what FeedState answers; appended is what AppendFeed was given.
	stored   map[library.FeedRef]library.FeedState
	appended []replaced
}

func (p *pool) FeedState(_ context.Context, ref library.FeedRef) (library.FeedState, error) {
	return p.stored[ref], nil
}

func (p *pool) AppendFeed(_ context.Context, ref library.FeedRef, items []library.PoolItem, _ int64, state library.FeedState, _ int64) error {
	p.appended = append(p.appended, replaced{ref, items, state})
	return nil
}

func (p *pool) ReplaceFeed(_ context.Context, ref library.FeedRef, items []library.PoolItem, state library.FeedState, _ int64) error {
	p.replaced = append(p.replaced, replaced{ref, items, state})
	return nil
}

func (p *pool) SetFeedState(_ context.Context, _ library.FeedRef, state library.FeedState) error {
	p.states = append(p.states, state)
	return nil
}

type module struct {
	feeds []media.RecommendationFeed
	pages map[media.RecommendationType][][]media.Recommendation
	fail  map[media.RecommendationType]error
	asked []media.RecommendQuery
}

func (m *module) RecommendationFeeds(context.Context, int64) ([]media.RecommendationFeed, map[media.RecommendationFeed]string) {
	return m.feeds, nil
}

func (m *module) Recommend(_ context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	m.asked = append(m.asked, query)
	if err := m.fail[query.Feed.Type]; err != nil {
		return nil, false, err
	}
	pages := m.pages[query.Feed.Type]
	if query.Page > len(pages) {
		return nil, false, nil
	}
	return pages[query.Page-1], query.Page < len(pages), nil
}

type sources struct{ module media.Recommending }

func (s sources) Feeds(mediaType string) (media.Recommending, bool) {
	return s.module, mediaType == "movie" && s.module != nil
}

type judge struct{ monitored map[string]bool }

func (judge) RatingLimit(settings.Category) discover.RatingLimit {
	return discover.RatingLimit{MaxAge: -1}
}
func (judge) DefaultLanguages(settings.Category) []string { return []string{"en"} }
func (j judge) FactsOf(_ context.Context, _ settings.Category, item media.Recommendation) (discover.Facts, int64, error) {
	return discover.Facts{Monitored: j.monitored[item.ExternalID]}, 0, nil
}

func titles(count int, prefix string) []media.Recommendation {
	items := make([]media.Recommendation, count)
	for index := range items {
		id := fmt.Sprintf("%s%d", prefix, index)
		items[index] = media.Recommendation{Provider: "tmdb:movie", ExternalID: id, Title: "Film " + id, Traits: media.Traits{OriginalLanguage: "en", Genres: []string{"Drama"}}}
	}
	return items
}

func types(amount int64, enabled ...string) []settings.DiscoverTypeSetting {
	var out []settings.DiscoverTypeSetting
	for _, name := range settings.DiscoverTypes {
		out = append(out, settings.DiscoverTypeSetting{Type: name, Amount: amount, Enabled: contains(enabled, name)})
	}
	return out
}

func contains(list []string, value string) bool {
	for _, one := range list {
		if one == value {
			return true
		}
	}
	return false
}

func service(c config, p *pool, m media.Recommending, j judge) discover.Service {
	return discover.Service{Config: c, Pool: p, Sources: sources{m}, Judge: j, Clock: clock.NewFake(time.Unix(1000, 0))}
}

func enabledLibrary(amount int64, enabled ...string) config {
	return config{
		global:    settings.DiscoverGlobal{Enabled: true},
		libraries: []settings.LibraryDiscover{{CategoryID: 1, Enabled: true, ClassicAgeYears: 25, Types: types(amount, enabled...)}},
		category:  settings.Category{ID: 1, MediaType: "movie"},
	}
}

func TestRefreshStopsReadingOnceTheAmountIsAdmitted(t *testing.T) {
	m := &module{
		feeds: []media.RecommendationFeed{{Type: media.RecommendTrending, Source: "Example"}},
		pages: map[media.RecommendationType][][]media.Recommendation{media.RecommendTrending: {titles(3, "a"), titles(3, "b"), titles(3, "c")}},
	}
	p := &pool{}
	if err := service(enabledLibrary(4, "trending"), p, m, judge{monitored: map[string]bool{"a1": true}}).Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if len(m.asked) != 2 {
		t.Errorf("pages read = %d, want 2", len(m.asked))
	}
	if len(p.replaced) != 1 || len(p.replaced[0].items) != 4 {
		t.Fatalf("replaced = %+v", p.replaced)
	}
	var ids []string
	for _, item := range p.replaced[0].items {
		ids = append(ids, item.ExternalID)
	}
	if want := []string{"a0", "a2", "b0", "b1"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("admitted %v, want %v", ids, want)
	}
	want := library.FeedState{FeedRef: library.FeedRef{CategoryID: 1, Type: "trending"}, PagesRead: 2, RefreshedAt: 1000}
	if p.replaced[0].state != want {
		t.Errorf("state = %+v, want %+v", p.replaced[0].state, want)
	}
	if m.asked[0].Languages[0] != "en" || m.asked[0].MaxAge != -1 || m.asked[0].ClassicAgeYears != 25 {
		t.Errorf("query = %+v", m.asked[0])
	}
}

func TestRefreshMarksAFeedExhaustedAndFiltersByExclusions(t *testing.T) {
	items := titles(2, "a")
	items[1].Traits.Genres = []string{"Reality"}
	m := &module{feeds: []media.RecommendationFeed{{Type: media.RecommendUpcoming}}, pages: map[media.RecommendationType][][]media.Recommendation{media.RecommendUpcoming: {items}}}
	c := enabledLibrary(10, "upcoming")
	c.global.ExcludeGenres = []string{"reality"}
	p := &pool{}
	if err := service(c, p, m, judge{}).Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if len(p.replaced[0].items) != 1 || !p.replaced[0].state.Exhausted {
		t.Errorf("replaced = %+v", p.replaced)
	}
}

func TestOneFailingFeedKeepsItsRowsAndTheOthersGoOn(t *testing.T) {
	m := &module{
		feeds: []media.RecommendationFeed{{Type: media.RecommendTrending}, {Type: media.RecommendHighestRated}},
		pages: map[media.RecommendationType][][]media.Recommendation{media.RecommendHighestRated: {titles(2, "r")}},
		fail:  map[media.RecommendationType]error{media.RecommendTrending: fmt.Errorf("down: %w", media.ErrSourceUnavailable)},
	}
	p := &pool{}
	if err := service(enabledLibrary(5, "trending", "highest_rated"), p, m, judge{}).Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if len(p.replaced) != 1 || p.replaced[0].ref.Type != "highest_rated" {
		t.Errorf("replaced = %+v", p.replaced)
	}
	if len(p.states) != 1 || p.states[0].LastError != discover.FailureSourceUnavailable || p.states[0].Type != "trending" {
		t.Errorf("states = %+v", p.states)
	}
}

func TestNothingIsReadWhenDiscoverOrTheLibraryIsOff(t *testing.T) {
	m := &module{feeds: []media.RecommendationFeed{{Type: media.RecommendTrending}}, pages: map[media.RecommendationType][][]media.Recommendation{media.RecommendTrending: {titles(1, "a")}}}
	off := enabledLibrary(5, "trending")
	off.global.Enabled = false
	libraryOff := enabledLibrary(5, "trending")
	libraryOff.libraries[0].Enabled = false
	typeOff := enabledLibrary(5)
	other := enabledLibrary(5, "trending")
	payload, _ := json.Marshal(discover.Payload{CategoryID: 2})
	for name, run := range map[string]func() error{
		"discover off": func() error { return service(off, &pool{}, m, judge{}).Run(t.Context(), jobs.Job{}) },
		"library off":  func() error { return service(libraryOff, &pool{}, m, judge{}).Run(t.Context(), jobs.Job{}) },
		"type off":     func() error { return service(typeOff, &pool{}, m, judge{}).Run(t.Context(), jobs.Job{}) },
		"other library": func() error {
			return service(other, &pool{}, m, judge{}).Run(t.Context(), jobs.Job{Payload: payload})
		},
	} {
		if err := run(); err != nil || len(m.asked) != 0 {
			t.Errorf("%s: asked %d, err %v", name, len(m.asked), err)
		}
	}
}

func TestADisabledKindIsSkipped(t *testing.T) {
	m := &module{
		feeds: []media.RecommendationFeed{{Type: media.RecommendTrending, Kind: "album"}, {Type: media.RecommendTrending, Kind: "artist"}},
		pages: map[media.RecommendationType][][]media.Recommendation{media.RecommendTrending: {titles(1, "a")}},
	}
	c := enabledLibrary(5, "trending")
	c.libraries[0].DisabledKinds = []string{"artist"}
	p := &pool{}
	if err := service(c, p, m, judge{}).Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if len(p.replaced) != 1 || p.replaced[0].ref.Kind != "album" || p.replaced[0].items[0].Kind != "album" {
		t.Errorf("replaced = %+v", p.replaced)
	}
}

type collections struct {
	items []media.Recommendation
	err   error
}

func (c collections) Candidates(context.Context, settings.Category, int64) ([]media.Recommendation, error) {
	return c.items, c.err
}

func TestRefreshBuildsTheCollectionsListFromTheLibraryAndAdmitsLikeAnyFeed(t *testing.T) {
	items := titles(3, "m")
	items[0].Subtitle = "Example Saga"
	p := &pool{}
	svc := service(enabledLibrary(2, "collections"), p, &module{}, judge{monitored: map[string]bool{"m1": true}})
	svc.Collections = collections{items: items}
	if err := svc.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if len(p.replaced) != 1 || p.replaced[0].ref != (library.FeedRef{CategoryID: 1, Type: "collections"}) {
		t.Fatalf("replaced = %+v", p.replaced)
	}
	var ids []string
	for _, item := range p.replaced[0].items {
		ids = append(ids, item.ExternalID)
	}
	if want := []string{"m0", "m2"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("admitted %v, want %v", ids, want)
	}
	if p.replaced[0].items[0].Subtitle != "Example Saga" {
		t.Errorf("subtitle = %q", p.replaced[0].items[0].Subtitle)
	}
}

func TestRefreshRecordsACollectionsFailureAndKeepsTheOldList(t *testing.T) {
	p := &pool{}
	svc := service(enabledLibrary(2, "collections"), p, &module{}, judge{})
	svc.Collections = collections{err: fmt.Errorf("read failed")}
	if err := svc.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if len(p.replaced) != 0 || len(p.states) != 1 || p.states[0].LastError != discover.FailureOther {
		t.Errorf("replaced = %+v, states = %+v", p.replaced, p.states)
	}
}

func TestMoreReadsTheNextPageAndSkipsWhatIsHeld(t *testing.T) {
	m := &module{
		feeds: []media.RecommendationFeed{{Type: media.RecommendTrending, Source: "Example"}},
		pages: map[media.RecommendationType][][]media.Recommendation{media.RecommendTrending: {titles(2, "a"), titles(2, "b"), titles(2, "c")}},
	}
	ref := library.FeedRef{CategoryID: 1, Type: "trending"}
	p := &pool{stored: map[library.FeedRef]library.FeedState{ref: {FeedRef: ref, PagesRead: 1}}}
	result, err := service(enabledLibrary(4, "trending"), p, m, judge{monitored: map[string]bool{"b0": true}}).More(t.Context(), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.asked) != 1 || m.asked[0].Page != 2 {
		t.Errorf("asked %+v, want page 2 only", m.asked)
	}
	if result != (discover.MoreResult{Added: 1}) || len(p.appended) != 1 || p.appended[0].state.PagesRead != 2 || p.appended[0].items[0].ExternalID != "b1" {
		t.Errorf("result %+v appended %+v", result, p.appended)
	}
}

func TestMoreKeepsReadingWhenAPageAdmitsNothingAndStopsAtTheEnd(t *testing.T) {
	m := &module{
		feeds: []media.RecommendationFeed{{Type: media.RecommendTrending, Source: "Example"}},
		pages: map[media.RecommendationType][][]media.Recommendation{media.RecommendTrending: {titles(1, "a"), titles(1, "b"), titles(1, "c")}},
	}
	p := &pool{}
	held := judge{monitored: map[string]bool{"a0": true, "b0": true}}
	result, err := service(enabledLibrary(4, "trending"), p, m, held).More(t.Context(), 1, "trending")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.asked) != 3 || result != (discover.MoreResult{Added: 1, Exhausted: true}) {
		t.Errorf("asked %d pages, result %+v", len(m.asked), result)
	}
	if _, err := service(enabledLibrary(4, "trending"), p, m, held).More(t.Context(), 1, "classics"); !errors.Is(err, discover.ErrNothingToRead) {
		t.Errorf("a list that is off: err = %v", err)
	}
}

func popular(count int, prefix string, highest float64) []media.Recommendation {
	items := titles(count, prefix)
	for index := range items {
		items[index].Popularity = highest / float64(index+1)
	}
	return items
}

func TestRefreshReadsEverySourceOfAListSplitsTheAmountAndScalesPopularity(t *testing.T) {
	first := &module{
		feeds: []media.RecommendationFeed{{Type: media.RecommendTrending, Source: "First", SourceKey: "first"}},
		pages: map[media.RecommendationType][][]media.Recommendation{media.RecommendTrending: {popular(3, "a", 50000)}},
	}
	second := &module{
		feeds: []media.RecommendationFeed{{Type: media.RecommendTrending, Source: "Second", SourceKey: "second"}},
		pages: map[media.RecommendationType][][]media.Recommendation{media.RecommendTrending: {popular(3, "b", 40)}},
	}
	p := &pool{}
	if err := service(enabledLibrary(4, "trending"), p, media.JoinedFeeds{first, second}, judge{}).Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	got := map[string][]float64{}
	for _, one := range p.replaced {
		for _, item := range one.items {
			got[one.ref.Source] = append(got[one.ref.Source], item.Popularity)
		}
		if one.state.Source != one.ref.Source {
			t.Errorf("state %+v of ref %+v", one.state, one.ref)
		}
	}
	want := map[string][]float64{"first": {100, 50}, "second": {100, 50}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("popularity by source = %v, want %v", got, want)
	}
	if p.replaced[0].state.PopularityMax != 50000 || p.replaced[1].state.PopularityMax != 40 {
		t.Errorf("states = %+v, %+v", p.replaced[0].state, p.replaced[1].state)
	}
}

func TestRefreshLeavesTheFeedsOfASwitchedOffProviderUnread(t *testing.T) {
	first := &module{
		feeds: []media.RecommendationFeed{{Type: media.RecommendTrending, SourceKey: "first"}},
		pages: map[media.RecommendationType][][]media.Recommendation{media.RecommendTrending: {titles(5, "a")}},
	}
	second := &module{feeds: []media.RecommendationFeed{{Type: media.RecommendTrending, SourceKey: "second"}}}
	c := enabledLibrary(4, "trending")
	c.disabled = []string{"second"}
	p := &pool{}
	if err := service(c, p, media.JoinedFeeds{first, second}, judge{}).Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if len(second.asked) != 0 || len(p.replaced) != 1 || len(p.replaced[0].items) != 4 {
		t.Errorf("second asked %d times, replaced = %+v", len(second.asked), p.replaced)
	}
}
