package adult

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parseadult"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
	"github.com/makearr/Solarr/internal/metadata/theporndb"
	"github.com/makearr/Solarr/internal/metadata/whisparr"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type fakeServer struct {
	scenes   []stashbox.Scene
	asked    []string
	byPrints map[string]stashbox.Scene
}

func (*fakeServer) Configured() bool { return true }

func (f *fakeServer) SearchScenes(_ context.Context, term string, _ int) ([]stashbox.Scene, error) {
	f.asked = append(f.asked, "search:"+term)
	return f.scenes, nil
}

func (f *fakeServer) FindScene(_ context.Context, id string) (stashbox.Scene, error) {
	f.asked = append(f.asked, "find:"+id)
	for _, scene := range f.scenes {
		if scene.ID == id {
			return scene, nil
		}
	}
	return stashbox.Scene{}, stashbox.ErrNotFound
}

func (f *fakeServer) ScenesByFingerprint(_ context.Context, files [][]stashbox.Fingerprint) ([][]stashbox.Scene, error) {
	out := make([][]stashbox.Scene, len(files))
	for i, file := range files {
		if scene, ok := f.byPrints[file[0].Hash]; ok {
			out[i] = []stashbox.Scene{scene}
		}
	}
	return out, nil
}

func (*fakeServer) FindPerformer(context.Context, string) (stashbox.Performer, error) {
	return stashbox.Performer{}, stashbox.ErrNotFound
}

func (f *fakeServer) SearchPerformers(context.Context, string, int) ([]stashbox.Performer, error) {
	return nil, nil
}

func (f *fakeServer) ScenesOf(_ context.Context, filter stashbox.SceneFilter, _, _ int) ([]stashbox.Scene, error) {
	f.asked = append(f.asked, "of:"+filter.PerformerID+filter.StudioID)
	return f.scenes, nil
}

type fakeWhisparr struct{ scenes []whisparr.Scene }

func (f fakeWhisparr) SearchScenes(context.Context, string) ([]whisparr.Scene, error) {
	return f.scenes, nil
}

func stashScene() stashbox.Scene {
	return stashbox.Scene{
		ID: "sb-1", Title: "Class Two", Details: "A lesson.", ReleaseDate: "2020-05-14", Duration: 600, Code: "E1",
		Studio:     &stashbox.Studio{ID: "st-1", Name: "Night Studio", Parent: &stashbox.Studio{ID: "st-0", Name: "Night Network"}},
		Tags:       []stashbox.Tag{{Name: "Teacher"}},
		Performers: []stashbox.Appearance{{As: "Kiky", Performer: stashbox.Performer{ID: "p-1", Name: "Ada Vale", Gender: "FEMALE"}}},
	}
}

func whisparrScene() whisparr.Scene {
	return whisparr.Scene{
		Title: "Class Two", ReleaseDate: "2020-05-14", Duration: 10, ForeignGUID: "tp-1", Genres: []string{"Teacher", "Blowjob"},
		Credits: []whisparr.Credit{{Name: "Kiky", ForeignGUID: "tp-p1", Gender: "Female"}},
	}
}

func TestFetchForTakesTheFirstSourcesFieldsAndFillsWhatItLacksFromTheNext(t *testing.T) {
	service := Service{
		Whisparr: fakeWhisparr{scenes: []whisparr.Scene{whisparrScene()}},
		Servers:  map[string]Server{ProviderStashDB: &fakeServer{scenes: []stashbox.Scene{stashScene()}}},
		Sources:  []string{"wikidata", "whisparr", ProviderStashDB},
	}
	scene, err := service.FetchFor(t.Context(), 0, IDs{}, Hint{Title: "Class Two", Date: "2020-05-14"})
	if err != nil {
		t.Fatal(err)
	}
	if scene.IDs != (IDs{StashDB: "sb-1", ThePornDB: "tp-1"}) {
		t.Errorf("ids = %+v, want both servers' ids", scene.IDs)
	}
	if scene.Studio != "Night Studio" || scene.Network != "Night Network" || scene.Overview != "A lesson." || scene.Code != "E1" {
		t.Errorf("scene = %+v, want what only StashDB holds filled in", scene)
	}
	if want := []string{"Kiky"}; !reflect.DeepEqual(scene.Performers(), want) {
		t.Errorf("performers = %v, want Whisparr's credits only (%v)", scene.Performers(), want)
	}
	if want := []string{"Teacher", "Blowjob"}; !reflect.DeepEqual(scene.Tags, want) {
		t.Errorf("tags = %v, want %v", scene.Tags, want)
	}
}

