package apiserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/auth"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/events"
	"github.com/makearr/MediaNexus/internal/settings"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/migrations"
)

type authFixture struct {
	server    *apiserver.Server
	cache     *settings.Cache
	bus       *events.Bus
	clock     *clock.Fake
	admin     settings.User
	housemate settings.User
	music     settings.Category
	adult     settings.Category
}

const testPassword = "correct horse battery staple"

func newAuthFixture(t *testing.T, mode apiserver.AuthMode, networks ...netip.Prefix) authFixture {
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

	hashed, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	admin, err := cache.CreateUser(t.Context(), fake, "avs", hashed, settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser(admin): %v", err)
	}
	housemate, err := cache.CreateUser(t.Context(), fake, "housemate", hashed, settings.RoleReadonly)
	if err != nil {
		t.Fatalf("CreateUser(housemate): %v", err)
	}
	music, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	adult, err := cache.CreateCategory(t.Context(), "adult", "Adult", 90)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if err := cache.SetCategoryAccess(t.Context(), housemate.ID, music.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}

	bus := events.NewBus(fake)
	server, err := apiserver.New(apiserver.Config{
		Bind: "127.0.0.1", Port: 0, BaseURL: "/",
		Checker: fakeChecker{},
		Events:  bus,
		Clock:   fake,
		Logger:  slog.New(slog.DiscardHandler),
		Auth: apiserver.AuthConfig{
			Mode:          mode,
			Sessions:      auth.NewSessions([]byte("0123456789abcdef0123456789abcdef"), fake, time.Hour),
			Settings:      cache,
			Clock:         fake,
			ProxyHeader:   "X-Remote-User",
			ProxyNetworks: networks,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return authFixture{server: server, cache: cache, bus: bus, clock: fake,
		admin: admin, housemate: housemate, music: music, adult: adult}
}

func (f authFixture) do(t *testing.T, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func (f authFixture) login(t *testing.T, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	return f.do(t, request)
}

func TestAuth_UnauthenticatedRequestsAreRefused(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)

	response := fixture.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if response.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", response.Code)
	}
}

func TestAuth_LoginIssuesAWorkingSession(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)

	response := fixture.login(t, "avs", testPassword)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", response.Code)
	}

	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.SessionCookieName {
		t.Fatalf("login set %v, want a session cookie", cookies)
	}
	if !cookies[0].HttpOnly {
		t.Error("the session cookie is readable by JavaScript")
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.AddCookie(cookies[0])
	if got := fixture.do(t, request).Code; got != http.StatusOK {
		t.Errorf("authenticated request = %d, want 200", got)
	}
}

// Distinguishing "no such user" from "wrong password" turns the login form into
// an account enumerator.
func TestAuth_LoginFailuresAreIndistinguishable(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)

	unknown := fixture.login(t, "nobody", testPassword)
	wrong := fixture.login(t, "avs", "not the password")

	if unknown.Code != http.StatusUnauthorized || wrong.Code != http.StatusUnauthorized {
		t.Fatalf("statuses were %d and %d, want both 401", unknown.Code, wrong.Code)
	}
	if unknown.Body.String() != wrong.Body.String() {
		t.Errorf("responses differ:\n unknown user:   %s\n wrong password: %s",
			unknown.Body.String(), wrong.Body.String())
	}
}

func TestAuth_LogoutClearsTheCookie(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)

	response := fixture.do(t, httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Errorf("logout set %v, want an expiring session cookie", cookies)
	}
}

func TestAuth_APIKeyAuthenticates(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)

	key, hash, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := fixture.cache.CreateAPIKey(t.Context(), fixture.clock, fixture.admin.ID, "scripts", hash); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.Header.Set(apiserver.APIKeyHeader, key.Reveal())
	if got := fixture.do(t, request).Code; got != http.StatusOK {
		t.Errorf("api key request = %d, want 200", got)
	}
}

func TestAuth_UnknownAPIKeyIsRefused(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)

	other, _, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.Header.Set(apiserver.APIKeyHeader, other.Reveal())

	if got := fixture.do(t, request).Code; got != http.StatusUnauthorized {
		t.Errorf("unknown api key = %d, want 401", got)
	}
}

// Trusting the header without knowing who may set it lets anyone claim to be
// anyone, which is why it is only believed from a configured network.
func TestAuth_ProxyHeaderIsOnlyBelievedFromATrustedNetwork(t *testing.T) {
	trusted := netip.MustParsePrefix("10.0.0.0/8")
	fixture := newAuthFixture(t, apiserver.AuthProxy, trusted)

	fromProxy := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	fromProxy.RemoteAddr = "10.1.2.3:44444"
	fromProxy.Header.Set("X-Remote-User", "avs")
	if got := fixture.do(t, fromProxy).Code; got != http.StatusOK {
		t.Errorf("request from the trusted proxy = %d, want 200", got)
	}

	fromElsewhere := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	fromElsewhere.RemoteAddr = "203.0.113.9:44444"
	fromElsewhere.Header.Set("X-Remote-User", "avs")
	if got := fixture.do(t, fromElsewhere).Code; got != http.StatusUnauthorized {
		t.Errorf("forged header from outside = %d, want 401", got)
	}
}

