package tv_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
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

// followFixture is a library with one person (TMDB id 11) and TV's follow over a service that can add TMDB show 7.
type followFixture struct {
	following tv.Following
	reader    library.SeriesReader
	opened    *store.Store
	asked     map[int64]bool
	person    int64
}

func newFollowFixture(t *testing.T, source tv.FollowSource) followFixture {
	t.Helper()
	opened := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	asked := map[int64]bool{}
	service := tv.Service{
		Skyhook: fixedSkyhook{skyhookShow()}, TMDB: askedTMDB{fakeTMDB{series: sampleTMDB(), seasons: []tmdb.Season{sampleTMDBSeason()}}, &asked},
		Series: library.NewSeries(opened), Exclusions: library.NewExclusions(opened), Desired: library.NewDesired(opened), Clock: fake,
	}
	people := library.NewPeople(opened)
	person, err := people.ResolvePerson(t.Context(), addNow, library.PersonLink{Provider: "tmdb", ExternalID: "11", Person: library.PersonIdentity{Name: "A Person"}})
	if err != nil {
		t.Fatal(err)
	}
	following := tv.Following{
		People: people, Subscriptions: library.NewSubscriptions(opened), Source: source, Service: service,
		DefaultProfileID: 1, Categories: categories{}, Clock: fake,
	}
	return followFixture{following: following, reader: library.NewSeriesReader(opened), opened: opened, asked: asked, person: person}
}

// run is what the follow sweep does with one follow: add what is not held, up to the limit.
func (f followFixture) run(t *testing.T, followed media.Follow) {
	t.Helper()
	candidates, err := f.following.FollowCandidates(t.Context(), followed)
	if err != nil {
		t.Fatal(err)
	}
	added := 0
	for _, candidate := range candidates {
		if candidate.Held || added == f.following.FollowLimit(followed) {
			continue
		}
		if err := f.following.AddFollowed(t.Context(), followed, candidate); err != nil {
			t.Fatal(err)
		}
		added++
	}
}

func (f followFixture) held(t *testing.T) int {
	t.Helper()
	list, err := seriesIn(t.Context(), f.opened, 3)
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
	followed := media.Follow{
		SubjectType: "person", SubjectID: fixture.person, CategoryID: 3, Roles: []string{tv.RoleActor},
		FiltersJSON: string(filters), CreatedAt: addNow.Unix(),
	}

	candidates, err := fixture.following.FollowCandidates(t.Context(), followed)
	if err != nil || len(candidates) != 1 || candidates[0].ExternalID != "7" || candidates[0].Role != tv.RoleActor || candidates[0].Held {
		t.Fatalf("candidates = %+v, %v; want only the new acting credit 7, not held", candidates, err)
	}
	fixture.run(t, followed)
	if got := fixture.held(t); got != 1 {
		t.Fatalf("series held = %d, want only the new one the person acts in", got)
	}
	if len(fixture.asked) != 1 || !fixture.asked[7] {
		t.Errorf("shows looked up = %v, want only 7: the old show, the film and the directing credit are not added", fixture.asked)
	}
	list, _ := seriesIn(t.Context(), fixture.opened, 3)
	if list[0].Counts.Monitored == 0 {
		t.Errorf("counts = %+v: the follow's monitor choice (all) should apply", list[0].Counts)
	}
	if candidates, _ := fixture.following.FollowCandidates(t.Context(), followed); len(candidates) != 1 || !candidates[0].Held {
		t.Errorf("after adding, candidates = %+v; want the show held, so a second sweep adds nothing", candidates)
	}
}

