package spec

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func testDriver(t *testing.T, spec Spec, server *httptest.Server, settings map[string]string) *Driver {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	host, _, _ := strings.Cut(strings.TrimPrefix(server.URL, "http://"), ":")
	client := httpx.NewClient(fake, httpx.NewGuard(nil, nil, []string{host}), limiter, breaker, httpx.ClientConfig{
		Provider: "spec-test", UserAgent: "test", Transport: http.DefaultTransport, Jar: jar,
	})
	return New(spec, client, fake, server.URL, settings)
}

func str(text string) *string { return &text }

const htmlResults = `<html><body><table>
<tr class="r"><td class="n"><a href="/t/1">Artist - Album (2020) [FLAC]</a></td><td class="c">Lossless</td><td class="s">1.5 GB</td><td class="se">1,234</td><td class="le">7</td><td class="d">2 days ago</td><td><a class="dl" href="/dl/1.torrent">get</a></td></tr>
<tr class="r"><td class="n"><a href="/t/2">Other - Thing</a></td><td class="c">Lossless</td><td class="s">700 MB</td><td class="se">3</td><td class="le">0</td><td class="d">1 hour ago</td><td><a class="dl" href="magnet:?xt=urn:btih:abc">m</a></td></tr>
</table></body></html>`

func htmlSpec() Spec {
	return Spec{
		ID: "test", Name: "Test", Privacy: "public",
		Caps: Caps{Categories: []CategoryMapping{{ID: "10", Cat: "Audio/Lossless", Description: "Lossless"}, {ID: "11", Cat: "Audio/MP3", Description: "MP3"}}},
		Search: Search{
			Paths:  []SearchPath{{Path: "/search"}},
			Inputs: map[string]string{"q": "{{ .Keywords }}", "cat": `{{ range .Categories }}{{.}},{{end}}`},
			Rows:   Rows{Selector: "tr.r"},
			Fields: []Extractor{
				{Name: "title", Selector: "td.n a"}, {Name: "details", Selector: "td.n a", Attribute: "href"},
				{Name: "download", Selector: "a.dl", Attribute: "href"}, {Name: "category", Selector: "td.c"},
				{Name: "size", Selector: "td.s"}, {Name: "seeders", Selector: "td.se"}, {Name: "leechers", Selector: "td.le"},
				{Name: "date", Selector: "td.d", Filters: []Filter{{Name: "timeago"}}},
			},
		},
	}
}

func TestSearchReadsHTMLRowsIntoReleases(t *testing.T) {
	var asked url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query()
		_, _ = w.Write([]byte(htmlResults))
	}))
	defer server.Close()
	driver := testDriver(t, htmlSpec(), server, nil)

	items, err := driver.Search(t.Context(), indexer.SearchRequest{Query: "artist album", Categories: []int{3000}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if asked.Get("q") != "artist album" || asked.Get("cat") != "10,11," {
		t.Errorf("asked %v, want the keywords and both site categories of the Audio parent", asked)
	}
	if len(items) != 2 {
		t.Fatalf("%d items, want 2: %+v", len(items), items)
	}
	first := items[0]
	if first.Title != "Artist - Album (2020) [FLAC]" || first.Link != server.URL+"/dl/1.torrent" || first.GUID != server.URL+"/t/1" ||
		first.SizeBytes != 1610612736 || first.Seeders != 1234 || first.Peers != 1241 || len(first.Categories) != 1 || first.Categories[0] != 3040 {
		t.Errorf("first = %+v", first)
	}
	if items[1].Link != "magnet:?xt=urn:btih:abc" {
		t.Errorf("magnet link = %q", items[1].Link)
	}
	if got, _ := driver.Search(t.Context(), indexer.SearchRequest{Query: "x", Categories: []int{2000}}); got != nil {
		t.Errorf("a movie search on an audio-only site returned %d items, want none and no request", len(got))
	}
}

func TestSearchReadsJSONWithParentValuesAndConditions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"categoryID":"10","data":{"torrents":[
		 {"name":"A - B FLAC","id":5,"size":1048576,"seeders":4,"leechers":1,"meta":{"hash":"ABCDEF"}},
		 {"name":"Hidden","id":6,"size":1,"seeders":0,"leechers":0,"free":true}]}}`))
	}))
	defer server.Close()
	spec := Spec{
		ID: "json", Name: "J", Caps: Caps{Categories: []CategoryMapping{{ID: "10", Cat: "Audio/Lossless"}}},
		Search: Search{
			Paths: []SearchPath{{Path: "/api", Response: map[string]string{"type": "json"}}},
			Rows:  Rows{Selector: "data.torrents:not(free:contains(true))"},
			Fields: []Extractor{
				{Name: "title", Selector: "name"}, {Name: "category", Selector: "..categoryID"},
				{Name: "infohash", Selector: "meta.hash"}, {Name: "size", Selector: "size"}, {Name: "seeders", Selector: "seeders"},
				{Name: "details", Text: str("/t/{{ .Result.title }}")},
			},
		},
	}
	items, err := testDriver(t, spec, server, nil).Search(t.Context(), indexer.SearchRequest{Query: "a b"})
	if err != nil || len(items) != 1 {
		t.Fatalf("Search = %+v, %v; want the one row that is not free", items, err)
	}
	if items[0].InfoHash != "abcdef" || !strings.Contains(items[0].Link, "abcdef") || items[0].SizeBytes != 1048576 || items[0].Categories[0] != 3040 {
		t.Errorf("item = %+v", items[0])
	}
}

func TestFormLoginKeepsTheSessionAndRetriesOnce(t *testing.T) {
	logins := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<form id="f" action="/do"><input type="hidden" name="token" value="T1"><input name="username"><input name="password"></form>`))
			}
		case "/do":
			_ = r.ParseForm()
			if r.Form.Get("token") != "T1" || r.Form.Get("username") != "me" || r.Form.Get("password") != "pw" {
				_, _ = w.Write([]byte(`<div class="err">Wrong password</div>`))
				return
			}
			logins++
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
	defer server.Close()
	spec := htmlSpec()
	spec.Login = &Login{
		Method: "form", Path: "/login", Form: "form#f",
		Inputs: map[string]string{"username": "{{ .Config.username }}", "password": "{{ .Config.password }}"},
		Error:  []ErrorSelector{{Selector: "div.err"}},
		Test:   &LoginTest{Path: "/index", Selector: `a[href="/logout"]`},
	}
	spec.Search.Error = []ErrorSelector{{Selector: "div.needlogin"}}
	spec.Settings = []Setting{{Name: "username", Type: "text"}, {Name: "password", Type: "password"}}

	driver := testDriver(t, spec, server, map[string]string{"username": "me", "password": "pw"})
	for range 2 {
		items, err := driver.Search(t.Context(), indexer.SearchRequest{Query: "x"})
		if err != nil || len(items) != 2 {
			t.Fatalf("Search = %d items, %v", len(items), err)
		}
	}
	if logins != 1 {
		t.Errorf("signed in %d times, want once (the cookie jar keeps the session)", logins)
	}

	wrong := testDriver(t, spec, server, map[string]string{"username": "me", "password": "nope"})
	if _, err := wrong.Search(t.Context(), indexer.SearchRequest{Query: "x"}); err == nil || !strings.Contains(err.Error(), "Wrong password") {
		t.Errorf("wrong password: %v, want the site's own message", err)
	}
}

