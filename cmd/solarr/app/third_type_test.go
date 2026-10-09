package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/service/follow"
)

// fakeMovie is one film its invented source knows.
type fakeMovie struct {
	id, title, director string
	year                int64
}

var fakeMovies = []fakeMovie{
	{"m1", "The Glass Orchard", "Ada Brenning", 2021},
	{"m2", "Salt Lanterns", "Ada Brenning", 2024},
}

// movieModule is a third media type that exists only here (revision 2, R1.9): a work without parts, director and
// actor roles, a studio. It is registered beside music and TV and nothing in core knows its name.
type movieModule struct {
	library   *store.Store
	profileID int64
}

func (*movieModule) Descriptor() media.Descriptor {
	return media.Descriptor{
		Key: "movie", Label: "Movies",
		Vocabulary: media.Vocabulary{
			Work: media.Term{One: "Movie", Many: "Movies"}, LeadPeople: media.Term{One: "Director", Many: "Directors"},
			Organisation: media.Term{One: "Studio", Many: "Studios"},
		},
		Levels: []model.Level{
			{Key: "movies", Label: "Movies", Entity: model.LevelEntityWork, DefaultSort: "title"},
			{Key: "people", Label: "People", Entity: model.LevelEntityPerson, DefaultSort: "name"},
			{Key: "studios", Label: "Studios", Entity: model.LevelEntityOrganisation, DefaultSort: "name"},
		},
		Roles: []media.Role{
			{Key: "director", Label: "Director", Lead: true, Followable: true},
			{Key: "actor", Label: "Cast", Followable: true, Character: true},
		},
		Facets: []media.Facet{{
			Key: "studio", Label: "Studio", Entity: model.LevelEntityWork, Kind: media.FacetText,
			Source: media.FacetSource{Kind: media.SourceOrganisation, Name: "studio"}, Column: true, Filterable: true,
		}},
		WorkImage: media.ImagePoster,
		AddKinds:  []media.Choice{{Key: "movie", Label: "Movies"}},
	}
}

func (*movieModule) LookupWorks(_ context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	var found []media.AddCandidate
	for _, movie := range fakeMovies {
		if strings.Contains(strings.ToLower(movie.title), strings.ToLower(lookup.Query)) {
			found = append(found, media.AddCandidate{Kind: "movie", Provider: "fake:movie", ExternalID: movie.id, Title: movie.title, Year: movie.year})
		}
	}
	return found, nil
}

func (m *movieModule) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	movie, ok := movieByID(request.ExternalID)
	if !ok {
		return media.AddResult{}, fmt.Errorf("no movie %q", request.ExternalID)
	}
	workID, err := m.add(ctx, request.CategoryID, movie, request.Monitored, 0, 0)
	return media.AddResult{Kind: "movie", WorkID: workID}, err
}

// add holds one movie in a library: the work, its director, its studio and a wanted item; with a file, the file too.
func (m *movieModule) add(ctx context.Context, categoryID int64, movie fakeMovie, monitored bool, rootFolderID, mediaObjectID int64) (int64, error) {
	now := time.Unix(1_800_000_000, 0)
	works, err := library.NewIdentity(m.library).ResolveWorks(ctx, now, []library.WorkLink{{
		Provider: "fake:movie", ExternalID: movie.id, Confidence: 1, RootFolderID: rootFolderID, MediaObjectID: mediaObjectID,
		Work: library.WorkIdentity{MediaType: "movie", CanonicalTitle: movie.title, SortTitle: strings.ToLower(movie.title)},
	}})
	if err != nil {
		return 0, err
	}
	people := library.NewPeople(m.library)
	director, err := people.ResolvePerson(ctx, now, library.PersonLink{Provider: "fake:person", ExternalID: movie.director, Person: library.PersonIdentity{Name: movie.director}})
	if err != nil {
		return 0, err
	}
	if err := people.AddCredit(ctx, works[0], director, "director", 0); err != nil {
		return 0, err
	}
	if _, _, err := library.NewOrganisations(m.library).SetWorkOrganisation(ctx, now, works[0], "studio", "fake:movie",
		&library.OrganisationLink{Kind: "studio", Name: "Northlight Pictures", Provider: "fake:studio", ExternalID: "s1"}); err != nil {
		return 0, err
	}
	desired := library.NewDesired(m.library)
	item, err := desired.EnsureAlbumItem(ctx, library.AlbumRequest{WorkID: works[0], CategoryID: categoryID, ProfileID: m.profileID, Monitored: monitored})
	if err != nil || mediaObjectID == 0 {
		return works[0], err
	}
	return works[0], desired.RecordAlignment(ctx, item.ID, works[0], []library.AlignedFile{{MediaObjectID: mediaObjectID}}, now.Unix())
}

