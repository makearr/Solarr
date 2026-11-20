package spec

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameSite(t *testing.T) {
	driver := &Driver{
		baseURL: "https://www.tracker.example/",
		spec:    Spec{Links: []string{"https://tracker.example/"}, LegacyLinks: []string{"https://old-mirror.example/"}},
	}
	for target, want := range map[string]bool{
		"https://www.tracker.example/download/1": true,
		"https://TRACKER.example/x":              true,
		"https://dl.tracker.example/x":           true,
		"https://old-mirror.example/x":           true,
		"https://cdn.other.example/x":            false,
		"https://tracker.example.evil.example/x": false,
		"https://eviltracker.example/x":          false,
		"https://tracker.example.co.uk/x":        false,
		"https://other.co.uk/x":                  false,
		"://bad":                                 false,
	} {
		if got := driver.sameSite(target); got != want {
			t.Errorf("sameSite(%q) = %v, want %v", target, got, want)
		}
	}
}

// The owner's cookie and the spec's headers reach the tracker and no other host.
func TestFetchSendsTheCookieAndHeadersOnlyToTheTracker(t *testing.T) {
	var seen http.Header
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.Header.Clone() }))
	defer server.Close()
	spec := htmlSpec()
	spec.Login = &Login{Method: "cookie"}
	driver := testDriver(t, spec, server, map[string]string{"cookie": "uid=1; pass=secret"})
	headers := map[string][]string{"X-Api-Key": {"key"}}

	if _, err := driver.fetch(t.Context(), http.MethodGet, server.URL+"/d", nil, headers); err != nil {
		t.Fatalf("fetch on the tracker: %v", err)
	}
	if seen.Get("Cookie") == "" || seen.Get("X-Api-Key") != "key" {
		t.Errorf("on the tracker: cookie %q key %q, want both", seen.Get("Cookie"), seen.Get("X-Api-Key"))
	}

	driver.baseURL = "https://tracker.example/"
	if _, err := driver.fetch(t.Context(), http.MethodGet, server.URL+"/d", nil, headers); err != nil {
		t.Fatalf("fetch off the tracker: %v", err)
	}
	if seen.Get("Cookie") != "" || seen.Get("X-Api-Key") != "" {
		t.Errorf("off the tracker: cookie %q key %q, want neither", seen.Get("Cookie"), seen.Get("X-Api-Key"))
	}
}