func TestFetchFindsTheDownloadOnTheDetailsPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/details/1":
			_, _ = w.Write([]byte(`<a class="get" href="/file/1.torrent">download</a>`))
		case "/details/2":
			_, _ = w.Write([]byte(`<a href="magnet:?xt=urn:btih:feed">magnet</a>`))
		case "/file/1.torrent":
			_, _ = w.Write([]byte("d8:announce0:e"))
		}
	}))
	defer server.Close()
	spec := htmlSpec()
	spec.Download = &Download{Selectors: []Extractor{{Selector: "a.get", Attribute: "href"}, {Selector: `a[href^="magnet:"]`, Attribute: "href"}}}
	driver := testDriver(t, spec, server, nil)
	if got, err := driver.Fetch(t.Context(), server.URL+"/details/1"); err != nil || string(got) != "d8:announce0:e" {
		t.Errorf("Fetch = %q, %v; want the torrent file", got, err)
	}
	if got, err := driver.Fetch(t.Context(), server.URL+"/details/2"); err != nil || string(got) != "magnet:?xt=urn:btih:feed" {
		t.Errorf("Fetch = %q, %v; want the magnet link as the release", got, err)
	}
}

func TestFilters(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	context := filterContext{variables: &Variables{Keywords: "alpha beta"}, now: now}
	for _, test := range []struct {
		name, in string
		args     []string
		want     string
	}{
		{"dateparse", "Nov 3, 2025 10:30", []string{"MMM d, yyyy HH:mm"}, "2025-11-03T10:30:00Z"},
		{"dateparse", "2025-11-03 10:30:00 +02:00", []string{"yyyy-MM-dd HH:mm:ss zzz"}, "2025-11-03T08:30:00Z"},
		{"timeago", "2 days 3 hours ago", nil, "2026-11-06T09:00:00Z"},
		{"fuzzytime", "Yesterday 08:15 PM", nil, "2026-11-07T20:15:00Z"},
		{"querystring", "/dl.php?id=42&x=1", []string{"id"}, "42"},
		{"regexp", "ID: 77 (x)", []string{`(\d+)`}, "77"},
		{"re_replace", "a.b.c", []string{`\.`, "-"}, "a-b-c"},
		{"split", "a|b|c", []string{"|", "-1"}, "c"},
		{"andmatch", "Alpha and BETA", nil, "Alpha and BETA"},
		{"andmatch", "Alpha only", nil, ""},
		{"validfilename", `a/b:c`, nil, "abc"},
		{"diacritics", "Beyoncé", nil, "Beyonce"},
		{"validate", "x", []string{"a, x"}, "x"},
		{"validate", "z", []string{"a, x"}, ""},
	} {
		got, err := applyFilter(test.in, test.name, test.args, context)
		if err != nil || got != test.want {
			t.Errorf("%s(%q, %v) = %q, %v; want %q", test.name, test.in, test.args, got, err, test.want)
		}
	}
}

func TestTemplatesReadDotNetStyle(t *testing.T) {
	variables := newVariables(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	variables.Keywords = "a  b"
	variables.Config["cat-id"] = "9"
	variables.Config["free"] = true
	variables.Categories = []string{"1", "2"}
	for text, want := range map[string]string{
		`{{ re_replace .Keywords "[\s]+" "+" }}`:                          "a+b",
		`s-{{ .Config.cat-id }}-{{ .Today.Year }}`:                        "s-9-2026",
		`{{ if .Config.free }}f{{ else }}n{{ end }}`:                      "f",
		`{{ join .Categories "," }}`:                                      "1,2",
		`{{ or .Query.Season "none" }}`:                                   "none",
		`{{ if eq .Config.cat-id "9" }}yes{{ end }}{{ .Config.missing }}`: "yes",
	} {
		if got, err := render(text, variables); err != nil || got != want {
			t.Errorf("render(%q) = %q, %v; want %q", text, got, err, want)
		}
	}
}
