package jellyfin_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
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
		`GET /Library/VirtualFolders MediaBrowser Token="k1"`,
		`GET /Library/VirtualFolders MediaBrowser Token="k1"`,
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