func TestFetchForRefusesAnotherScenesAnswer(t *testing.T) {
	other := stashScene()
	other.Title, other.ReleaseDate = "Another Night", "2019-01-01"
	service := Service{Servers: map[string]Server{ProviderStashDB: &fakeServer{scenes: []stashbox.Scene{other}}}, Sources: []string{ProviderStashDB}}
	if _, err := service.FetchFor(t.Context(), 0, IDs{}, Hint{Title: "Class Two", Date: "2020-05-14"}); !errors.Is(err, ErrNoSource) {
		t.Errorf("err = %v, want ErrNoSource", err)
	}
}

func TestByFingerprintNeedsExactlyOneSceneAtAServer(t *testing.T) {
	server := &fakeServer{byPrints: map[string]stashbox.Scene{"abc": stashScene()}}
	service := Service{Servers: map[string]Server{ProviderStashDB: server}, Sources: []string{ProviderStashDB}}
	scene, found := service.ByFingerprint(t.Context(), 0, []stashbox.Fingerprint{{Hash: "abc", Algorithm: stashbox.AlgorithmOSHash}})
	if !found || scene.IDs.StashDB != "sb-1" {
		t.Errorf("found = %v, scene = %+v", found, scene)
	}
	if _, found := service.ByFingerprint(t.Context(), 0, []stashbox.Fingerprint{{Hash: "zzz"}}); found {
		t.Error("an unknown hash identified a scene")
	}
}

func TestPickCandidateTakesTheOneSceneOfThatTitleAndDay(t *testing.T) {
	candidates := []Candidate{
		{IDs: IDs{StashDB: "a"}, Title: "Class Two", Date: "2019-02-02"},
		{IDs: IDs{StashDB: "b"}, Title: "Class Two", Date: "2020-05-14"},
		{IDs: IDs{StashDB: "c"}, Title: "Something Else", Date: "2020-05-14"},
	}
	pick, ok := pickCandidate(candidates, parseadult.Info{Title: "Class Two", Date: "2020-05-14"})
	if !ok || pick.IDs.StashDB != "b" {
		t.Errorf("pick = %+v, %v; want b", pick, ok)
	}
	if _, ok := pickCandidate(candidates[:1], parseadult.Info{Title: "Class Two", Date: "2020-05-14"}); ok {
		t.Error("a scene of another day was taken")
	}
	twice := []Candidate{{IDs: IDs{StashDB: "x"}, Title: "Class Two", Date: "2020-05-14"}, {IDs: IDs{ThePornDB: "y"}, Title: "Class Two", Date: "2020-05-14"}}
	if pick, ok := pickCandidate(twice, parseadult.Info{Title: "Class Two", Date: "2020-05-14"}); !ok || pick.IDs.StashDB != "x" {
		t.Errorf("one scene listed twice: pick = %+v, %v; want the first", pick, ok)
	}
	two := []Candidate{{Title: "Class Two"}, {Title: "Class Two"}}
	if _, ok := pickCandidate(two, parseadult.Info{Title: "Class Two"}); ok {
		t.Error("two equal hits with no day to tell them apart were guessed")
	}
	suffixed, ok := pickCandidate([]Candidate{{IDs: IDs{StashDB: "d"}, Title: "Class Two", Date: "2020-05-14"}}, parseadult.Info{Title: "Class Two - Scene 3", Date: "2020-05-14"})
	if !ok || suffixed.IDs.StashDB != "d" {
		t.Errorf("a scene number after the title kept the scene from matching: %+v, %v", suffixed, ok)
	}
}

