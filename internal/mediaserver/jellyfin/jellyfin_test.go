package jellyfin_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/mediaserver/jellyfin"
	"github.com/makearr/Solarr/internal/mediaserver/mediaservertest"
)

func TestClient_TestAndLibraries(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/System/Info/Public":
			_, _ = w.Write([]byte(`{"ServerName":"Den","Version":"10.10.3"}`))
		case "/Library/VirtualFolders":
			_, _ = w.Write([]byte(`[{"Name":"Albums","ItemId":"abc","CollectionType":"music","Locations":["/data/music"]}]`))
		}
	}))
	defer server.Close()
	client := jellyfin.New(mediaservertest.Direct(), server.URL, "k1")
	info, err := client.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if want := (mediaserver.Info{Name: "Den", Version: "10.10.3", Admin: true}); info != want {
		t.Errorf("info = %+v, want %+v", info, want)
	}
	libraries, _ := client.Libraries(t.Context())
	want := []mediaserver.Library{{ID: "abc", Name: "Albums", Type: "music", Paths: []string{"/data/music"}}}
	if !reflect.DeepEqual(libraries, want) {
		t.Errorf("libraries = %+v, want %+v", libraries, want)
	}
	wantSeen := []string{
		"GET /System/Info/Public ",
		`GET /Library/VirtualFolders MediaBrowser Client="Solarr", Device="Solarr", DeviceId="solarr", Version="1", Token="k1"`,
		`GET /Library/VirtualFolders MediaBrowser Client="Solarr", Device="Solarr", DeviceId="solarr", Version="1", Token="k1"`,
	}
	if !reflect.DeepEqual(seen, wantSeen) {
		t.Errorf("requests = %q, want %q", seen, wantSeen)
	}
}

func TestClient_TestReportsAKeyThatIsNotAnAdministrators(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Library/VirtualFolders" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"ServerName":"Den","Version":"10.10.3"}`))
	}))
	defer server.Close()
	info, err := jellyfin.New(mediaservertest.Direct(), server.URL, "k1").Test(t.Context())
	if err != nil || info.Admin {
		t.Errorf("info = %+v, err = %v; want a working non-admin key", info, err)
	}
}

func TestClient_ChangedSendsOnePathUpdateBatch(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+string(body))
	}))
	defer server.Close()
	client := jellyfin.New(mediaservertest.Direct(), server.URL, "k1")
	err := client.Changed(t.Context(), []mediaserver.Change{{Path: "/m/a", Kind: "created"}, {Path: "/m/b", Kind: "deleted"}, {Path: "/m/c", Kind: "modified"}})
	if err != nil {
		t.Fatalf("Changed: %v", err)
	}
	want := []string{`POST /Library/Media/Updated {"Updates":[{"Path":"/m/a","UpdateType":"Created"},{"Path":"/m/b","UpdateType":"Deleted"},{"Path":"/m/c","UpdateType":"Modified"}]}`}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("requests = %q, want %q", seen, want)
	}
}

func TestClient_ChangedScansWhenThereAreManyPaths(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = append(seen, r.Method+" "+r.URL.Path) }))
	defer server.Close()
	changes := make([]mediaserver.Change, 51)
	if err := jellyfin.New(mediaservertest.Direct(), server.URL, "k1").Changed(t.Context(), changes); err != nil {
		t.Fatalf("Changed: %v", err)
	}
	if want := []string{"POST /Library/Refresh"}; !reflect.DeepEqual(seen, want) {
		t.Errorf("requests = %q, want %q", seen, want)
	}
}

