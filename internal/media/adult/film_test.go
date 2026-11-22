package adult

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/identify/parseadult"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

func TestFilmTitle_ReadsANumberedSceneOnly(t *testing.T) {
	for title, want := range map[string]string{
		"Long Summer - Scene 1": "Long Summer", "Long Summer, Scene 12": "Long Summer", "Long Summer (Scene 3)": "Long Summer",
		"Long Summer: scene 2": "Long Summer", "Two Timing In The Sun. Sc1": "Two Timing In The Sun", "Two Timing In The Sun Sc4": "Two Timing In The Sun",
		"Scene 1": "", "The Scene": "", "Long Summer": "",
	} {
		got, numbered := filmTitle(title)
		if got != want || numbered != (want != "") {
			t.Errorf("filmTitle(%q) = %q, %v; want %q", title, got, numbered, want)
		}
	}
}

func filmCandidates() []Candidate {
	return []Candidate{
		{IDs: IDs{StashDB: "s1"}, Title: "Long Summer - Scene 1", Studio: "Night Studio", Year: 2019, Date: "2019-05-01"},
		{IDs: IDs{ThePornDB: "s2"}, Title: "Long Summer - Scene 2", Studio: "Night Studio", Year: 2019, Date: "2019-05-01"},
		{IDs: IDs{StashDB: "x1"}, Title: "Long Summer Nights - Scene 1", Studio: "Night Studio", Year: 2019},
	}
}

func TestFilmOf_FindsTheScenesOfTheFilesTitle(t *testing.T) {
	film, found := filmOf(filmCandidates(), parseadult.Info{Title: "Long Summer", Year: 2019})
	if !found || film.Title != "Long Summer" || len(film.Scenes) != 2 {
		t.Fatalf("film = %+v, %v; want Long Summer of two scenes", film, found)
	}
	picked := film.identification()
	if want := "stashdb=s1,theporndb=s2"; picked.ExternalID.Provider != ProviderFilm || picked.ExternalID.ExternalID != want {
		t.Errorf("candidate = %+v, want the film's scenes %q", picked.ExternalID, want)
	}
	if got := picked.Evidence[0].Detail; got != "Long Summer: the whole film of 2 scenes (Night Studio)" {
		t.Errorf("detail = %q", got)
	}
	if members, ok := filmMembers(picked.ExternalID.ExternalID); !ok || !reflect.DeepEqual(members, []IDs{{StashDB: "s1"}, {ThePornDB: "s2"}}) {
		t.Errorf("members = %+v, %v", members, ok)
	}
}

// Found live: scene records carry upload years, and two providers list one film under two studios, one of them unnamed.
func TestFilmOf_ToleratesUploadYearsAndTakesTheLargestListing(t *testing.T) {
	candidates := []Candidate{
		{IDs: IDs{ThePornDB: "a"}, Title: "Long Summer. Sc1", Year: 2015}, {IDs: IDs{ThePornDB: "b"}, Title: "Long Summer Sc2", Year: 2015},
		{IDs: IDs{ThePornDB: "c"}, Title: "Long Summer Sc3", Year: 2015},
		{IDs: IDs{StashDB: "d"}, Title: "Long Summer Scene 2", Studio: "Night Studio", Year: 2015}, {IDs: IDs{StashDB: "e"}, Title: "Long Summer Scene 3", Studio: "Night Studio", Year: 2015},
	}
	film, found := filmOf(candidates, parseadult.Info{Title: "Long Summer", Year: 2013})
	if !found || len(film.Scenes) != 3 {
		t.Errorf("film = %+v, %v; want the three scenes", film, found)
	}
}

func TestFilmOf_RefusesWhatIsNotOneFilm(t *testing.T) {
	two := filmCandidates()[:2]
	other := slicesWithStudio(two, "Day Studio")
	for name, test := range map[string]struct {
		candidates []Candidate
		info       parseadult.Info
	}{
		"a name that numbers a scene": {two, parseadult.Info{Title: "Long Summer - Scene 1"}},
		"another year":                {two, parseadult.Info{Title: "Long Summer", Year: 2001}},
		"one scene alone":             {two[:1], parseadult.Info{Title: "Long Summer"}},
		"two films of one title":      {append(two[:2:2], other...), parseadult.Info{Title: "Long Summer"}},
	} {
		if film, found := filmOf(test.candidates, test.info); found {
			t.Errorf("%s: found %+v", name, film)
		}
	}
}

