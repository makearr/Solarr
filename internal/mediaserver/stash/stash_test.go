package stash_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/mediaserver/mediaservertest"
	"github.com/makearr/Solarr/internal/mediaserver/stash"
)

func TestClient_TestAndLibraries(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, r.Method+" "+r.URL.Path+" "+r.Header.Get("ApiKey")+" "+string(body))
		if string(body) == `{"query":"query { version { version } }"}` {
			_, _ = w.Write([]byte(`{"data":{"version":{"version":"v0.27.2"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"configuration":{"general":{"stashes":[{"path":"/media/clips"}]}}}}`))
	}))
	defer server.Close()
	client := stash.New(mediaservertest.Direct(), server.URL, "k")
	info, err := client.Test(t.Context())
	if err != nil || info != (mediaserver.Info{Name: "Stash", Version: "v0.27.2", Admin: true}) {
		t.Errorf("info = %+v, err %v", info, err)
	}
	libraries, _ := client.Libraries(t.Context())
	if want := []mediaserver.Library{{ID: "stash", Name: "Stash", Type: "adult", Paths: []string{"/media/clips"}}}; !reflect.DeepEqual(libraries, want) {
		t.Errorf("libraries = %+v", libraries)
	}
	if want := `POST /graphql k {"query":"query { version { version } }"}`; bodies[0] != want {
		t.Errorf("request = %q, want %q", bodies[0], want)
	}
}

func TestClient_LoginSendsTheSessionCookie(t *testing.T) {
	var cookie, form string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			raw, _ := io.ReadAll(r.Body)
			form = string(raw)
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc"})
			return
		}
		cookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(`{"data":{"version":{"version":"v1"}}}`))
	}))
	defer server.Close()
	if _, err := stash.NewLogin(mediaservertest.Direct(), server.URL, "home", "pw").Test(t.Context()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if cookie != "session=abc" || form != "password=pw&returnURL=%2F&username=home" {
		t.Errorf("cookie %q, form %q", cookie, form)
	}
}

func TestClient_ItemByProviderIDAsksTheBoxEndpointAndMapsTheScene(t *testing.T) {
	var asked string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		asked = string(body)
		_, _ = io.WriteString(w, `{"data":{"findScenes":{"scenes":[{"id":"9","title":"Invented Scene","details":"Words.","date":"2021-05-04","code":"X-1","director":"Pat Dir","studio":{"Name":"Invented Studio"},"performers":[{"Name":"Alex Example"}],"tags":[{"Name":"Tag One"}],"stash_ids":[{"endpoint":"https://stashdb.org/graphql","stash_id":"abc"}],"files":[{"path":"/data/a.mp4","duration":61.5}]}]}}}`)
	}))
	defer server.Close()
	client := stash.New(mediaservertest.Direct(), server.URL, "k")
	got, found, err := client.ItemByProviderID(t.Context(), "adult", "stashdb", "abc")
	want := mediaserver.Item{
		ID: "9", MediaType: "adult", Title: "Invented Scene", Overview: "Words.", Year: 2021, Date: "2021-05-04", Tags: []string{"Tag One"},
		Organisations: []string{"Invented Studio"}, RuntimeMS: 61500, Path: "/data/a.mp4",
		People: []mediaserver.Credit{{Name: "Alex Example", Kind: "actor"}, {Name: "Pat Dir", Kind: "director"}},
		IDs:    map[string]string{"code": "X-1", "stashdb": "abc"},
	}
	if err != nil || !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("item = %+v, %v, %v", got, found, err)
	}
	if !strings.Contains(asked, `https://stashdb.org/graphql`) || !strings.Contains(asked, `modifier: EQUALS`) {
		t.Errorf("query = %s", asked)
	}
	if _, found, _ := client.ItemByProviderID(t.Context(), "adult", "imdb", "tt1"); found {
		t.Error("a provider that is no stash-box found a scene")
	}
}