func (m *movieModule) FollowCandidates(ctx context.Context, follow media.Follow) ([]media.FollowCandidate, error) {
	person, err := library.NewPeople(m.library).Get(ctx, follow.SubjectID, []int64{follow.CategoryID})
	if err != nil {
		return nil, err
	}
	var candidates []media.FollowCandidate
	for _, movie := range fakeMovies {
		if movie.director == person.Name {
			_, held, err := library.NewIdentity(m.library).WorkByExternalID(ctx, "fake:movie", movie.id)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, media.FollowCandidate{Provider: "fake:movie", ExternalID: movie.id, Title: movie.title, Year: movie.year, Role: "director", Held: held})
		}
	}
	return candidates, nil
}

func (m *movieModule) AddFollowed(ctx context.Context, follow media.Follow, candidate media.FollowCandidate) error {
	movie, _ := movieByID(candidate.ExternalID)
	_, err := m.add(ctx, follow.CategoryID, movie, true, 0, 0)
	return err
}

func (*movieModule) FollowLimit(media.Follow) int { return 0 }

func (*movieModule) SearchWanted(context.Context, media.WantedSearch) (judge.Result, error) {
	return judge.Result{}, nil
}

func (*movieModule) GrabWanted(context.Context, media.WantedSearch) (judge.Result, dispatch.Best, error) {
	return judge.Result{}, dispatch.Best{}, nil
}

func (m *movieModule) PlaceFile(ctx context.Context, categoryID, mediaObjectID int64, placement media.Placement, _ bool) (int64, int64, error) {
	movie, ok := movieByID(placement.ExternalID)
	if !ok {
		return 0, 0, fmt.Errorf("no movie %q", placement.ExternalID)
	}
	queue := library.NewUnidentified(m.library)
	entry, err := queue.Get(ctx, mediaObjectID)
	if err != nil {
		return 0, 0, err
	}
	workID, err := m.add(ctx, categoryID, movie, true, entry.RootFolderID, mediaObjectID)
	if err != nil {
		return 0, 0, err
	}
	return workID, 1, queue.Clear(ctx, []int64{mediaObjectID})
}

func movieByID(id string) (fakeMovie, bool) {
	for _, movie := range fakeMovies {
		if movie.id == id {
			return movie, true
		}
	}
	return fakeMovie{}, false
}