func TestToWriteStoresStudioPerformersAndTheReleaseDay(t *testing.T) {
	scene := FromStashBox(ProviderStashDB, stashScene())
	write := ToWrite(scene, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if write.Network != "Night Studio" || write.OrganisationKind != "studio" || len(write.MoreNetworks) != 2 ||
		write.MoreNetworks[0].ExternalID != "st-1" || write.MoreNetworks[0].Provider != "stashdb:studio" || write.MoreNetworks[1].Name != "Night Network" || write.MoreNetworks[1].ExternalID != "st-0" {
		t.Errorf("studio = %q (%s), more = %+v", write.Network, write.OrganisationKind, write.MoreNetworks)
	}
	if len(write.Credits) != 1 || write.Credits[0].Provider != "stashdb:performer" || write.Credits[0].ExternalID != "p-1" || write.Credits[0].Role != RolePerformer || write.Credits[0].Character != "Kiky" {
		t.Errorf("credits = %+v", write.Credits)
	}
	if len(write.Dates) != 1 || write.Dates[0].Date != "2020-05-14" || *write.RuntimeMs != 600_000 {
		t.Errorf("dates = %+v, runtime = %v", write.Dates, write.RuntimeMs)
	}
	future := FromStashBox(ProviderStashDB, stashScene())
	future.Date = "2030-01-01"
	if future.StatusOn(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) != StatusAnnounced {
		t.Error("a scene dated in the future is announced")
	}
}

func TestNamingRendersTheStudioFolderAndTheSceneBelowIt(t *testing.T) {
	facts := FileNaming{Studio: "Night Studio", Title: "Class Two", Date: "2020-05-14", Performers: []string{"Ada Vale", "Bo Lane"}}
	got, _, err := NamingFor(nil).RenderScene(facts, "x.mp4")
	if want := "Night Studio/Class Two (2020-05-14)/[Night Studio] Class Two (2020-05-14).mp4"; err != nil || got != want {
		t.Errorf("default = %q, %v; want %q", got, err, want)
	}
	profile := DefaultNamingProfile()
	profile.Formats = map[string]string{SlotStudioFolder: "{Studio Name}", SlotStandardScene: "{Studio Name} - {Release Date} - {Performer Names} - {Scene Title}"}
	preset := NamingFor(&profile)
	got, _, err = preset.RenderScene(facts, "x.mp4")
	if want := "Night Studio/Night Studio - 2020-05-14 - Ada Vale, Bo Lane - Class Two.mp4"; err != nil || got != want {
		t.Errorf("with performers = %q, %v; want %q", got, err, want)
	}
	unknown, _, _ := NamingFor(nil).RenderScene(FileNaming{Title: "Lone"}, "x.mp4")
	if want := "Unknown Studio/Lone/[Unknown Studio] Lone.mp4"; unknown != want {
		t.Errorf("no studio = %q, want %q", unknown, want)
	}
}

func TestPerformerDetailsGivesTheColumnsAndThePageFacts(t *testing.T) {
	identity, extras := PerformerDetails(stashbox.Performer{
		Name: "Ada Vale", Gender: "TRANSGENDER_FEMALE", Country: "CZ", BirthDate: "1990-04-03", Aliases: []string{"Kiky"}, Ethnicity: "CAUCASIAN",
		HairColor: "BLACK", Height: 165, BandSize: 34, CupSize: "D", WaistSize: 24, CareerStartYear: 2005, BreastType: "NATURAL",
		Tattoos:    []stashbox.BodyModification{{Location: "Left wrist", Description: "script"}},
		URLs:       []stashbox.Link{{URL: "https://www.example.org/ada", Type: "INSTAGRAM"}, {URL: "https://www.site.test/a"}},
		Images:     []stashbox.Image{{URL: "https://img/1"}, {URL: "https://img/2"}},
		SceneCount: 7,
	})
	if identity.Gender != "Trans woman" || identity.Country != "CZ" || identity.Born != "1990-04-03" || identity.ImageURL != "https://img/1" || identity.Kind != "Performer" {
		t.Errorf("identity = %+v", identity)
	}
	want := []library.PersonFact{
		{Label: "Ethnicity", Value: "Caucasian"}, {Label: "Hair colour", Value: "Black"}, {Label: "Height", Value: "165 cm"},
		{Label: "Measurements", Value: "34D-24-?"}, {Label: "Breasts", Value: "Natural"}, {Label: "Career", Value: "2005 - present"},
		{Label: "Tattoos", Value: "Left wrist: script"}, {Label: "Scenes on the server", Value: "7"},
	}
	if !reflect.DeepEqual(extras.Facts, want) {
		t.Errorf("facts = %+v, want %+v", extras.Facts, want)
	}
	if len(extras.Links) != 2 || extras.Links[0].Label != "Instagram" || extras.Links[1].Label != "site.test" || len(extras.Images) != 2 {
		t.Errorf("links = %+v, images = %v", extras.Links, extras.Images)
	}
}

type fakeIDs struct{ ids map[string]string }

func (f fakeIDs) ExternalIDFor(_ context.Context, _ int64, provider string) (string, bool, error) {
	id, ok := f.ids[provider]
	return id, ok, nil
}

func (f fakeIDs) OrganisationExternalID(_ context.Context, _ int64, provider string) (string, bool, error) {
	return f.ExternalIDFor(context.Background(), 0, provider)
}

func TestFollowListsOnlyScenesOutSinceTheFollowUnlessItAsksForTheBackCatalogue(t *testing.T) {
	old, fresh, future := stashScene(), stashScene(), stashScene()
	old.ID, old.ReleaseDate = "old", "2015-01-01"
	fresh.ID, fresh.ReleaseDate = "fresh", "2026-10-30"
	future.ID, future.ReleaseDate = "future", "2027-01-01"
	server := &fakeServer{scenes: []stashbox.Scene{future, fresh, old}}
	following := Following{
		Service: Service{Servers: map[string]Server{ProviderStashDB: server}},
		People:  fakeIDs{ids: map[string]string{"stashdb:performer": "p-1"}},
		Now:     func() time.Time { return time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC) },
		Held:    func(context.Context, IDs) (int64, bool) { return 0, false },
	}
	follow := media.Follow{SubjectType: "person", SubjectID: 1, CreatedAt: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC).Unix()}
	got, err := following.FollowCandidates(t.Context(), follow)
	if err != nil || len(got) != 1 || got[0].ExternalID != "fresh" || got[0].Provider != ProviderStashDB || got[0].Role != RolePerformer {
		t.Fatalf("new scenes = %+v, %v; want only fresh", got, err)
	}
	if want := []string{"of:p-1"}; !reflect.DeepEqual(server.asked, want) {
		t.Errorf("asked %v, want %v", server.asked, want)
	}
	follow.BackCatalogue = true
	got, _ = following.FollowCandidates(t.Context(), follow)
	if len(got) != 2 {
		t.Errorf("back catalogue = %+v, want fresh and old without the unreleased one", got)
	}
	if limit := following.FollowLimit(follow); limit != maxPerBackCatalogue {
		t.Errorf("limit = %d", limit)
	}
}

