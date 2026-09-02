package apiserver_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/auth"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/settings"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/migrations"
)

// setupFixture is an install with no accounts -- the state the wizard exists
// for, and the one every other fixture skips past.
type setupFixture struct {
	server *apiserver.Server
	cache  *settings.Cache
}

func newSetupFixture(t *testing.T, roots apiserver.RootChecker) setupFixture {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "settings", Path: filepath.Join(directory, "settings.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Settings()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	cache, err := settings.NewCache(t.Context(), opened)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	server, err := apiserver.New(apiserver.Config{
		Bind: "127.0.0.1", Port: 0, BaseURL: "/",
		Checker: fakeChecker{},
		Clock:   fake,
		Logger:  slog.New(slog.DiscardHandler),
		Roots:   roots,
		Auth: apiserver.AuthConfig{
			Mode:     apiserver.AuthLocal,
			Sessions: auth.NewSessions([]byte("0123456789abcdef0123456789abcdef"), fake, time.Hour),
			Settings: cache,
			Clock:    fake,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return setupFixture{server: server, cache: cache}
}

func (f setupFixture) post(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(recorder, request)
	return recorder
}

// The window in which anyone can claim the install closes on the first account
// and never reopens. Tying it to "the wizard finished" would leave it open
// across a restart someone abandoned halfway -- exactly when nobody is watching.
func TestCreateFirstAdministrator_ClosesPermanently(t *testing.T) {
	fixture := newSetupFixture(t, nil)

	first := fixture.post(t, "/api/v1/setup/admin",
		`{"username":"avs","password":"correct horse battery"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first claim: status = %d, want 201 (%s)", first.Code, first.Body)
	}
	// Signed in by the same response: asking someone to log in with credentials
	// they typed ten seconds ago is friction with nothing behind it.
	if len(first.Result().Cookies()) == 0 {
		t.Error("the claim did not set a session cookie")
	}

	second := fixture.post(t, "/api/v1/setup/admin",
		`{"username":"attacker","password":"correct horse battery"}`)
	if second.Code != http.StatusConflict {
		t.Errorf("second claim: status = %d, want 409 -- the install is claimable twice",
			second.Code)
	}
	if users := fixture.cache.Snapshot().Users(); len(users) != 1 {
		t.Errorf("%d accounts exist, want 1", len(users))
	}
}

func TestCreateFirstAdministrator_RejectsWeakInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"no username", `{"username":"  ","password":"correct horse battery"}`},
		{"short password", `{"username":"avs","password":"hunter2"}`},
		{"not json", `not json at all`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSetupFixture(t, nil)

			if got := fixture.post(t, "/api/v1/setup/admin", test.body).Code; got != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", got)
			}
			// A rejected attempt must not consume the one claim available.
			if users := fixture.cache.Snapshot().Users(); len(users) != 0 {
				t.Errorf("%d accounts created by a rejected request", len(users))
			}
		})
	}
}

// The status endpoint is unauthenticated, so it must give away nothing beyond
// what is outstanding.
func TestGetSetupStatus_IsUnauthenticatedAndMinimal(t *testing.T) {
	fixture := newSetupFixture(t, nil)

	recorder := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/setup", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body["needs_administrator"] != true {
		t.Errorf("needs_administrator = %v, want true on a fresh install", body["needs_administrator"])
	}
	for _, leaked := range []string{"version", "commit", "users", "path", "paths"} {
		if _, present := body[leaked]; present {
			t.Errorf("the unauthenticated status leaks %q", leaked)
		}
	}
}

// stubRoots stands in for a filesystem, so a test can make a path unusable or
// unwritable without arranging either on disk.
type stubRoots struct {
	invalid     error
	notWritable error
}

func (r stubRoots) Validate(string) error { return r.invalid }
func (r stubRoots) Writable(string) error { return r.notWritable }

// A path that does not exist is a mistake in what was typed, and is refused --
// accepting it turns a typo into a mystery at the first import.
func TestAddRootFolder_RefusesAnUnusablePath(t *testing.T) {
	fixture := newSetupFixture(t, stubRoots{invalid: errUnusable})

	created := fixture.post(t, "/api/v1/setup/admin",
		`{"username":"avs","password":"correct horse battery"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("claim: %d", created.Code)
	}
	cookie := created.Result().Cookies()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/rootfolders",
		strings.NewReader(`{"path":"/media/music"}`))
	request.Header.Set("Content-Type", "application/json")
	for _, c := range cookie {
		request.AddCookie(c)
	}
	recorder := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", recorder.Code, recorder.Body)
	}
	if folders := fixture.cache.Snapshot().RootFolders(); len(folders) != 0 {
		t.Errorf("an unusable root was stored anyway: %+v", folders)
	}
}

var errUnusable = errors.New("\"/media/music\" does not exist inside the container")

// A folder that exists but is owned by somebody else is not a mistake -- it is a
// working library with one thing to fix, and whether to fix it is the
// operator's call. MediaNexus reports it and accepts it, because refusing would
// mean a library owned by another user could not be configured at all.
func TestAddRootFolder_AcceptsAndFlagsAnUnwritablePath(t *testing.T) {
	fixture := newSetupFixture(t, stubRoots{notWritable: errUnwritable})

	created := fixture.post(t, "/api/v1/setup/admin",
		`{"username":"avs","password":"correct horse battery"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("claim: %d", created.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/rootfolders",
		strings.NewReader(`{"path":"/media/music"}`))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range created.Result().Cookies() {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 -- ownership is the operator's to decide (%s)",
			recorder.Code, recorder.Body)
	}

	var body struct {
		Path     string `json:"path"`
		Writable *bool  `json:"writable"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body.Writable == nil || *body.Writable {
		t.Error("the root was stored without being flagged as unwritable")
	}

	// Stored, so the interface can show it and the operator can act on it.
	if folders := fixture.cache.Snapshot().RootFolders(); len(folders) != 1 {
		t.Errorf("%d root folders stored, want 1", len(folders))
	}
}

var errUnwritable = errors.New(`"/media/music" is owned by uid=0 gid=0`)