func slicesWithStudio(candidates []Candidate, studio string) []Candidate {
	out := make([]Candidate, len(candidates))
	for index, candidate := range candidates {
		candidate.Studio = studio
		out[index] = candidate
	}
	return out
}

func TestJoinScenes_TheFilmHoldsEveryoneOnceAndTheEarliestDay(t *testing.T) {
	ada := Credit{Name: "Ada", Provider: "stashdb", ExternalID: "p1", Role: RolePerformer}
	bea := Credit{Name: "Bea", Provider: "stashdb", ExternalID: "p2", Role: RolePerformer, Order: 1}
	film := joinScenes("Long Summer", []Scene{
		{IDs: IDs{StashDB: "s2"}, Title: "Long Summer - Scene 2", Date: "2019-06-01", DurationSec: 900, Studio: "Night", Credits: []Credit{ada, bea}},
		{IDs: IDs{StashDB: "s1"}, Title: "Long Summer - Scene 1", Date: "2019-05-01", DurationSec: 600, Studio: "Night", Credits: []Credit{ada}},
	})
	if film.Title != "Long Summer" || film.Date != "2019-05-01" || film.Year != 2019 || film.DurationSec != 1500 {
		t.Errorf("film = %+v", film)
	}
	if got := film.Performers(); !reflect.DeepEqual(got, []string{"Ada", "Bea"}) {
		t.Errorf("performers = %v", got)
	}
	write := ToWrite(film, time.Unix(1_700_000_000, 0))
	want := []library.ExternalIDWrite{{Provider: ProviderStashDB, ExternalID: "s2"}, {Provider: ProviderStashDB, ExternalID: "s1"}, {Provider: ProviderFilm, ExternalID: "s2"}}
	if !reflect.DeepEqual(write.IDs, want) {
		t.Errorf("ids = %+v, want %+v", write.IDs, want)
	}
}