func TestClient_AnnounceMessagesActiveSessionsOnly(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+string(body))
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[{"Id":"s1","IsActive":true,"SupportsRemoteControl":true},{"Id":"s2","IsActive":false,"SupportsRemoteControl":true},{"Id":"s3","IsActive":true,"SupportsRemoteControl":false}]`))
		}
	}))
	defer server.Close()
	if err := jellyfin.New(mediaservertest.Direct(), server.URL, "k1").Announce(t.Context(), "Now available: Dune"); err != nil {
		t.Fatalf("Announce: %v", err)
	}
	want := []string{"GET /Sessions ", `POST /Sessions/s1/Message {"Header":"Solarr","Text":"Now available: Dune","TimeoutMs":8000}`}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("requests = %q, want %q", seen, want)
	}
}

func TestClient_LoginWithAnEmptyPasswordSendsTheToken(t *testing.T) {
	var body string
	var auth, loginAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users/AuthenticateByName":
			raw, _ := io.ReadAll(r.Body)
			body = string(raw)
			_, _ = w.Write([]byte(`{"AccessToken":"tok"}`))
			loginAuth = r.Header.Get("Authorization")
		case "/Users":
			auth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`[{"Id":"1","Name":"a"}]`))
		}
	}))
	defer server.Close()
	users, err := jellyfin.NewLogin(mediaservertest.Direct(), server.URL, "home", "").Users(t.Context())
	if err != nil || len(users) != 1 {
		t.Fatalf("Users = %+v, %v", users, err)
	}
	if !strings.HasPrefix(loginAuth, "MediaBrowser Client=") || body != `{"Username":"home","Pw":""}` || !strings.Contains(auth, `Token="tok"`) {
		t.Errorf("login body %q, header %q", body, auth)
	}
}

func TestClient_ItemByPathMatchesOnlyAnItemAboveTheFile(t *testing.T) {
	var terms []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		terms = append(terms, r.URL.Query().Get("searchTerm"))
		_, _ = w.Write([]byte(`{"Items":[
{"Id":"x1","Type":"Movie","Name":"Other Film","Path":"/data/films/Other Film (2001)/Other Film.mkv"},
{"Id":"m1","Type":"Movie","Name":"Harbor Lights","OriginalTitle":"Lumieres du Port","Overview":"A ferry pilot.","ProductionYear":1999,
 "PremiereDate":"1999-05-04T00:00:00.0000000Z","Genres":["Drama"],"CommunityRating":7.5,"OfficialRating":"PG","RunTimeTicks":54000000000,
 "ProviderIds":{"Tmdb":"123","Imdb":"tt0000123"},"Path":"/data/films/Harbor Lights (1999)/Harbor Lights (1999).mkv",
 "People":[{"Name":"Ada Quill","Role":"Pilot","Type":"Actor"}],"Studios":[{"Name":"Reef Pictures"}],"ImageTags":{"Primary":"t"}}]}`))
	}))
	defer server.Close()
	client := jellyfin.New(mediaservertest.Direct(), server.URL, "k1")
	item, found, err := client.ItemByPath(t.Context(), "/data/films/Harbor Lights (1999)/Harbor Lights (1999).mkv")
	if err != nil || !found {
		t.Fatalf("ItemByPath = %v, %v", found, err)
	}
	want := mediaserver.Item{
		ID: "m1", MediaType: "movie", Title: "Harbor Lights", OriginalTitle: "Lumieres du Port", Overview: "A ferry pilot.", Year: 1999,
		Date: "1999-05-04", Genres: []string{"Drama"}, Rating: 7.5, AgeRating: "PG", RuntimeMS: 5400000,
		IDs:           map[string]string{"tmdb": "123", "imdb": "tt0000123"},
		People:        []mediaserver.Credit{{Name: "Ada Quill", Role: "Pilot", Kind: "actor"}},
		Organisations: []string{"Reef Pictures"},
		Images:        []mediaserver.Image{{Kind: "primary", URL: server.URL + "/Items/m1/Images/Primary"}},
		Path:          "/data/films/Harbor Lights (1999)/Harbor Lights (1999).mkv",
	}
	if !reflect.DeepEqual(item, want) {
		t.Errorf("item = %+v,\nwant %+v", item, want)
	}
	if terms[0] != "Harbor Lights" {
		t.Errorf("first search term = %q", terms[0])
	}
	if _, found, _ := client.ItemByPath(t.Context(), "/data/films/Unknown (2020)/Unknown (2020).mkv"); found {
		t.Error("an unrelated path matched an item")
	}
}

func TestClient_ItemByProviderIDReadsTheIndexOnceAndTheItemByItsId(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Query().Get("Fields")+"|"+r.URL.Query().Get("Ids"))
		if r.URL.Query().Get("Ids") == "j2" {
			_, _ = io.WriteString(w, `{"Items":[{"Id":"j2","Type":"Series","Name":"Night Harbor","ProviderIds":{"Tmdb":"77"}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"Items":[{"Id":"j1","Type":"Series","ProviderIds":{"Tmdb":"5"}},{"Id":"j2","Type":"Series","ProviderIds":{"Tmdb":"77","Imdb":"tt9"}}]}`)
	}))
	defer server.Close()
	client := jellyfin.New(mediaservertest.Direct(), server.URL, "k1")
	item, found, err := client.ItemByProviderID(t.Context(), "tv", "tmdb", "77")
	if err != nil || !found || item.Title != "Night Harbor" || item.ID != "j2" {
		t.Fatalf("ItemByProviderID = %+v, %v, %v", item, found, err)
	}
	if _, found, _ = client.ItemByProviderID(t.Context(), "tv", "imdb", "tt404"); found {
		t.Error("an unknown id was found")
	}
	if _, found, _ = client.ItemByProviderID(t.Context(), "tv", "imdb", "tt9"); !found {
		t.Error("the second id of an item was not found")
	}
	if len(requests) != 3 {
		t.Errorf("requests = %q, want one index read and two item reads", requests)
	}
}