type performerServer struct{ *fakeServer }

func (performerServer) FindPerformer(_ context.Context, id string) (stashbox.Performer, error) {
	return stashbox.Performer{
		ID: id, Name: "Ada Vale", Gender: "FEMALE", Height: 170,
		URLs: []stashbox.Link{{URL: "https://www.wikidata.org/wiki/Q42", Type: "WIKIDATA"}},
	}, nil
}

type peopleRecorder struct {
	identity  library.PersonIdentity
	extras    library.PersonExtras
	biography string
	refreshed []int64
}

func (r *peopleRecorder) DueForDetails(context.Context, string, string, time.Time, int) ([]int64, error) {
	return []int64{7}, nil
}

func (r *peopleRecorder) ExternalIDFor(_ context.Context, _ int64, provider string) (string, bool, error) {
	return "p-1", provider == "stashdb:performer", nil
}

func (r *peopleRecorder) BackfillDetails(_ context.Context, _ int64, identity library.PersonIdentity) error {
	r.identity = identity
	return nil
}

func (r *peopleRecorder) SetExtras(_ context.Context, _ int64, extras library.PersonExtras) error {
	r.extras = extras
	return nil
}

func (r *peopleRecorder) SetBiography(_ context.Context, _ int64, biography string) error {
	r.biography = biography
	return nil
}

func (r *peopleRecorder) RecordRefresh(_ context.Context, personID, _ int64) error {
	r.refreshed = append(r.refreshed, personID)
	return nil
}

type fakeWikidata struct{}

func (fakeWikidata) Biography(_ context.Context, qid string) (wikidata.Biography, error) {
	return wikidata.Biography{Extract: "Wikipedia's lead for " + qid}, nil
}