func TestFollowingTheBackCatalogueWantsRegularCastOnly(t *testing.T) {
	var person tmdb.Person
	person.CombinedCredits.Cast = []tmdb.Credit{recentCredit(7, "2019-03-01", 40), recentCredit(70, "2018-01-01", 1)}
	fixture := newFollowFixture(t, fakeFollowSource{person: person})
	fixture.run(t, media.Follow{SubjectType: "person", SubjectID: fixture.person, CategoryID: 3, BackCatalogue: true, CreatedAt: addNow.Unix()})
	if got := fixture.held(t); got != 1 || len(fixture.asked) != 1 || !fixture.asked[7] {
		t.Errorf("series held = %d, shows looked up = %v, want the long-running one and not the single-episode guest spot", got, fixture.asked)
	}
	back, forward := fixture.following.FollowLimit(media.Follow{BackCatalogue: true}), fixture.following.FollowLimit(media.Follow{})
	if back != 25 || forward != 5 {
		t.Errorf("limits = %d and %d, want 25 for the back catalogue and 5 otherwise", back, forward)
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
	fixture.run(t, media.Follow{SubjectType: "organisation", SubjectID: network, CategoryID: 3, CreatedAt: addNow.Unix()})
	if got := fixture.held(t); got != 2 {
		t.Errorf("series held = %d, want the seed and the one the network listed as new", got)
	}
}

func TestFollowingTVWithoutATMDBKeyCannotFollow(t *testing.T) {
	fixture := newFollowFixture(t, nil)
	_, err := fixture.following.FollowCandidates(t.Context(), media.Follow{SubjectType: "person", SubjectID: fixture.person, CategoryID: 3})
	if !errors.Is(err, media.ErrNoFollowSource) {
		t.Errorf("err = %v, want media.ErrNoFollowSource", err)
	}
}

type fakeBridgeSource struct{ wikidata map[int64]string }

func (fakeBridgeSource) Configured() bool { return true }
func (f fakeBridgeSource) Person(_ context.Context, id int64) (tmdb.Person, error) {
	var person tmdb.Person
	person.ExternalIDs.Wikidata = f.wikidata[id]
	return person, nil
}

func TestBridgeSweepLinksTheActorWhoSharesAMusicArtistsNameAndMergesOnTheSameItem(t *testing.T) {
	opened := newLibrary(t)
	people := library.NewPeople(opened)
	artist, err := people.ResolvePerson(t.Context(), addNow, library.PersonLink{Provider: "musicbrainz:artist", ExternalID: "m1", Person: library.PersonIdentity{Name: "Vutu Doci"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := people.ResolvePerson(t.Context(), addNow, library.PersonLink{Provider: "tmdb", ExternalID: "11", Person: library.PersonIdentity{Name: "Vutu Doci"}}); err != nil {
		t.Fatal(err)
	}
	_, err = people.ResolvePerson(t.Context(), addNow, library.PersonLink{Provider: "tmdb", ExternalID: "12", Person: library.PersonIdentity{Name: "Other Name"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := people.LinkWikidata(t.Context(), artist, "Q7"); err != nil {
		t.Fatal(err)
	}
	job := tv.NewBridgeJob(tv.BridgeConfig{People: people, Source: fakeBridgeSource{wikidata: map[int64]string{11: "Q7", 12: "Q8"}}})

	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if id, found, _ := people.FindByExternalID(t.Context(), "tmdb", "11"); !found || id != artist {
		t.Errorf("tmdb 11 -> %d, found %v; want the actor folded into artist %d", id, found, artist)
	}
	if _, found, _ := people.FindByExternalID(t.Context(), "wikidata", "Q8"); found {
		t.Errorf("the stranger shares no artist's name and must not be asked about")
	}
}

type fakeArtistIDs map[string]string

func (f fakeArtistIDs) MusicBrainzArtists(_ context.Context, qids []string) (map[string]string, error) {
	answers := map[string]string{}
	for _, qid := range qids {
		if id, found := f[qid]; found {
			answers[qid] = id
		}
	}
	return answers, nil
}

func TestBridgeSweepMergesAnActorIntoTheArtistWikidataSaysTheyAre(t *testing.T) {
	opened := newLibrary(t)
	people := library.NewPeople(opened)
	artist, err := people.ResolvePerson(t.Context(), addNow, library.PersonLink{Provider: "musicbrainz:artist", ExternalID: "m1", Person: library.PersonIdentity{Name: "Vutu Doci"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := people.ResolvePerson(t.Context(), addNow, library.PersonLink{Provider: "tmdb", ExternalID: "11", Person: library.PersonIdentity{Name: "Vutu Doci"}}); err != nil {
		t.Fatal(err)
	}
	job := tv.NewBridgeJob(tv.BridgeConfig{
		People: people, Source: fakeBridgeSource{wikidata: map[int64]string{11: "Q9"}}, Wikidata: fakeArtistIDs{"Q9": "m1"},
	})

	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if id, found, _ := people.FindByExternalID(t.Context(), "tmdb", "11"); !found || id != artist {
		t.Errorf("tmdb 11 -> %d, found %v; want it folded into the artist %d although the artist carried no item", id, found, artist)
	}
}
