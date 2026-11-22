package stash_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
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
