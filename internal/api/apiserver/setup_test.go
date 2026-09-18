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

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/auth"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

// An install with no accounts: the state the wizard exists for.
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

// authed replays a request with the cookies a prior claim/login response set,
// for the PATCH endpoints that require an administrator.
func (f setupFixture) authed(t *testing.T, method, path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(recorder, request)
	return recorder
}

// The claim closes on the first account, not when the wizard finishes.
func TestCreateFirstAdministrator_ClosesPermanently(t *testing.T) {
	fixture := newSetupFixture(t, nil)

	first := fixture.post(t, "/api/v1/setup/admin",
		`{"username":"avs","password":"correct horse battery"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first claim: status = %d, want 201 (%s)", first.Code, first.Body)
	}
	// Signed in by the same response.
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

// A short password is not weak input. Length and composition rules produce
// predictable substitutions rather than strength, and on a self-hosted install
// the only person they constrain is its owner. Empty stays refused: an account
// nobody can fail to authenticate as is auth_mode none, which is a deliberate
// decision made in config.yml.
func TestCreateFirstAdministrator_AcceptsAShortPassword(t *testing.T) {
	fixture := newSetupFixture(t, nil)

	got := fixture.post(t, "/api/v1/setup/admin", `{"username":"avs","password":"x"}`)
	if got.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: a length rule has come back", got.Code)
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
		{"no password", `{"username":"avs","password":""}`},
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

// Unauthenticated, so it must give away nothing but the outstanding steps.
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

// Makes a path unusable or unwritable without arranging either on disk.
type stubRoots struct {
	invalid     error
	notWritable error
	freeBytes   int64
	usageErr    error
}

func (r stubRoots) Validate(string) error { return r.invalid }
func (r stubRoots) Writable(string) error { return r.notWritable }
func (r stubRoots) Usage(string) (int64, int64, error) {
	return r.freeBytes * 2, r.freeBytes, r.usageErr
}

// A path that does not exist is a typo, and is refused.
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

// Owned by somebody else is the operator's call, so it is flagged and accepted.
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

	// Stored, so the interface can show it.
	if folders := fixture.cache.Snapshot().RootFolders(); len(folders) != 1 {
		t.Errorf("%d root folders stored, want 1", len(folders))
	}
}

var errUnwritable = errors.New(`"/media/music" is owned by uid=0 gid=0`)

// addedRootFolder claims the install and adds one root, returning its id and
// the admin's session cookies for the PATCH tests below.
func addedRootFolder(t *testing.T, fixture setupFixture) (id int64, cookies []*http.Cookie) {
	t.Helper()
	claim := fixture.post(t, "/api/v1/setup/admin", `{"username":"avs","password":"correct horse battery"}`)
	if claim.Code != http.StatusCreated {
		t.Fatalf("claim: %d", claim.Code)
	}
	cookies = claim.Result().Cookies()

	created := fixture.authed(t, http.MethodPost, "/api/v1/rootfolders", `{"path":"/media/music"}`, cookies)
	if created.Code != http.StatusCreated {
		t.Fatalf("add root folder: %d (%s)", created.Code, created.Body)
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	return body.ID, cookies
}

func TestUpdateRootFolderPolicy_UpdatesReadOnlyAllowRenameAndReserve(t *testing.T) {
	fixture := newSetupFixture(t, stubRoots{})
	id, cookies := addedRootFolder(t, fixture)

	recorder := fixture.authed(t, http.MethodPatch, "/api/v1/rootfolders/"+itoa(id),
		`{"read_only":true,"allow_rename":true,"min_free_bytes":50000000000}`, cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}

	updated, found := fixture.cache.Snapshot().RootFolder(id)
	if !found {
		t.Fatal("root folder not found after update")
	}
	if !updated.ReadOnly || !updated.AllowRename || updated.MinFreeBytes != 50_000_000_000 {
		t.Errorf("root = %+v, want ReadOnly/AllowRename true and MinFreeBytes 50_000_000_000", updated)
	}
}

func TestUpdateRootFolderPolicy_RequiresAdministrator(t *testing.T) {
	fixture := newSetupFixture(t, stubRoots{})
	id, _ := addedRootFolder(t, fixture)

	recorder := fixture.authed(t, http.MethodPatch, "/api/v1/rootfolders/"+itoa(id),
		`{"read_only":true,"allow_rename":false,"min_free_bytes":0}`, nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unauthenticated policy change", recorder.Code)
	}
}

func TestUpdateRootFolderPolicy_RefusesAnUnknownRoot(t *testing.T) {
	fixture := newSetupFixture(t, stubRoots{})
	_, cookies := addedRootFolder(t, fixture)

	recorder := fixture.authed(t, http.MethodPatch, "/api/v1/rootfolders/999",
		`{"read_only":true,"allow_rename":false,"min_free_bytes":0}`, cookies)
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown root folder", recorder.Code)
	}
}

func TestSetRootFolderOnline_TogglesAndRequiresAdministrator(t *testing.T) {
	fixture := newSetupFixture(t, stubRoots{})
	id, cookies := addedRootFolder(t, fixture)

	unauthenticated := fixture.authed(t, http.MethodPatch, "/api/v1/rootfolders/"+itoa(id)+"/online",
		`{"online":false}`, nil)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unauthenticated declare", unauthenticated.Code)
	}

	recorder := fixture.authed(t, http.MethodPatch, "/api/v1/rootfolders/"+itoa(id)+"/online",
		`{"online":false}`, cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	offline, _ := fixture.cache.Snapshot().RootFolder(id)
	if offline.Online {
		t.Error("Online = true after declaring the root offline")
	}

	recorder = fixture.authed(t, http.MethodPatch, "/api/v1/rootfolders/"+itoa(id)+"/online",
		`{"online":true}`, cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	online, _ := fixture.cache.Snapshot().RootFolder(id)
	if !online.Online {
		t.Error("Online = false after declaring the root online again")
	}
}

func TestSetRootFolderOnline_RefusesAnUnknownRoot(t *testing.T) {
	fixture := newSetupFixture(t, stubRoots{})
	_, cookies := addedRootFolder(t, fixture)

	recorder := fixture.authed(t, http.MethodPatch, "/api/v1/rootfolders/999/online", `{"online":false}`, cookies)
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown root folder", recorder.Code)
	}
}

// A root folder attached to no category can simply be removed -- found live:
// there was no way to undo adding one, root-folder remove (Release checklist).
func TestDeleteRootFolder_RemovesIt(t *testing.T) {
	fixture := newSetupFixture(t, stubRoots{})
	id, cookies := addedRootFolder(t, fixture)

	recorder := fixture.authed(t, http.MethodDelete, "/api/v1/rootfolders/"+itoa(id), "", cookies)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if _, found := fixture.cache.Snapshot().RootFolder(id); found {
		t.Error("root folder still present in settings.db after delete")
	}
}

func TestDeleteRootFolder_RequiresAdministrator(t *testing.T) {
	fixture := newSetupFixture(t, stubRoots{})
	id, _ := addedRootFolder(t, fixture)

	recorder := fixture.authed(t, http.MethodDelete, "/api/v1/rootfolders/"+itoa(id), "", nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unauthenticated delete", recorder.Code)
	}
}

func TestDeleteRootFolder_MissingRootAnswers404(t *testing.T) {
	fixture := newSetupFixture(t, stubRoots{})
	_, cookies := addedRootFolder(t, fixture)

	recorder := fixture.authed(t, http.MethodDelete, "/api/v1/rootfolders/999", "", cookies)
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown root folder", recorder.Code)
	}
}

// A root folder a category still attaches must be refused, the same as
// deleting an in-use quality profile or category (settings.ErrInUse).
func TestDeleteRootFolder_RefusedWhileACategoryAttachesIt(t *testing.T) {
	fixture := newSetupFixture(t, stubRoots{})
	id, cookies := addedRootFolder(t, fixture)

	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 1)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if err := fixture.cache.AttachRootFolder(t.Context(), settings.CategoryRoot{
		CategoryID: category.ID, RootFolderID: id, AcceptsNew: true,
	}); err != nil {
		t.Fatalf("AttachRootFolder: %v", err)
	}

	recorder := fixture.authed(t, http.MethodDelete, "/api/v1/rootfolders/"+itoa(id), "", cookies)
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 while a category is still attached, body = %s",
			recorder.Code, recorder.Body)
	}
	if _, found := fixture.cache.Snapshot().RootFolder(id); !found {
		t.Error("root folder removed despite the refusal")
	}
}

// Placement refuses a root whose free space is unknown, so adding one records it at once
// rather than leaving an import to fail until the six-hourly sweep runs.
func TestAddRootFolder_RecordsCapacityImmediately(t *testing.T) {
	fixture := newSetupFixture(t, stubRoots{freeBytes: 900 << 30})
	claim := fixture.post(t, "/api/v1/setup/admin", `{"username":"avs","password":"correct horse battery"}`)
	if claim.Code != http.StatusCreated {
		t.Fatalf("claim: %d", claim.Code)
	}

	created := fixture.authed(t, http.MethodPost, "/api/v1/rootfolders", `{"path":"/library"}`, claim.Result().Cookies())
	if created.Code != http.StatusCreated {
		t.Fatalf("add root folder: %d (%s)", created.Code, created.Body)
	}
	var body struct {
		FreeBytes  *int64 `json:"free_bytes"`
		TotalBytes *int64 `json:"total_bytes"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body.FreeBytes == nil || *body.FreeBytes != 900<<30 || body.TotalBytes == nil {
		t.Errorf("free/total = %v/%v, want the capacity probed at creation", body.FreeBytes, body.TotalBytes)
	}
}
