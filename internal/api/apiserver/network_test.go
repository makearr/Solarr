package apiserver_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/settings"
)

type networkSettingsBody struct {
	Port            int      `json:"port"`
	InEffectPort    int      `json:"in_effect_port"`
	RestartRequired bool     `json:"restart_required"`
	ProxyURL        *string  `json:"proxy_url,omitempty"`
	ProxyBypass     []string `json:"proxy_bypass"`
	AllowCIDRs      []string `json:"allow_cidrs"`
}

func TestGetNetworkSettings_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(httptest.NewRequest(http.MethodGet, "/api/v1/network", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestGetNetworkSettings_DefaultsToNothingConfigured(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/network"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body networkSettingsBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Port != 0 || body.RestartRequired || len(body.ProxyBypass) != 0 || len(body.AllowCIDRs) != 0 {
		t.Errorf("body = %+v, want the zero value", body)
	}
}

func TestUpdateNetworkSettings_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	request := authedPatch(t, cookies, "/api/v1/network", `{"port":6000,"proxy_bypass":[],"allow_cidrs":[]}`)
	recorder := fixture.do(request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

// httptest's default RemoteAddr (192.0.2.1) keeps this write past the lockout guard (Inv. 89).
func TestUpdateNetworkSettings_RoundTripsAndReportsRestartRequired(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	request := authedPatch(t, cookies, "/api/v1/network",
		`{"port":6000,"proxy_url":"socks5://10.0.0.5:1080","proxy_bypass":[".lan"],"allow_cidrs":["192.0.2.0/24"]}`)
	recorder := fixture.do(request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	var body networkSettingsBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The fixture's server was built with Port: 0, so a stored port always disagrees -- restart pending.
	if body.Port != 6000 || !body.RestartRequired {
		t.Errorf("body = %+v, want port 6000 and restart_required", body)
	}
	if body.ProxyURL == nil || *body.ProxyURL != "socks5://10.0.0.5:1080" {
		t.Errorf("ProxyURL = %v, want the socks5 URL", body.ProxyURL)
	}
	if len(body.ProxyBypass) != 1 || len(body.AllowCIDRs) != 1 {
		t.Errorf("body = %+v", body)
	}
}

// Invariant 89 at the HTTP layer: a write that would exclude the caller's
// own resolved address is refused, not silently applied.
func TestUpdateNetworkSettings_RefusesAnAllowlistThatWouldLockOutTheCaller(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	// httptest.NewRequest's default RemoteAddr is 192.0.2.1, excluded here.
	request := authedPatch(t, cookies, "/api/v1/network", `{"port":0,"proxy_bypass":[],"allow_cidrs":["10.0.0.0/8"]}`)
	recorder := fixture.do(request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want 400", recorder.Code, recorder.Body)
	}
	if got := fixture.cache.Snapshot().NetworkSettings().AllowCIDRs; len(got) != 0 {
		t.Errorf("AllowCIDRs = %v, want unchanged (empty)", got)
	}
}

func TestUpdateNetworkSettings_RejectsAMalformedBody(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	request := authedPatch(t, cookies, "/api/v1/network", `not json`)
	recorder := fixture.do(request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

// ADR-26 (todo 4.14): "a setting not in the registry cannot be written."
// An empty registry stands in for a write path that forgot to register its
// own key -- this is the mutation the Registry.LookupAll check in
// UpdateNetworkSettings guards against.
func TestUpdateNetworkSettings_RefusesWhenNetworkKeysAreNotRegistered(t *testing.T) {
	fixture := newWorksFixtureWithRegistry(t, settings.NewRegistry())
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	request := authedPatch(t, cookies, "/api/v1/network", `{"port":6000,"proxy_bypass":[],"allow_cidrs":[]}`)
	recorder := fixture.do(request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s, want 500", recorder.Code, recorder.Body)
	}
	if got := fixture.cache.Snapshot().NetworkSettings().Port; got != 0 {
		t.Errorf("Port = %d, want unchanged (nothing should have been written)", got)
	}
}

// ipAllowlist runs before routing, so it refuses even an unauthenticated route.
func TestIPAllowlist_RefusesAnExcludedAddressEvenOnAnUnauthenticatedRoute(t *testing.T) {
	fixture := newWorksFixture(t)
	// Set directly with a caller address the list includes, past the lockout guard tested elsewhere.
	err := fixture.cache.SetNetworkSettings(t.Context(), fixture.clock,
		settings.NetworkSettings{AllowCIDRs: []string{"10.0.0.0/8"}}, netip.MustParseAddr("10.0.0.1"))
	if err != nil {
		t.Fatalf("SetNetworkSettings: %v", err)
	}

	// httptest.NewRequest's default RemoteAddr, 192.0.2.1, is not in 10.0.0.0/8.
	recorder := fixture.do(httptest.NewRequest(http.MethodGet, "/api/v1/about", nil))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s, want 403", recorder.Code, recorder.Body)
	}
}

func TestIPAllowlist_HealthIsAlwaysExempt(t *testing.T) {
	fixture := newWorksFixture(t)
	err := fixture.cache.SetNetworkSettings(t.Context(), fixture.clock,
		settings.NetworkSettings{AllowCIDRs: []string{"10.0.0.0/8"}}, netip.MustParseAddr("10.0.0.1"))
	if err != nil {
		t.Fatalf("SetNetworkSettings: %v", err)
	}

	recorder := fixture.do(httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200 -- health must stay reachable", recorder.Code, recorder.Body)
	}
}

func TestIPAllowlist_AllowsAMatchingAddress(t *testing.T) {
	fixture := newWorksFixture(t)
	err := fixture.cache.SetNetworkSettings(t.Context(), fixture.clock,
		settings.NetworkSettings{AllowCIDRs: []string{"192.0.2.0/24"}}, netip.MustParseAddr("192.0.2.1"))
	if err != nil {
		t.Fatalf("SetNetworkSettings: %v", err)
	}

	recorder := fixture.do(httptest.NewRequest(http.MethodGet, "/api/v1/about", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", recorder.Code, recorder.Body)
	}
}

func TestIPAllowlist_EmptyListAllowsEverything(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(httptest.NewRequest(http.MethodGet, "/api/v1/about", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty allowlist means allow all)", recorder.Code)
	}
}

// Invariant 89: an allowed address still must authenticate for a secured route.
func TestIPAllowlist_NeverGrantsOrAuthenticates(t *testing.T) {
	fixture := newWorksFixture(t)
	err := fixture.cache.SetNetworkSettings(t.Context(), fixture.clock,
		settings.NetworkSettings{AllowCIDRs: []string{"192.0.2.0/24"}}, netip.MustParseAddr("192.0.2.1"))
	if err != nil {
		t.Fatalf("SetNetworkSettings: %v", err)
	}

	recorder := fixture.do(httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 -- an allowed address still needs a credential", recorder.Code)
	}
}

// ADR-14/58: X-Forwarded-For is believed only from a trusted proxy peer, or the allowlist decorates (Inv. 89).
func TestIPAllowlist_TrustsForwardedForOnlyBehindATrustedProxy(t *testing.T) {
	fixture := newWorksFixture(t)
	if err := fixture.cache.SetNetworkSettings(t.Context(), fixture.clock,
		settings.NetworkSettings{AllowCIDRs: []string{"10.0.0.0/8"}}, netip.MustParseAddr("10.0.0.1")); err != nil {
		t.Fatalf("SetNetworkSettings: %v", err)
	}

	trustedProxy := netip.MustParsePrefix("192.0.2.0/24") // httptest's own default peer, 192.0.2.1
	server, err := apiserver.New(apiserver.Config{
		Bind: "127.0.0.1", Port: 0, BaseURL: "/",
		Checker: fakeChecker{},
		Clock:   fixture.clock,
		Logger:  slog.New(slog.DiscardHandler),
		Auth: apiserver.AuthConfig{
			Mode: apiserver.AuthLocal, Settings: fixture.cache, Clock: fixture.clock,
			ProxyNetworks: []netip.Prefix{trustedProxy},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The socket peer (192.0.2.1) is trusted, so the forwarded address is believed and refused.
	forwarded := httptest.NewRequest(http.MethodGet, "/api/v1/about", nil)
	forwarded.Header.Set("X-Forwarded-For", "203.0.113.9")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, forwarded)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s, want 403 for the forwarded address", recorder.Code, recorder.Body)
	}

	// An untrusted peer's own forwarded claim must be ignored; its socket address governs instead.
	untrustedProxy := httptest.NewRequest(http.MethodGet, "/api/v1/about", nil)
	untrustedProxy.RemoteAddr = "198.51.100.5:4321"
	untrustedProxy.Header.Set("X-Forwarded-For", "10.0.0.9")
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, untrustedProxy)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s, want 403 -- an untrusted peer's forwarded claim must be ignored",
			recorder.Code, recorder.Body)
	}
}