func newFilmService(t *testing.T, server Server) Service {
	t.Helper()
	directory := t.TempDir()
	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{Name: "library", Path: filepath.Join(directory, "library.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, directory, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return Service{
		Servers: map[string]Server{ProviderStashDB: server}, Sources: []string{ProviderStashDB}, Clock: fake,
		Series: library.NewSeries(opened), Reader: library.NewSeriesReader(opened), Desired: library.NewDesired(opened),
	}
}

// The film is one work under all its scenes' ids, a second file of it lands on the same work, and a refresh leaves it
// alone: a provider asked by a scene's id would rename it "Long Summer - Scene 1".
func TestAddFilm_OneWorkUnderEveryScenesIDWhichNoRefreshRenames(t *testing.T) {
	first := stashbox.Scene{ID: "s1", Title: "Long Summer - Scene 1", ReleaseDate: "2019-05-01", Duration: 600}
	second := stashbox.Scene{ID: "s2", Title: "Long Summer - Scene 2", ReleaseDate: "2019-05-01", Duration: 900}
	service := newFilmService(t, &fakeServer{scenes: []stashbox.Scene{first, second}})
	request := AddFilmRequest{Members: []IDs{{StashDB: "s1"}, {StashDB: "s2"}}, CategoryID: 3, ProfileID: 1}

	added, err := service.AddFilm(t.Context(), request)
	if err != nil {
		t.Fatalf("AddFilm: %v", err)
	}
	if !added.Created || added.Scene.Title != "Long Summer" {
		t.Fatalf("added = %+v", added)
	}
	for _, id := range []IDs{{StashDB: "s1"}, {StashDB: "s2"}} {
		if work, held := service.WorkByIDs(t.Context(), id); !held || work != added.WorkID {
			t.Errorf("scene %v: work %d, %v; want the film %d", id, work, held, added.WorkID)
		}
	}
	again, err := service.AddFilm(t.Context(), request)
	if err != nil || again.WorkID != added.WorkID || again.Created {
		t.Errorf("second add = %+v, %v; want the same work", again, err)
	}
	if _, err := service.Refresh(t.Context(), 3, added.WorkID); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	basics, err := service.Reader.Basics(t.Context(), added.WorkID)
	if err != nil || basics.Title != "Long Summer" {
		t.Errorf("after a refresh the title is %q, %v", basics.Title, err)
	}
}

func TestAddFilm_RefusesAScenePlacedOnItsOwn(t *testing.T) {
	first := stashbox.Scene{ID: "s1", Title: "Long Summer - Scene 1", ReleaseDate: "2019-05-01"}
	second := stashbox.Scene{ID: "s2", Title: "Long Summer - Scene 2", ReleaseDate: "2019-05-01"}
	service := newFilmService(t, &fakeServer{scenes: []stashbox.Scene{first, second}})
	if _, err := service.Add(context.Background(), AddRequest{IDs: IDs{StashDB: "s2"}, CategoryID: 3, ProfileID: 1}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	_, err := service.AddFilm(t.Context(), AddFilmRequest{Members: []IDs{{StashDB: "s1"}, {StashDB: "s2"}}, CategoryID: 3, ProfileID: 1})
	if !errors.Is(err, media.ErrInvalidAdd) || !strings.Contains(err.Error(), "on its own") {
		t.Errorf("err = %v, want a refusal naming why", err)
	}
}

// downServer answers every scene question with a transport error, as a provider in an outage does.
type downServer struct{ *fakeServer }

var errProviderDown = errors.New("provider down")

func (downServer) FindScene(context.Context, string) (stashbox.Scene, error) {
	return stashbox.Scene{}, errProviderDown
}

func (downServer) SearchScenes(context.Context, string, int) ([]stashbox.Scene, error) {
	return nil, errProviderDown
}

func (downServer) ScenesOnDate(context.Context, string, string, int) ([]stashbox.Scene, error) {
	return nil, errProviderDown
}

// A refresh while the provider that holds the scene's stored id is down must not store the shorter merge of the others (META-14).
func TestRefresh_WhileTheProviderThatSuppliedTheSceneIsDownStoresNothing(t *testing.T) {
	service := newFilmService(t, &fakeServer{scenes: []stashbox.Scene{stashScene()}})
	added, err := service.Add(t.Context(), AddRequest{IDs: IDs{StashDB: "sb-1"}, CategoryID: 3, ProfileID: 1})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	other := stashScene()
	other.ID, other.Details = "tp-1", "Shorter."
	service.Servers = map[string]Server{ProviderStashDB: downServer{&fakeServer{}}, ProviderThePornDB: &fakeServer{scenes: []stashbox.Scene{other}}}
	service.Sources = []string{ProviderStashDB, ProviderThePornDB}
	if _, err := service.Refresh(t.Context(), 3, added.WorkID); !errors.Is(err, media.ErrSourceUnavailable) {
		t.Fatalf("Refresh = %v, want ErrSourceUnavailable", err)
	}
}

func TestFetchFor_NamesTheSourcesThatAnsweredAnError(t *testing.T) {
	service := Service{
		Servers: map[string]Server{ProviderStashDB: downServer{&fakeServer{}}, ProviderThePornDB: &fakeServer{scenes: []stashbox.Scene{stashScene()}}},
		Sources: []string{ProviderStashDB, ProviderThePornDB},
	}
	scene, err := service.FetchFor(t.Context(), 0, IDs{}, Hint{Title: "Class Two", Date: "2020-05-14"})
	if err != nil || !slices.Equal(scene.Failed, []string{ProviderStashDB}) {
		t.Errorf("Failed = %v, %v", scene.Failed, err)
	}
}

// A refresh asks a source that finds the scene by its name with the studio and day the library holds (META-14).
func TestRefresh_AsksASourceByNameWithTheStoredStudioAndDay(t *testing.T) {
	service := newFilmService(t, &fakeServer{scenes: []stashbox.Scene{stashScene()}})
	added, err := service.Add(t.Context(), AddRequest{IDs: IDs{StashDB: "sb-1"}, CategoryID: 3, ProfileID: 1})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	byName := &fakeServer{}
	service.Servers = map[string]Server{ProviderStashDB: &fakeServer{scenes: []stashbox.Scene{stashScene()}}, ProviderThePornDB: byName}
	service.Sources = []string{ProviderStashDB, ProviderThePornDB}
	if _, err := service.Refresh(t.Context(), 3, added.WorkID); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	want := []string{"on:2020-05-14:Class Two", "search:Night Studio Class Two", "search:Class Two"}
	if !slices.Equal(byName.asked, want) {
		t.Errorf("asked = %v, want %v", byName.asked, want)
	}
}
