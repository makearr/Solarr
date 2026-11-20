package spec

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// signInServer is a site whose sign-in counts its posts and accepts only the password "pw".
func signInServer(posts *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`<form id="f" action="/do"><input name="username"><input name="password"></form>`))
		case "/do":
			posts.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("password") != "pw" {
				_, _ = w.Write([]byte(`<div class="err">Wrong password</div>`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "ok", Path: "/"})
			http.Redirect(w, r, "/index", http.StatusFound)
		case "/index":
			_, _ = w.Write([]byte(`<a href="/logout">logout</a>`))
		case "/search":
			if c, err := r.Cookie("sid"); err != nil || c.Value != "ok" {
				_, _ = w.Write([]byte(`<div class="needlogin">please sign in</div>`))
				return
			}
			_, _ = w.Write([]byte(htmlResults))
		}
	}))
}

func signInSpec() Spec {
	spec := htmlSpec()
	spec.Login = &Login{
		Method: "form", Path: "/login", Form: "form#f",
		Inputs: map[string]string{"username": "{{ .Config.username }}", "password": "{{ .Config.password }}"},
		Error:  []ErrorSelector{{Selector: "div.err"}},
		Test:   &LoginTest{Path: "/index", Selector: `a[href="/logout"]`},
	}
	spec.Search.Error = []ErrorSelector{{Selector: "div.needlogin"}}
	spec.Settings = []Setting{{Name: "username", Type: "text"}, {Name: "password", Type: "password"}}
	return spec
}

func TestLogin_SearchesRunningTogetherSignInOnce(t *testing.T) {
	var posts atomic.Int32
	server := signInServer(&posts)
	defer server.Close()
	driver := testDriver(t, signInSpec(), server, map[string]string{"username": "me", "password": "pw"})

	var wait sync.WaitGroup
	for range 20 {
		wait.Go(func() {
			if items, err := driver.Search(t.Context(), indexer.SearchRequest{Query: "x"}); err != nil || len(items) != 2 {
				t.Errorf("Search = %d items, %v", len(items), err)
			}
		})
	}
	wait.Wait()
	if got := posts.Load(); got != 1 {
		t.Errorf("%d sign-ins for 20 searches, want one", got)
	}
}

func TestLogin_ARefusedSignInIsNotRepeatedUntilTheMemoryExpiresOrTheSettingsChange(t *testing.T) {
	var posts atomic.Int32
	server := signInServer(&posts)
	defer server.Close()
	driver := testDriver(t, signInSpec(), server, map[string]string{"username": "me", "password": "nope"})
	fake, ok := driver.clock.(*clock.Fake)
	if !ok {
		t.Fatal("test driver clock is not the fake")
	}

	for range 20 {
		if _, err := driver.Search(t.Context(), indexer.SearchRequest{Query: "x"}); err == nil {
			t.Fatal("Search with a wrong password succeeded")
		}
	}
	if got := posts.Load(); got != 1 {
		t.Errorf("%d sign-ins for 20 refused searches, want one", got)
	}

	fake.Advance(16 * time.Minute)
	_, _ = driver.Search(t.Context(), indexer.SearchRequest{Query: "x"})
	if got := posts.Load(); got != 2 {
		t.Errorf("%d sign-ins after 16 minutes, want a second", got)
	}

	fixed := testDriver(t, signInSpec(), server, map[string]string{"username": "me", "password": "pw"})
	if _, err := fixed.Search(t.Context(), indexer.SearchRequest{Query: "x"}); err != nil {
		t.Errorf("Search with new settings = %v, want them tried at once", err)
	}
}
