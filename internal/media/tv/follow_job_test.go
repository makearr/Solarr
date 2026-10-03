package tv_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type fakeFollowSource struct {
	person tmdb.Person
	hits   []tmdb.Hit
}

func (fakeFollowSource) Configured() bool { return true }
func (f fakeFollowSource) Person(context.Context, int64) (tmdb.Person, error) {
	return f.person, nil
}
func (f fakeFollowSource) NewOnNetwork(context.Context, int64, string) ([]tmdb.Hit, error) {
	return f.hits, nil
}

// askedTMDB records which shows the service looked up, since the fake answers every id with the same show.
type askedTMDB struct {
	fakeTMDB
	asked *map[int64]bool
}

func (a askedTMDB) Series(ctx context.Context, id int64) (tmdb.Series, error) {
	(*a.asked)[id] = true
	return a.fakeTMDB.Series(ctx, id)
}

// followFixture is a library with one followed person (TMDB id 11) and a service that can add TMDB show 7.
type followFixture struct {
	job    *tv.FollowJob
	subs   library.Subscriptions
	reader library.SeriesReader
	opened *store.Store
	asked  map[int64]bool
	person int64
	at     time.Time
}

func newFollowFixture(t *testing.T, source tv.FollowSource) followFixture {
	t.Helper()
	opened := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	asked := map[int64]bool{}
	service := tv.Service{
		Skyhook: fixedSkyhook{skyhookShow()}, TMDB: askedTMDB{fakeTMDB{series: sampleTMDB(), seasons: []tmdb.Season{sampleTMDBSeason()}}, &asked},
		Series: library.NewSeries(opened), Desired: library.NewDesired(opened), Clock: fake,
	}
	people := library.NewPeople(opened)
	person, err := people.ResolvePerson(t.Context(), addNow, library.PersonLink{Provider: "tmdb", ExternalID: "11", Person: library.PersonIdentity{Name: "A Person"}})
	if err != nil {
		t.Fatal(err)
	}
	subs := library.NewSubscriptions(opened)
	job := tv.NewFollowJob(tv.FollowConfig{
		Subscriptions: subs, People: people, Source: source, Service: service, DefaultProfileID: 1, Categories: categories{}, Clock: fake,
	})
	return followFixture{job: job, subs: subs, reader: library.NewSeriesReader(opened), opened: opened, asked: asked, person: person, at: addNow}
}

func (f followFixture) run(t *testing.T) {
	t.Helper()
	if err := f.job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
}

