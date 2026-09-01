package apiserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/authz"
	"github.com/makearr/MediaNexus/internal/settings"
)

// fakeChecker makes either probe fail without arranging a broken database.
type fakeChecker struct {
	live  error
	ready error
}

func (c fakeChecker) Live(context.Context) error  { return c.live }
func (c fakeChecker) Ready(context.Context) error { return c.ready }

func newServer(t *testing.T, checker apiserver.Checker) *apiserver.Server {
	t.Helper()
	server, err := apiserver.New(apiserver.Config{
		Bind: "127.0.0.1", Port: 0, BaseURL: "/",
		Build: apiserver.BuildInfo{
			Version: "1.2.3", Commit: "abc1234", BuildDate: "2026-03-01T00:00:00Z",
			SourceURL: "https://github.com/makearr/MediaNexus", License: "AGPL-3.0-or-later",
		},
		Checker: checker,
		Logger:  slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server
}

func get(t *testing.T, server *apiserver.Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func TestHealth_IsOKWhenTheProcessIsWorking(t *testing.T) {
	server := newServer(t, fakeChecker{})
	if got := get(t, server, "/api/v1/health").Code; got != http.StatusOK {
		t.Errorf("status = %d, want 200", got)
	}
}

func TestHealth_DoesNotDependOnReadiness(t *testing.T) {
	// A dead indexer must not restart the container: liveness is about this
	// process, not about anything it talks to (section 20.2).
	server := newServer(t, fakeChecker{ready: errors.New("database is starting")})

	if got := get(t, server, "/api/v1/health").Code; got != http.StatusOK {
		t.Errorf("health = %d while not ready, want 200", got)
	}
}

func TestHealth_FailsWhenTheWriteQueueIsWedged(t *testing.T) {
	server := newServer(t, fakeChecker{live: errors.New("write queue is not draining")})
	server.MarkReady()

	response := get(t, server, "/api/v1/health")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want RFC 7807's", got)
	}
}

// Readiness is false during startup and shutdown, so an orchestrator stops
// sending work before the process stops accepting it.
func TestReady_IsFalseUntilMarkedAndFalseAgainAfterShutdown(t *testing.T) {
	server := newServer(t, fakeChecker{})

	if got := get(t, server, "/api/v1/ready").Code; got != http.StatusServiceUnavailable {
		t.Errorf("ready during startup = %d, want 503", got)
	}

	server.MarkReady()
	if got := get(t, server, "/api/v1/ready").Code; got != http.StatusOK {
		t.Errorf("ready after MarkReady = %d, want 200", got)
	}

	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := get(t, server, "/api/v1/ready").Code; got != http.StatusServiceUnavailable {
		t.Errorf("ready after shutdown = %d, want 503", got)
	}
}

// AGPL section 13 obliges the running program to offer its source to all users
// interacting with it remotely, which is not limited to those who can log in.
func TestAbout_IsUnauthenticatedAndNamesTheExactBuild(t *testing.T) {
	server := newServer(t, fakeChecker{})
	response := get(t, server, "/api/v1/about")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}

	var about apiserver.BuildInfo
	if err := json.NewDecoder(response.Body).Decode(&about); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if about.Version != "1.2.3" || about.Commit != "abc1234" {
		t.Errorf("about = %+v, want the injected build metadata", about)
	}
	if about.SourceURL == "" {
		t.Error("SourceURL is empty; section 13 requires a link to the Corresponding Source")
	}
	if about.License != "AGPL-3.0-or-later" {
		t.Errorf("License = %q", about.License)
	}
}

func TestRoutes_HonourTheBaseURL(t *testing.T) {
	server, err := apiserver.New(apiserver.Config{
		Bind: "127.0.0.1", Port: 0, BaseURL: "/medianexus/",
		Checker: fakeChecker{}, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := get(t, server, "/medianexus/api/v1/health").Code; got != http.StatusOK {
		t.Errorf("health under the base URL = %d, want 200", got)
	}
	if got := get(t, server, "/api/v1/health").Code; got != http.StatusNotFound {
		t.Errorf("health at the root = %d, want 404 when a base URL is set", got)
	}
}

func TestNew_RequiresAChecker(t *testing.T) {
	if _, err := apiserver.New(apiserver.Config{Bind: "127.0.0.1", Port: 0}); err == nil {
		t.Error("a server with no Checker was accepted; its probes would panic")
	}
}

func TestProblem_MapsDomainErrorsToStatuses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
		slug string
	}{
		// Invariant 37: a hidden category is 404, never 403.
		{"hidden category", authz.ErrNotVisible, http.StatusNotFound, apiserver.TypeNotFound},
		{"visible but not permitted", authz.ErrInsufficientPermission, http.StatusForbidden, apiserver.TypeForbidden},
		{"still referenced", settings.ErrInUse, http.StatusConflict, apiserver.TypeConflict},
		{"missing record", settings.ErrNotFound, http.StatusNotFound, apiserver.TypeNotFound},
		{"unclassified", errors.New("something broke"), http.StatusInternalServerError, apiserver.TypeInternal},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			apiserver.WriteErrorForTest(recorder, slog.New(slog.DiscardHandler), testCase.err)

			if recorder.Code != testCase.want {
				t.Errorf("status = %d, want %d", recorder.Code, testCase.want)
			}
			var problem apiserver.Problem
			if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if problem.Type != testCase.slug {
				t.Errorf("type = %q, want %q", problem.Type, testCase.slug)
			}
			if problem.Status != testCase.want {
				t.Errorf("body status = %d, want %d", problem.Status, testCase.want)
			}
		})
	}
}

// An unclassified error must not hand its internals to a client.
func TestProblem_InternalErrorsDoNotLeakTheirDetail(t *testing.T) {
	recorder := httptest.NewRecorder()
	apiserver.WriteErrorForTest(recorder, slog.New(slog.DiscardHandler),
		errors.New("dial tcp 10.0.0.5:5432: connection refused"))

	if recorder.Body.String() == "" {
		t.Fatal("empty body")
	}
	if got := recorder.Body.String(); contains(got, "10.0.0.5") {
		t.Errorf("the response leaked internal detail: %s", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