func TestAuth_ProxyHeaderIsIgnoredInLocalMode(t *testing.T) {
	trusted := netip.MustParsePrefix("10.0.0.0/8")
	fixture := newAuthFixture(t, apiserver.AuthLocal, trusted)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.RemoteAddr = "10.1.2.3:44444"
	request.Header.Set("X-Remote-User", "avs")

	if got := fixture.do(t, request).Code; got != http.StatusUnauthorized {
		t.Errorf("proxy header in local mode = %d, want 401", got)
	}
}

func TestAuth_LoginIsDisabledInAPIKeyOnlyMode(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthAPIKeyOnly)

	if got := fixture.login(t, "avs", testPassword).Code; got != http.StatusForbidden {
		t.Errorf("login in apikey_only mode = %d, want 403", got)
	}
}

// The endpoint a client uses to discover its own permissions, rather than
// guessing from failed requests.
func TestMe_ReportsOnlyVisibleCategories(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)

	cookies := fixture.login(t, "housemate", testPassword).Result().Cookies()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.AddCookie(cookies[0])
	response := fixture.do(t, request)

	var body struct {
		UserID     int64 `json:"user_id"`
		Categories []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Permission string `json:"permission"`
		} `json:"categories"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.UserID != fixture.housemate.ID {
		t.Errorf("user_id = %d, want %d", body.UserID, fixture.housemate.ID)
	}
	if len(body.Categories) != 1 || body.Categories[0].ID != fixture.music.ID {
		t.Fatalf("categories = %+v, want only Music", body.Categories)
	}
	if body.Categories[0].Permission != "read" {
		t.Errorf("permission = %q, want read", body.Categories[0].Permission)
	}
}

func TestMe_AdminSeesEveryCategory(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)

	cookies := fixture.login(t, "avs", testPassword).Result().Cookies()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.AddCookie(cookies[0])

	var body struct {
		Categories []struct {
			Permission string `json:"permission"`
		} `json:"categories"`
	}
	if err := json.NewDecoder(fixture.do(t, request).Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Categories) != 2 {
		t.Fatalf("admin sees %d categories, want 2", len(body.Categories))
	}
	for _, category := range body.Categories {
		if category.Permission != "manage" {
			t.Errorf("admin permission = %q, want manage", category.Permission)
		}
	}
}

func TestAuth_HealthAndAboutStayUnauthenticated(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	fixture.server.MarkReady()

	for _, path := range []string{"/api/v1/health", "/api/v1/ready", "/api/v1/about"} {
		if got := fixture.do(t, httptest.NewRequest(http.MethodGet, path, nil)).Code; got != http.StatusOK {
			t.Errorf("%s = %d without credentials, want 200", path, got)
		}
	}
}

func TestAuth_ExpiredSessionIsRefused(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	cookies := fixture.login(t, "avs", testPassword).Result().Cookies()

	fixture.clock.Advance(2 * time.Hour)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.AddCookie(cookies[0])
	if got := fixture.do(t, request).Code; got != http.StatusUnauthorized {
		t.Errorf("expired session = %d, want 401", got)
	}
}

func TestAuth_SessionForADeletedUserIsRefused(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	cookies := fixture.login(t, "housemate", testPassword).Result().Cookies()

	if err := fixture.cache.DeleteUser(context.Background(), fixture.housemate.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// The signature is still valid; the user is not.
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.AddCookie(cookies[0])
	if got := fixture.do(t, request).Code; got != http.StatusUnauthorized {
		t.Errorf("session for a deleted user = %d, want 401", got)
	}
}

func TestJobs_RejectsABadCursor(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	cookies := fixture.login(t, "avs", testPassword).Result().Cookies()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?cursor=not-a-cursor", nil)
	request.AddCookie(cookies[0])

	// Validated before the queue's availability is considered, so a client is
	// told what is actually wrong with its request.
	if got := fixture.do(t, request).Code; got != http.StatusBadRequest {
		t.Errorf("bad cursor = %d, want 400", got)
	}
}

func TestJobs_RejectsAnUnknownState(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	cookies := fixture.login(t, "avs", testPassword).Result().Cookies()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?state=SLEEPING", nil)
	request.AddCookie(cookies[0])

	// Silently returning nothing would look like an empty queue.
	if got := fixture.do(t, request).Code; got != http.StatusBadRequest {
		t.Errorf("unknown state = %d, want 400", got)
	}
}

func TestJobs_RequiresAuthentication(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	if got := fixture.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)).Code; got != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", got)
	}
}