func TestThePerformersSweepFillsThePageAndTakesTheBiographyFromTheLinkedWikidataItem(t *testing.T) {
	people := &peopleRecorder{}
	sweep := Performers{
		Service:  Service{Servers: map[string]Server{ProviderStashDB: performerServer{&fakeServer{}}}},
		People:   people,
		Wikidata: fakeWikidata{},
		Clock:    clock.NewFakeAtEpoch(),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := sweep.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if people.identity.Name != "Ada Vale" || people.identity.Gender != "Female" || len(people.extras.Facts) != 1 || people.extras.Facts[0].Value != "170 cm" {
		t.Errorf("identity = %+v, extras = %+v", people.identity, people.extras)
	}
	if people.biography != "Wikipedia's lead for Q42" {
		t.Errorf("biography = %q", people.biography)
	}
	if !reflect.DeepEqual(people.refreshed, []int64{7}) {
		t.Errorf("refreshed = %v", people.refreshed)
	}
}

type fakePeopleStore struct {
	resolved    []library.PersonLink
	subscribed  []library.SubscriptionRequest
	backfilled  []library.PersonIdentity
	extrasKept  []library.PersonExtras
	heldPersons map[string]int64
}

func (f *fakePeopleStore) FindByExternalID(_ context.Context, provider, id string) (int64, bool, error) {
	person, held := f.heldPersons[provider+"|"+id]
	return person, held, nil
}

func (f *fakePeopleStore) ResolvePerson(_ context.Context, _ time.Time, link library.PersonLink) (int64, error) {
	f.resolved = append(f.resolved, link)
	return 42, nil
}

func (f *fakePeopleStore) BackfillDetails(_ context.Context, _ int64, identity library.PersonIdentity) error {
	f.backfilled = append(f.backfilled, identity)
	return nil
}

func (f *fakePeopleStore) SetExtras(_ context.Context, _ int64, extras library.PersonExtras) error {
	f.extrasKept = append(f.extrasKept, extras)
	return nil
}

func (f *fakePeopleStore) Subscribe(_ context.Context, _ time.Time, _, _ int64, request library.SubscriptionRequest) (int64, error) {
	f.subscribed = append(f.subscribed, request)
	return 7, nil
}

type searchingServer struct{ performerServer }

func (searchingServer) SearchPerformers(context.Context, string, int) ([]stashbox.Performer, error) {
	return []stashbox.Performer{{ID: "p-1", Name: "Ada Vale", Country: "CZ", BirthDate: "1990-04-03", SceneCount: 12}, {ID: "p-2", Name: "Ada Old", MergedIntoID: "p-1"}}, nil
}

func TestAddingAPerformerMakesThePersonWithTheirDetailsAndFollowsThemWhenMonitored(t *testing.T) {
	people := &fakePeopleStore{heldPersons: map[string]int64{"stashdb:performer|p-1": 9}}
	service := Service{Servers: map[string]Server{ProviderStashDB: searchingServer{performerServer{&fakeServer{}}}}, Sources: []string{ProviderStashDB}}
	adds := PerformerAdds{People: people, Now: func() time.Time { return time.Unix(100, 0) }}
	found, err := adds.Lookup(t.Context(), service, media.AddLookup{CategoryID: 1, Kind: KindPerformer, Query: "ada", Limit: 5})
	if err != nil || len(found) != 1 {
		t.Fatalf("found = %+v, %v; want the one performer a merged duplicate does not add", found, err)
	}
	if got := found[0]; got.Provider != "stashdb:performer" || got.Title != "Ada Vale" || got.Subtitle != "CZ - 12 scenes" || got.Year != 1990 || got.LibraryID != 9 {
		t.Errorf("candidate = %+v", got)
	}
	result, err := adds.Add(t.Context(), service, 1, media.AddRequest{Kind: KindPerformer, Provider: "stashdb:performer", ExternalID: "p-1", Monitored: true, UserID: 3})
	if err != nil || result.PersonID != 42 || result.Kind != KindPerformer {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if len(people.resolved) != 1 || people.resolved[0].Provider != "stashdb:performer" || people.resolved[0].ExternalID != "p-1" || len(people.extrasKept) != 1 {
		t.Errorf("resolved = %+v, extras = %+v", people.resolved, people.extrasKept)
	}
	if len(people.subscribed) != 1 || !people.subscribed[0].MonitorBackCatalogue || people.subscribed[0].CategoryID != 1 || people.subscribed[0].Roles[0] != RolePerformer {
		t.Errorf("subscribed = %+v", people.subscribed)
	}
	if _, err := adds.Add(t.Context(), service, 1, media.AddRequest{Kind: KindPerformer, Provider: "tmdb", ExternalID: "1"}); !errors.Is(err, media.ErrInvalidAdd) {
		t.Errorf("a provider that is no performer's: err = %v", err)
	}
}

type fakeMovies struct{ films []theporndb.Movie }

func (fakeMovies) Configured() bool { return true }

func (f fakeMovies) SearchMovies(context.Context, string, int) ([]theporndb.Movie, error) {
	return f.films, nil
}

func (f fakeMovies) Movie(_ context.Context, id string) (theporndb.Movie, error) {
	for _, film := range f.films {
		if film.ID == id {
			return film, nil
		}
	}
	return theporndb.Movie{}, theporndb.ErrNotFound
}

func TestAFilmThePornDBHoldsIsFoundByNameAndThenByItsId(t *testing.T) {
	film := theporndb.Movie{ID: "m-1", Title: "Big Film Vol. 3", Date: "2017-03-13", Duration: 7980,
		Site: &theporndb.Site{UUID: "s-1", Name: "Night Label", Network: &struct {
			UUID string `json:"uuid"`
			Name string `json:"name"`
		}{UUID: "n-1", Name: "Night Network"}},
		Performers: []theporndb.Performer{{ID: "site-p", Name: "Ada V.", Parent: &struct {
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Image string          `json:"image"`
			Extra theporndb.Extra `json:"extra"`
		}{ID: "p-1", Name: "Ada Vale"}}}}
	service := Service{
		Servers: map[string]Server{ProviderThePornDB: &fakeServer{}}, Movies: fakeMovies{films: []theporndb.Movie{film}},
		Sources: []string{ProviderThePornDB},
	}
	scene, err := service.FetchFor(t.Context(), 0, IDs{}, Hint{Title: "Big Film Vol 3", Studio: "Night Label"})
	if err != nil {
		t.Fatal(err)
	}
	if scene.IDs.ThePornDB != "m-1" || scene.Studio != "Night Label" || scene.Network != "Night Network" || scene.DurationSec != 7980 || scene.Year != 2017 {
		t.Errorf("scene = %+v", scene)
	}
	if scene.Credits[0].ExternalID != "p-1" || scene.Credits[0].Name != "Ada Vale" {
		t.Errorf("credits = %+v, want the canonical performer", scene.Credits)
	}
	again, err := service.FetchFor(t.Context(), 0, IDs{ThePornDB: "m-1"}, Hint{})
	if err != nil || again.Title != "Big Film Vol. 3" {
		t.Errorf("by id: %+v, %v", again, err)
	}
	found, err := service.Search(t.Context(), 0, Query{Title: "big film"}, 5)
	if err != nil || len(found) != 1 || found[0].IDs.ThePornDB != "m-1" {
		t.Errorf("search = %+v, %v", found, err)
	}
}

func TestSearchAsksEveryProviderSoAFilmOnlyThePornDBHoldsIsStillFound(t *testing.T) {
	unrelated := stashScene()
	unrelated.Title = "Something Else Entirely"
	film := theporndb.Movie{ID: "m-9", Title: "Big Film Vol. 3", Date: "2017-03-13"}
	service := Service{
		Servers: map[string]Server{ProviderStashDB: &fakeServer{scenes: []stashbox.Scene{unrelated}}, ProviderThePornDB: &fakeServer{}},
		Movies:  fakeMovies{films: []theporndb.Movie{film}}, Sources: []string{ProviderStashDB, ProviderThePornDB},
	}
	found, err := service.Search(t.Context(), 0, Query{Title: "big film vol 3"}, 10)
	if err != nil || len(found) != 2 {
		t.Fatalf("found = %+v, %v; want StashDB's unrelated hit and the film", found, err)
	}
	pick, ok := pickCandidate(found, parseadult.Info{Title: "Big Film Vol. 3", Year: 2017})
	if !ok || pick.IDs.ThePornDB != "m-9" {
		t.Errorf("pick = %+v, %v; want the film", pick, ok)
	}
}
