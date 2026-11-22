package audiobookshelf_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/mediaserver/audiobookshelf"
	"github.com/makearr/Solarr/internal/mediaserver/mediaservertest"
)

func TestClient_TestLibrariesUsers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" && r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/status":
			_, _ = w.Write([]byte(`{"serverVersion":"2.38.0"}`))
		case "/api/me":
			_, _ = w.Write([]byte(`{"type":"admin"}`))
		case "/api/libraries":
			_, _ = w.Write([]byte(`{"libraries":[{"id":"l1","name":"Spoken","mediaType":"book","folders":[{"fullPath":"/audiobooks"}]}]}`))
		case "/api/users":
			_, _ = w.Write([]byte(`{"users":[{"id":"u1","username":"guest"}]}`))
		}
	}))
	defer server.Close()
	client := audiobookshelf.New(mediaservertest.Direct(), server.URL, "tok")
	info, err := client.Test(t.Context())
	if err != nil || info != (mediaserver.Info{Name: "Audiobookshelf", Version: "2.38.0", Admin: true}) {
		t.Errorf("info = %+v, err %v", info, err)
	}
	libraries, _ := client.Libraries(t.Context())
	if want := []mediaserver.Library{{ID: "l1", Name: "Spoken", Type: "book", Paths: []string{"/audiobooks"}}}; !reflect.DeepEqual(libraries, want) {
		t.Errorf("libraries = %+v", libraries)
	}
	users, _ := client.Users(t.Context())
	if want := []mediaserver.User{{ID: "u1", Name: "guest"}}; !reflect.DeepEqual(users, want) {
		t.Errorf("users = %+v", users)
	}
	if _, err := audiobookshelf.New(mediaservertest.Direct(), server.URL, "bad").Test(t.Context()); !errors.Is(err, mediaserver.ErrRejected) {
		t.Errorf("err = %v, want ErrRejected", err)
	}
}

func TestClient_ChangedScansEachLibraryOnce(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = append(seen, r.Method+" "+r.URL.Path) }))
	defer server.Close()
	changes := []mediaserver.Change{{Path: "/a", LibraryIDs: []string{"l1"}}, {Path: "/b", LibraryIDs: []string{"l1", "l2"}}}
	if err := audiobookshelf.New(mediaservertest.Direct(), server.URL, "tok").Changed(t.Context(), changes); err != nil {
		t.Fatalf("Changed: %v", err)
	}
	if want := []string{"POST /api/libraries/l1/scan", "POST /api/libraries/l2/scan"}; !reflect.DeepEqual(seen, want) {
		t.Errorf("requests = %q, want %q", seen, want)
	}
}
