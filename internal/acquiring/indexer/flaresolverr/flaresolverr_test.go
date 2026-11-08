package flaresolverr_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer/flaresolverr"
)

func TestChallengeIsSolvedOnceThenRepeatedWithTheClearance(t *testing.T) {
	solves := 0
	solver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		solves++
		_, _ = io.WriteString(w, `{"status":"ok","solution":{"status":200,"response":"<html>page</html>","userAgent":"Solved/1","cookies":[{"name":"cf_clearance","value":"ok"}]}}`)
	}))
	defer solver.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("cf_clearance"); err != nil || cookie.Value != "ok" || r.UserAgent() != "Solved/1" {
			w.Header().Set("Server", "cloudflare")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, "<rss/>")
	}))
	defer site.Close()

	transport := &flaresolverr.Transport{Direct: http.DefaultTransport, Solver: http.DefaultTransport, SolverURL: solver.URL}
	for round := 1; round <= 2; round++ {
		request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, site.URL+"/api", nil)
		response, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 200 || strings.TrimSpace(string(body)) != "<rss/>" {
			t.Fatalf("round %d: %d %q", round, response.StatusCode, body)
		}
	}
	if solves != 1 {
		t.Errorf("FlareSolverr asked %d times, want once (the second request carries the clearance)", solves)
	}
}

func TestSolverRefusingIsAnError(t *testing.T) {
	solver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"error","message":"timeout"}`)
	}))
	defer solver.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "cloudflare")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer site.Close()
	transport := &flaresolverr.Transport{Direct: http.DefaultTransport, Solver: http.DefaultTransport, SolverURL: solver.URL}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, site.URL, nil)
	if _, err := transport.RoundTrip(request); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Errorf("err = %v, want the solver's message", err)
	}
}
