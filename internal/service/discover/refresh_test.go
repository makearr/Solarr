package discover_test

import (
	"context"
	"encoding/json"
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
}

func (c config) Global() settings.DiscoverGlobal { return c.global }
func (c config) Libraries(context.Context) ([]settings.LibraryDiscover, error) {
	return c.libraries, nil
}
func (c config) Category(id int64) (settings.Category, bool) { return c.category, id == c.category.ID }

type replaced struct {
	ref   library.FeedRef
	items []library.PoolItem
	state library.FeedState
}

type pool struct {
	replaced []replaced
	states   []library.FeedState
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

func (m *module) RecommendationFeeds(context.Context) ([]media.RecommendationFeed, map[media.RecommendationFeed]string) {
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
