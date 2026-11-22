package kavita_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/mediaserver/kavita"
	"github.com/makearr/Solarr/internal/mediaserver/mediaservertest"
)

func TestClient_TestAndLibraries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/Library/libraries":
			_, _ = w.Write([]byte(`[{"id":3,"name":"Strips","type":1,"folders":["/comics"]}]`))
		case "/api/Server/server-info-slim":
			_, _ = w.Write([]byte(`{"kavitaVersion":"0.8.4"}`))
		case "/api/Users":
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()
	client := kavita.New(mediaservertest.Direct(), server.URL, "k")
	info, err := client.Test(t.Context())
	if err != nil || info != (mediaserver.Info{Name: "Kavita", Version: "0.8.4"}) {
		t.Errorf("info = %+v, err %v; want a non-admin key", info, err)
	}
	libraries, _ := client.Libraries(t.Context())
	if want := []mediaserver.Library{{ID: "3", Name: "Strips", Type: "comic", Paths: []string{"/comics"}}}; !reflect.DeepEqual(libraries, want) {
		t.Errorf("libraries = %+v", libraries)
	}
}

func TestClient_ChangedScansEachLibraryOnce(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = append(seen, r.Method+" "+r.URL.RequestURI()) }))
	defer server.Close()
	changes := []mediaserver.Change{{Path: "/a", LibraryIDs: []string{"3"}}, {Path: "/b", LibraryIDs: []string{"3"}}}
	if err := kavita.New(mediaservertest.Direct(), server.URL, "k").Changed(t.Context(), changes); err != nil {
		t.Fatalf("Changed: %v", err)
	}
	if want := []string{"POST /api/Library/scan?libraryId=3"}; !reflect.DeepEqual(seen, want) {
		t.Errorf("requests = %q, want %q", seen, want)
	}
}

func TestClient_LoginSignsInAgainOnA401(t *testing.T) {
	var logins int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/Account/login":
			logins++
			_, _ = w.Write([]byte(`{"token":"jwt` + string(rune('0'+logins)) + `"}`))
		case "/api/Library/libraries":
			if r.Header.Get("Authorization") != "Bearer jwt2" && logins < 2 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	if _, err := kavita.NewLogin(mediaservertest.Direct(), server.URL, "u", "p").Libraries(t.Context()); err != nil {
		t.Fatalf("Libraries: %v", err)
	}
	if logins != 2 {
		t.Errorf("logins = %d, want 2 (first token refused)", logins)
	}
}

func TestClient_LoginRefusedIsRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	_, err := kavita.NewLogin(mediaservertest.Direct(), server.URL, "u", "bad").Libraries(t.Context())
	if !errors.Is(err, mediaserver.ErrRejected) {
		t.Errorf("err = %v, want ErrRejected", err)
	}
}
