package apiserver_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/auth"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/events"
	"github.com/makearr/Solarr/migrations"
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
			Transport:     testTransport(t),
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

// Self-service (ADR-26): any authenticated user, not just an administrator,
// may change their own tier -- it only changes what is displayed. The
// response itself must already carry the new tier, not what Access looked
// like when the request began (the exact staleness class the category-
// editing endpoint hit live, 2026-09-12) -- checked directly rather than
// trusting a follow-up GET /me to prove the PATCH handler itself is right.
func TestSetMyTier_TheResponseItselfReflectsTheChange(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	cookies := fixture.login(t, "housemate", testPassword).Result().Cookies()

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/me/tier", strings.NewReader(`{"tier":"expert"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookies[0])

	var body struct {
		Tier string `json:"tier"`
	}
	if err := json.NewDecoder(fixture.do(t, request).Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Tier != "expert" {
		t.Fatalf("PATCH /me/tier response tier = %q, want expert", body.Tier)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	getRequest.AddCookie(cookies[0])
	var getBody struct {
		Tier string `json:"tier"`
	}
	if err := json.NewDecoder(fixture.do(t, getRequest).Body).Decode(&getBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if getBody.Tier != "expert" {
		t.Errorf("a follow-up GET /me tier = %q, want expert", getBody.Tier)
	}
}

func TestSetMyTier_InvalidTierAnswers400(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	cookies := fixture.login(t, "housemate", testPassword).Result().Cookies()

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/me/tier", strings.NewReader(`{"tier":"godmode"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookies[0])

	if response := fixture.do(t, request); response.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an invalid tier", response.Code)
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

// The mechanism (settings.Cache.CreateAPIKey/ListAPIKeys/UserByAPIKeyHash)
// existed and was tested; these cover the HTTP surface that finally reaches
// it (todo: expose per-user API keys for upstream apps).
func TestCreateMyAPIKey_ReturnsThePlaintextOnceAndItAuthenticates(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	cookies := fixture.login(t, "avs", testPassword).Result().Cookies()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/me/apikeys",
		bytes.NewBufferString(`{"name":"Prowlarr"}`))
	request.AddCookie(cookies[0])
	recorder := fixture.do(t, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	var body struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Name != "Prowlarr" || body.Key == "" {
		t.Fatalf("body = %+v, want name=Prowlarr and a non-empty key", body)
	}

	// The plaintext just returned actually works as a credential.
	authRequest := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	authRequest.Header.Set(apiserver.APIKeyHeader, body.Key)
	if got := fixture.do(t, authRequest).Code; got != http.StatusOK {
		t.Errorf("authenticating with the newly created key = %d, want 200", got)
	}
}

func TestCreateMyAPIKey_EmptyNameAnswers400(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	cookies := fixture.login(t, "avs", testPassword).Result().Cookies()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/me/apikeys", bytes.NewBufferString(`{"name":""}`))
	request.AddCookie(cookies[0])
	if got := fixture.do(t, request).Code; got != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an empty name", got)
	}
}

func TestListMyAPIKeys_NeverIncludesTheKeyOrHash(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	_, hash, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := fixture.cache.CreateAPIKey(t.Context(), fixture.clock, fixture.admin.ID, "scripts", hash); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	cookies := fixture.login(t, "avs", testPassword).Result().Cookies()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me/apikeys", nil)
	request.AddCookie(cookies[0])
	recorder := fixture.do(t, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if strings.Contains(recorder.Body.String(), hash) {
		t.Error("response body carries the key's own hash")
	}

	var body []map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 1 || body[0]["name"] != "scripts" {
		t.Fatalf("body = %+v, want one key named scripts", body)
	}
	if _, hasKey := body[0]["key"]; hasKey {
		t.Error("list response carries a key field -- the plaintext must never be re-shown")
	}
}

func TestDeleteMyAPIKey_RemovesIt(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	_, hash, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	created, err := fixture.cache.CreateAPIKey(t.Context(), fixture.clock, fixture.admin.ID, "scripts", hash)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	cookies := fixture.login(t, "avs", testPassword).Result().Cookies()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/me/apikeys/"+itoa(created.ID), nil)
	request.AddCookie(cookies[0])
	if got := fixture.do(t, request).Code; got != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", got)
	}

	remaining, err := fixture.cache.ListAPIKeys(t.Context(), fixture.admin.ID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("keys remaining = %+v, want none", remaining)
	}
}

// The real guard: settings.Cache.DeleteAPIKey has to check ownership itself,
// since the underlying query deletes by a bare id (mutation-tested in
// TestDeleteAPIKey_RefusesAnotherUsersKey, internal/db/settings).
func TestDeleteMyAPIKey_RefusesAnotherUsersKey(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	_, hash, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	adminsKey, err := fixture.cache.CreateAPIKey(t.Context(), fixture.clock, fixture.admin.ID, "admin-only", hash)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	cookies := fixture.login(t, "housemate", testPassword).Result().Cookies()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/me/apikeys/"+itoa(adminsKey.ID), nil)
	request.AddCookie(cookies[0])
	if got := fixture.do(t, request).Code; got != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for another user's key", got)
	}

	remaining, err := fixture.cache.ListAPIKeys(t.Context(), fixture.admin.ID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(remaining) != 1 {
		t.Error("the admin's key was removed despite the refusal")
	}
}

// A new password ends every session from before it; the browser that set it stays signed in.
func TestAuth_ChangingAPasswordEndsTheOtherSessions(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	elsewhere := fixture.login(t, "avs", testPassword).Result().Cookies()[0]
	here := fixture.login(t, "avs", testPassword).Result().Cookies()[0]
	housemate := fixture.login(t, "housemate", testPassword).Result().Cookies()[0]

	body := strings.NewReader(`{"password":"a new and long passphrase"}`)
	change := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/users/%d", fixture.admin.ID), body)
	change.AddCookie(here)
	response := fixture.do(t, change)
	if response.Code != http.StatusOK {
		t.Fatalf("password change = %d %s", response.Code, response.Body.String())
	}
	renewed := response.Result().Cookies()
	if len(renewed) != 1 || renewed[0].Name != auth.SessionCookieName {
		t.Fatalf("the changing browser got %v, want a new session cookie", renewed)
	}
	me := func(cookie *http.Cookie) int {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		request.AddCookie(cookie)
		return fixture.do(t, request).Code
	}
	if got := []int{me(elsewhere), me(here), me(renewed[0]), me(housemate)}; !slices.Equal(got, []int{401, 401, 200, 200}) {
		t.Errorf("other session, old cookie here, renewed cookie, another account = %v; want 401 401 200 200", got)
	}
}

// Five wrong passwords from one address make the sixth try wait, even with the right password.
func TestAuth_FailedLoginsFromOneAddressAreLimited(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	for range auth.LoginAttempts {
		if got := fixture.login(t, "avs", "not the password").Code; got != http.StatusUnauthorized {
			t.Fatalf("wrong password = %d, want 401", got)
		}
	}
	limited := fixture.login(t, "avs", testPassword)
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("sixth try = %d (Retry-After %q), want 429 with Retry-After", limited.Code, limited.Header().Get("Retry-After"))
	}
	fixture.clock.Advance(auth.LoginWindow)
	if got := fixture.login(t, "avs", testPassword).Code; got != http.StatusOK {
		t.Errorf("a minute later = %d, want 200", got)
	}
}

func testTransport(t *testing.T) *auth.Transport {
	t.Helper()
	transport, err := auth.NewTransport([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

// seal encrypts a value for the server as the browser does (web/src/api/seal.ts).
func seal(t *testing.T, serverPublic, plain string) string {
	t.Helper()
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicBytes, _ := base64.StdEncoding.DecodeString(serverPublic)
	server, err := ecdh.X25519().NewPublicKey(publicBytes)
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := ephemeral.ECDH(server)
	key, _ := hkdf.Key(sha256.New, shared, nil, "solarr:transport-seal:v1", 32)
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, aead.NonceSize())
	_, _ = rand.Read(nonce)
	encode := base64.StdEncoding.EncodeToString
	return auth.SealedPrefix + encode(ephemeral.PublicKey().Bytes()) + ":" + encode(nonce) + ":" + encode(aead.Seal(nil, nonce, []byte(plain), nil))
}

// A password sealed in the browser signs in like a plain one; a seal that does not open is refused, not passed on.
func TestAuth_LoginAcceptsAPasswordSealedInTheBrowser(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	keyResponse := fixture.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/auth/transport-key", nil))
	var key struct {
		PublicKey string `json:"public_key"`
	}
	if keyResponse.Code != http.StatusOK || json.Unmarshal(keyResponse.Body.Bytes(), &key) != nil || key.PublicKey == "" {
		t.Fatalf("transport key = %d %s", keyResponse.Code, keyResponse.Body.String())
	}
	login := func(password string) int {
		body, _ := json.Marshal(map[string]any{"username": "avs", "password": password, "remember": false})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		return fixture.do(t, request).Code
	}
	sealed := seal(t, key.PublicKey, testPassword)
	if got := []int{login(sealed), login(sealed[:len(sealed)-6] + "AAAAAA"), login(testPassword)}; !slices.Equal(got, []int{200, 400, 200}) {
		t.Errorf("sealed, tampered, plain = %v; want 200 400 200", got)
	}
}
