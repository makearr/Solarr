package httpx_test

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/makearr/MediaNexus/internal/platform/httpx"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return parsed
}

func mustNewRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("NewRequest(%q): %v", target, err)
	}
	return request
}

func TestProxyFunc_UsesTheConfiguredProxyByDefault(t *testing.T) {
	proxyURL := mustParseURL(t, "socks5://10.0.0.5:1080")
	proxyFunc := httpx.ProxyFunc(func() httpx.ProxyPolicy { return httpx.ProxyPolicy{URL: proxyURL} })

	got, err := proxyFunc(mustNewRequest(t, "https://musicbrainz.org/ws/2/release"))
	if err != nil {
		t.Fatalf("proxyFunc: %v", err)
	}
	if got != proxyURL {
		t.Errorf("proxy = %v, want %v", got, proxyURL)
	}
}

func TestProxyFunc_NoPolicyMeansDirect(t *testing.T) {
	proxyFunc := httpx.ProxyFunc(func() httpx.ProxyPolicy { return httpx.ProxyPolicy{} })

	got, err := proxyFunc(mustNewRequest(t, "https://musicbrainz.org/ws/2/release"))
	if err != nil {
		t.Fatalf("proxyFunc: %v", err)
	}
	if got != nil {
		t.Errorf("proxy = %v, want nil (direct)", got)
	}
}

func TestProxyFunc_BypassesAMatchingCIDR(t *testing.T) {
	proxyURL := mustParseURL(t, "http://10.0.0.5:3128")
	proxyFunc := httpx.ProxyFunc(func() httpx.ProxyPolicy {
		return httpx.ProxyPolicy{URL: proxyURL, Bypass: []string{"192.168.0.0/16"}}
	})

	got, err := proxyFunc(mustNewRequest(t, "http://192.168.1.50:8080/metadata"))
	if err != nil {
		t.Fatalf("proxyFunc: %v", err)
	}
	if got != nil {
		t.Errorf("proxy = %v, want nil (bypassed)", got)
	}
}

func TestProxyFunc_BypassesAMatchingHostSuffix(t *testing.T) {
	proxyURL := mustParseURL(t, "http://10.0.0.5:3128")
	proxyFunc := httpx.ProxyFunc(func() httpx.ProxyPolicy {
		return httpx.ProxyPolicy{URL: proxyURL, Bypass: []string{".lan"}}
	})

	got, err := proxyFunc(mustNewRequest(t, "http://metadata-mirror.lan/api"))
	if err != nil {
		t.Fatalf("proxyFunc: %v", err)
	}
	if got != nil {
		t.Errorf("proxy = %v, want nil (bypassed by suffix)", got)
	}
}

func TestProxyFunc_HostSuffixDoesNotMatchAnUnrelatedHost(t *testing.T) {
	proxyURL := mustParseURL(t, "http://10.0.0.5:3128")
	proxyFunc := httpx.ProxyFunc(func() httpx.ProxyPolicy {
		return httpx.ProxyPolicy{URL: proxyURL, Bypass: []string{".lan"}}
	})

	got, err := proxyFunc(mustNewRequest(t, "https://musicbrainz.org/ws/2/release"))
	if err != nil {
		t.Fatalf("proxyFunc: %v", err)
	}
	if got != proxyURL {
		t.Errorf("proxy = %v, want %v (not bypassed)", got, proxyURL)
	}
}

// Invariant 65: a dial to the configured proxy skips classification; a direct destination does not.
func TestProxyDial_SkipsClassificationOnlyForTheConfiguredProxy(t *testing.T) {
	proxyURL := mustParseURL(t, "socks5://192.168.1.50:1080")
	policy := func() httpx.ProxyPolicy { return httpx.ProxyPolicy{URL: proxyURL} }
	guard := httpx.NewGuard(fakeResolver{}, refusingDial, nil)

	dial := httpx.ProxyDial(guard, policy)

	// Dialing the configured proxy's own address must not be blocked even
	// though it is a private address.
	var dialled string
	guardWithRecorder := httpx.NewGuard(fakeResolver{}, recordingDial(&dialled), nil)
	dialToProxy := httpx.ProxyDial(guardWithRecorder, policy)
	if _, err := dialToProxy(t.Context(), "tcp", "192.168.1.50:1080"); err != nil {
		t.Fatalf("dial to the configured proxy: %v", err)
	}
	if dialled != "192.168.1.50:1080" {
		t.Errorf("dialled %q, want the proxy address", dialled)
	}

	// Dialing anything else -- a bypassed request's own direct connection to
	// its real destination -- still goes through the full guard and is
	// blocked exactly as it would be with no proxy configured at all.
	if _, err := dial(t.Context(), "tcp", "10.1.2.3:443"); !errors.Is(err, httpx.ErrBlockedAddress) {
		t.Errorf("dial to a non-proxy private address = %v, want ErrBlockedAddress", err)
	}
}

func TestProxyDial_NoPolicyBehavesLikeTheGuardAlone(t *testing.T) {
	guard := httpx.NewGuard(fakeResolver{}, refusingDial, nil)
	dial := httpx.ProxyDial(guard, func() httpx.ProxyPolicy { return httpx.ProxyPolicy{} })

	if _, err := dial(t.Context(), "tcp", "10.1.2.3:443"); !errors.Is(err, httpx.ErrBlockedAddress) {
		t.Errorf("dial = %v, want ErrBlockedAddress", err)
	}
}