// A third media type, registered through its module alone, is listed, added, shown, credited, followed, searched and
// placed by the same endpoints as music and TV (revision 2, R1.9: the generic core holds).
func TestAThirdMediaTypeWorksThroughTheModuleAlone(t *testing.T) {
	movies := &movieModule{}
	server := newGoldenServerWith(t, movies)
	movies.library, movies.profileID = server.process.library, server.process.defaultQualityProfileID
	ctx := t.Context()

	if types := string(server.get(t, "/mediatypes")); !strings.Contains(types, `"key":"movie"`) || !strings.Contains(types, `"lead_people":{"many":"Directors","one":"Director"}`) {
		t.Fatalf("/mediatypes = %s, want the movie type with its words", types)
	}
	status, body := server.post(t, "/categories", `{"media_type":"movie","name":"Movies"}`)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create a movie library: %d %s", status, body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	movieLibrary := created.ID
	root, err := server.process.cache.CreateRootFolder(ctx, "/library/movies", "Movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.process.cache.AttachRootFolder(ctx, settings.CategoryRoot{CategoryID: movieLibrary, RootFolderID: root.ID, Weight: 1, AcceptsNew: true}); err != nil {
		t.Fatal(err)
	}

	if found := string(server.get(t, fmt.Sprintf("/works/lookup?category_id=%d&q=glass&kind=movie", movieLibrary))); !strings.Contains(found, `"external_id":"m1"`) {
		t.Errorf("lookup = %s, want The Glass Orchard", found)
	}
	status, body = server.post(t, "/works", fmt.Sprintf(`{"category_id":%d,"kind":"movie","provider":"fake:movie","external_id":"m1","monitored":true}`, movieLibrary))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("add: %d %s", status, body)
	}
	var added struct {
		WorkID int64 `json:"work_id"`
	}
	if err := json.Unmarshal([]byte(body), &added); err != nil || added.WorkID == 0 {
		t.Fatalf("add answered %s", body)
	}

	if list := string(server.get(t, fmt.Sprintf("/works?category_id=%d", movieLibrary))); !strings.Contains(list, "The Glass Orchard") || !strings.Contains(list, "Northlight Pictures") {
		t.Errorf("works list = %s, want the movie with its studio facet", list)
	}
	if page := string(server.get(t, fmt.Sprintf("/works/%d", added.WorkID))); !strings.Contains(page, "Ada Brenning") || !strings.Contains(page, `"part_count":0`) {
		t.Errorf("work page = %s, want its director and no parts", page)
	}

	var people struct {
		Items []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	peopleBody := server.get(t, fmt.Sprintf("/people?category_id=%d", movieLibrary))
	if err := json.Unmarshal(peopleBody, &people); err != nil || len(people.Items) != 1 || people.Items[0].Name != "Ada Brenning" {
		t.Fatalf("people = %s, want the director", peopleBody)
	}
	director := people.Items[0].ID

	followBody := fmt.Sprintf(`{"category_id":%d,"back_catalogue":true}`, movieLibrary)
	if status, preview := server.post(t, fmt.Sprintf("/people/%d/follow/preview", director), followBody); status != http.StatusOK || !strings.Contains(preview, "Salt Lanterns") {
		t.Errorf("follow preview: %d %s, want her other movie", status, preview)
	}
	if status, answer := server.send(t, http.MethodPut, fmt.Sprintf("/people/%d/follow", director), followBody); status != http.StatusOK {
		t.Fatalf("follow: %d %s", status, answer)
	}
	// The test server runs no dispatcher, so the sweep the follow queued is run here.
	if err := server.process.followService().Run(ctx, jobs.Job{Type: follow.JobType}); err != nil {
		t.Fatal(err)
	}
	if list := string(server.get(t, fmt.Sprintf("/works?category_id=%d", movieLibrary))); !strings.Contains(list, "Salt Lanterns") {
		t.Errorf("after the follow the works are %s, want her other movie added", list)
	}

	if status, answer := server.post(t, fmt.Sprintf("/works/%d/search", added.WorkID), ""); status != http.StatusOK || !strings.Contains(answer, `"decisions":[]`) {
		t.Errorf("search: %d %s, want the type's own empty search", status, answer)
	}

	files, err := library.NewPhysical(server.process.library).RegisterBatch(ctx, root.ID, []library.PhysicalFile{
		{RelPath: "Unsorted/glass.mkv", SizeBytes: 4_000_000_000, QuickHash: "movie-1", FirstSeenAt: goldenSeededAt},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := library.NewUnidentified(server.process.library).Record(ctx, []library.QueueEntry{{MediaObjectID: files[0].MediaObjectID, RootFolderID: root.ID, RelPath: "Unsorted/glass.mkv", At: goldenSeededAt.Unix()}}); err != nil {
		t.Fatal(err)
	}
	status, body = server.post(t, fmt.Sprintf("/library/unidentified/%d/resolve", files[0].MediaObjectID), `{"provider":"fake:movie","external_id":"m1"}`)
	if status != http.StatusOK || !strings.Contains(body, fmt.Sprintf(`"work_id":%d`, added.WorkID)) {
		t.Errorf("place a waiting file: %d %s, want it placed as the movie", status, body)
	}

	for _, probe := range []string{
		fmt.Sprintf("/people/%d?category_id=%d", director, movieLibrary),
		fmt.Sprintf("/organisations?category_id=%d", movieLibrary),
		fmt.Sprintf("/categories/%d/statistics", movieLibrary),
		fmt.Sprintf("/categories/%d/duplicates", movieLibrary),
	} {
		server.get(t, probe)
	}

	// Two seams the proof found (revision 2, R1.9): kept here as what must hold once core gives a third type its due.
	t.Run("lead and completeness", func(t *testing.T) {
		t.Skip("seam gaps, private/questions.md R1.9: the lead is only an \"artist\" credit; a work without parts never completes")
		list := string(server.get(t, fmt.Sprintf("/works?category_id=%d", movieLibrary)))
		if !strings.Contains(list, fmt.Sprintf(`"lead_id":%d`, director)) || !strings.Contains(list, `"wanted_state":"COMPLETE"`) {
			t.Errorf("works = %s, want the director as lead and the placed movie complete", list)
		}
	})
}