func (f followFixture) held(t *testing.T) int {
	t.Helper()
	list, err := f.reader.List(t.Context(), 3, library.SeriesCursor{}, 10, f.at)
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func recentCredit(id int64, date string, episodes int) tmdb.Credit {
	return tmdb.Credit{ID: id, MediaType: "tv", Name: "Example Show", FirstAirDate: date, EpisodeCount: episodes}
}

func TestFollowingAPersonAddsTheirNewSeriesOnceAndOnlyInTheFollowedRoles(t *testing.T) {
	var person tmdb.Person
	recent := addNow.AddDate(0, 0, -10).Format("2006-01-02")
	person.CombinedCredits.Cast = []tmdb.Credit{
		recentCredit(7, recent, 1),
		recentCredit(70, "2019-03-01", 40),
		{ID: 71, MediaType: "movie", Title: "A Film", ReleaseDate: recent},
	}
	person.CombinedCredits.Crew = []tmdb.Credit{{ID: 72, MediaType: "tv", Name: "Directed", FirstAirDate: recent, Job: "Director"}}
	fixture := newFollowFixture(t, fakeFollowSource{person: person})
	filters, _ := json.Marshal(tv.FollowFilters{Monitor: "all"})
	if _, err := fixture.subs.Follow(t.Context(), addNow, "person", fixture.person, 1, tv.MediaType,
		library.FollowRequest{CategoryID: 3, Roles: []string{tv.RoleActor}, FiltersJSON: string(filters)}); err != nil {
		t.Fatal(err)
	}

	fixture.run(t)
	if got := fixture.held(t); got != 1 {
		t.Fatalf("series held = %d, want only the new one the person acts in", got)
	}
	if len(fixture.asked) != 1 || !fixture.asked[7] {
		t.Errorf("shows looked up = %v, want only 7: the old show, the film and the directing credit are not added", fixture.asked)
	}
	list, _ := fixture.reader.List(t.Context(), 3, library.SeriesCursor{}, 10, addNow)
	if list[0].Counts.Monitored == 0 {
		t.Errorf("counts = %+v: the follow's monitor choice (all) should apply", list[0].Counts)
	}
	fixture.run(t)
	if got := fixture.held(t); got != 1 {
		t.Errorf("series held after a second sweep = %d, want no duplicate", got)
	}
}

func TestFollowingTheBackCatalogueWantsRegularCastOnly(t *testing.T) {
	var person tmdb.Person
	person.CombinedCredits.Cast = []tmdb.Credit{recentCredit(7, "2019-03-01", 40), recentCredit(70, "2018-01-01", 1)}
	fixture := newFollowFixture(t, fakeFollowSource{person: person})
	if _, err := fixture.subs.Follow(t.Context(), addNow, "person", fixture.person, 1, tv.MediaType,
		library.FollowRequest{CategoryID: 3, BackCatalogue: true}); err != nil {
		t.Fatal(err)
	}
	fixture.run(t)
	if got := fixture.held(t); got != 1 || len(fixture.asked) != 1 || !fixture.asked[7] {
		t.Errorf("series held = %d, shows looked up = %v, want the long-running one and not the single-episode guest spot", got, fixture.asked)
	}
}

func TestFollowingANetworkAddsWhatTMDBListsAsNew(t *testing.T) {
	fixture := newFollowFixture(t, fakeFollowSource{hits: []tmdb.Hit{{ID: 7, Name: "Example Show", FirstAirDate: "2026-09-20"}}})
	seed := library.SeriesWrite{
		Work: library.WorkIdentity{MediaType: "tv", CanonicalTitle: "Seed", SortTitle: "seed"}, IDs: []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: "999"}},
		Network: "Net", MoreNetworks: []library.SeriesNetworkWrite{{Name: "Net", Provider: "tmdb", ExternalID: "9"}},
	}
	result, err := library.NewSeries(fixture.opened).Upsert(t.Context(), addNow, seed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewDesired(fixture.opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 3, ProfileID: 1}); err != nil {
		t.Fatal(err)
	}
	var network int64
	if err := fixture.opened.Read().QueryRowContext(t.Context(), `SELECT id FROM organisation WHERE name = 'Net'`).Scan(&network); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.subs.Follow(t.Context(), addNow, "organisation", network, 1, tv.MediaType, library.FollowRequest{CategoryID: 3}); err != nil {
		t.Fatal(err)
	}
	fixture.run(t)
	if got := fixture.held(t); got != 2 {
		t.Errorf("series held = %d, want the seed and the one the network listed as new", got)
	}
}

func TestAFollowOfAnotherMediaTypeIsNotRewritten(t *testing.T) {
	fixture := newFollowFixture(t, fakeFollowSource{})
	if _, err := fixture.subs.Follow(t.Context(), addNow, "person", fixture.person, 1, "music", library.FollowRequest{CategoryID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.subs.Follow(t.Context(), addNow, "person", fixture.person, 1, tv.MediaType, library.FollowRequest{CategoryID: 3}); err == nil {
		t.Error("a TV follow overwrote a music subscription")
	}
}

func TestUnfollowRemovesTheFollow(t *testing.T) {
	fixture := newFollowFixture(t, fakeFollowSource{})
	if _, err := fixture.subs.Follow(t.Context(), addNow, "person", fixture.person, 1, tv.MediaType, library.FollowRequest{CategoryID: 3}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := fixture.subs.FollowOf(t.Context(), "person", fixture.person, 1); !found {
		t.Fatal("follow not stored")
	}
	if err := fixture.subs.Unfollow(t.Context(), "person", fixture.person, 1); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := fixture.subs.FollowOf(t.Context(), "person", fixture.person, 1); found {
		t.Error("follow still there after unfollow")
	}
}
