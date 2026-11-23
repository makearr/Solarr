package komga_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/mediaserver/komga"
	"github.com/makearr/Solarr/internal/mediaserver/mediaservertest"
)

func TestClient_TestLibrariesAndScan(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("X-Api-Key"))
		switch r.URL.Path {
		case "/api/v2/users/me":
			_, _ = w.Write([]byte(`{"email":"a@example.invalid","roles":["USER","ADMIN"]}`))
		case "/actuator/info":
			_, _ = w.Write([]byte(`{"build":{"version":"1.11.0"}}`))
		case "/api/v1/libraries":
			_, _ = w.Write([]byte(`[{"id":"L1","name":"Comics","root":"/data/comics"}]`))
		}
	}))
	defer server.Close()
	client := komga.New(mediaservertest.Direct(), server.URL, "k1")
	info, err := client.Test(t.Context())
	if want := (mediaserver.Info{Name: "Komga", Version: "1.11.0", Admin: true}); err != nil || info != want {
		t.Errorf("info = %+v, %v; want %+v", info, err, want)
	}
	libraries, _ := client.Libraries(t.Context())
	if want := []mediaserver.Library{{ID: "L1", Name: "Comics", Type: "comic", Paths: []string{"/data/comics"}}}; !reflect.DeepEqual(libraries, want) {
		t.Errorf("libraries = %+v", libraries)
	}
	seen = nil
	changes := []mediaserver.Change{{Path: "/data/comics/a.cbz", LibraryIDs: []string{"L1"}}, {Path: "/data/comics/b.cbz", LibraryIDs: []string{"L1"}}}
	if err := client.Changed(t.Context(), changes); err != nil {
		t.Fatal(err)
	}
	if want := []string{"POST /api/v1/libraries/L1/scan k1"}; !reflect.DeepEqual(seen, want) {
		t.Errorf("requests = %q, want %q", seen, want)
	}
}

func TestClient_LoginSendsBasicCredentials(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"roles":[]}`))
	}))
	defer server.Close()
	info, err := komga.NewLogin(mediaservertest.Direct(), server.URL, "reader", "pw").Test(t.Context())
	if err != nil || info.Admin || got != "Basic cmVhZGVyOnB3" {
		t.Errorf("info %+v err %v header %q", info, err, got)
	}
}
