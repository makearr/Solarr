package settings_test

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestNetworkSettings_DefaultsWhenNothingIsStored(t *testing.T) {
	cache, _ := newCache(t)
	network := cache.Snapshot().NetworkSettings()
	want := settings.NetworkSettings{
		ProxyBypass: settings.DefaultLocalRanges, AllowCIDRs: settings.DefaultLocalRanges,
	}
	if network.Port != want.Port || network.ProxyURL != want.ProxyURL ||
		network.ProxyEnabled || network.AllowCIDRsEnabled ||
		!slices.Equal(network.ProxyBypass, want.ProxyBypass) ||
		!slices.Equal(network.AllowCIDRs, want.AllowCIDRs) {
		t.Errorf("NetworkSettings = %+v, want %+v with both switches off", network, want)
	}
}

func TestNetworkSettings_AnEmptiedProxyBypassStaysEmpty(t *testing.T) {
	cache, cl := newCache(t)
	stored := settings.NetworkSettings{ProxyBypass: []string{}}
	if err := cache.SetNetworkSettings(t.Context(), cl, stored, netip.MustParseAddr("10.0.0.1")); err != nil {
		t.Fatalf("SetNetworkSettings: %v", err)
	}
	if got := cache.Snapshot().NetworkSettings().ProxyBypass; len(got) != 0 {
		t.Errorf("ProxyBypass = %v, want the list the user emptied, not the default", got)
	}
}

func TestSetNetworkSettings_RoundTrips(t *testing.T) {
	cache, cl := newCache(t)
	want := settings.NetworkSettings{
		Port: 6000, ProxyEnabled: true, ProxyURL: "socks5://10.0.0.5:1080",
		ProxyBypass:       []string{"10.0.0.0/8", ".lan"},
		AllowCIDRsEnabled: true,
		AllowCIDRs:        []string{"10.0.0.0/8", "192.168.1.10/32"},
	}
	caller := netip.MustParseAddr("10.0.0.1")
	if err := cache.SetNetworkSettings(t.Context(), cl, want, caller); err != nil {
		t.Fatalf("SetNetworkSettings: %v", err)
	}
	if got := cache.Snapshot().NetworkSettings(); got.Port != want.Port || got.ProxyURL != want.ProxyURL ||
		!got.ProxyEnabled || !got.AllowCIDRsEnabled ||
		len(got.ProxyBypass) != 2 || len(got.AllowCIDRs) != 2 {
		t.Errorf("NetworkSettings = %+v, want %+v", got, want)
	}
}

func TestSetNetworkSettings_RejectsAnOutOfRangePort(t *testing.T) {
	cache, cl := newCache(t)
	err := cache.SetNetworkSettings(t.Context(), cl, settings.NetworkSettings{Port: 70000}, netip.Addr{})
	if err == nil {
		t.Fatal("a port above 65535 was accepted")
	}
}

func TestSetNetworkSettings_ZeroPortClearsTheOverride(t *testing.T) {
	cache, cl := newCache(t)
	if err := cache.SetNetworkSettings(t.Context(), cl, settings.NetworkSettings{Port: 6000}, netip.Addr{}); err != nil {
		t.Fatalf("SetNetworkSettings: %v", err)
	}
	if err := cache.SetNetworkSettings(t.Context(), cl, settings.NetworkSettings{Port: 0}, netip.Addr{}); err != nil {
		t.Fatalf("SetNetworkSettings: %v", err)
	}
	if got := cache.Snapshot().NetworkSettings().Port; got != 0 {
		t.Errorf("Port = %d, want 0 (unset) after clearing", got)
	}
}

func TestSetNetworkSettings_RejectsAProxyURLWithoutAnExplicitPort(t *testing.T) {
	cache, cl := newCache(t)
	err := cache.SetNetworkSettings(t.Context(), cl, settings.NetworkSettings{ProxyURL: "socks5://proxy.lan"}, netip.Addr{})
	if err == nil {
		t.Fatal("a proxy_url with no port was accepted")
	}
}

func TestSetNetworkSettings_RejectsAnUnsupportedProxyScheme(t *testing.T) {
	cache, cl := newCache(t)
	err := cache.SetNetworkSettings(t.Context(), cl, settings.NetworkSettings{ProxyURL: "ftp://proxy.lan:21"}, netip.Addr{})
	if err == nil {
		t.Fatal("an ftp:// proxy_url was accepted")
	}
}

func TestSetNetworkSettings_RejectsAMalformedAllowCIDR(t *testing.T) {
	cache, cl := newCache(t)
	err := cache.SetNetworkSettings(t.Context(), cl, settings.NetworkSettings{AllowCIDRs: []string{"not-a-cidr"}}, netip.Addr{})
	if err == nil {
		t.Fatal("a malformed CIDR was accepted")
	}
}

func TestSetNetworkSettings_RejectsAnEmptyProxyBypassEntry(t *testing.T) {
	cache, cl := newCache(t)
	err := cache.SetNetworkSettings(t.Context(), cl, settings.NetworkSettings{ProxyBypass: []string{"  "}}, netip.Addr{})
	if err == nil {
		t.Fatal("a blank proxy_bypass entry was accepted")
	}
}

// Invariant 89's own guard: a write excluding its own caller is refused.
func TestSetNetworkSettings_RefusesAnAllowlistThatWouldLockOutTheCaller(t *testing.T) {
	cache, cl := newCache(t)
	caller := netip.MustParseAddr("203.0.113.7")
	err := cache.SetNetworkSettings(t.Context(), cl,
		settings.NetworkSettings{AllowCIDRsEnabled: true, AllowCIDRs: []string{"10.0.0.0/8"}}, caller)
	if err == nil {
		t.Fatal("an enforced whitelist excluding the caller's own address was accepted")
	}
}

func TestSetNetworkSettings_AcceptsAnAllowlistThatIncludesTheCaller(t *testing.T) {
	cache, cl := newCache(t)
	caller := netip.MustParseAddr("10.1.2.3")
	err := cache.SetNetworkSettings(t.Context(), cl,
		settings.NetworkSettings{AllowCIDRsEnabled: true, AllowCIDRs: []string{"10.0.0.0/8"}}, caller)
	if err != nil {
		t.Fatalf("SetNetworkSettings: %v", err)
	}
}

// An unverifiable caller address is refused too -- it cannot be proven safe.
func TestSetNetworkSettings_RefusesWhenTheCallerAddressCannotBeVerified(t *testing.T) {
	cache, cl := newCache(t)
	err := cache.SetNetworkSettings(t.Context(), cl,
		settings.NetworkSettings{AllowCIDRsEnabled: true, AllowCIDRs: []string{"10.0.0.0/8"}}, netip.Addr{})
	if err == nil {
		t.Fatal("an unverifiable caller address was accepted for an enforced whitelist")
	}
}

func TestSetNetworkSettings_EmptyAllowlistNeedsNoCallerAddress(t *testing.T) {
	cache, cl := newCache(t)
	if err := cache.SetNetworkSettings(t.Context(), cl, settings.NetworkSettings{}, netip.Addr{}); err != nil {
		t.Fatalf("SetNetworkSettings with an empty allowlist: %v", err)
	}
}

// The switch is what makes the list a rule, so a filled-in list nobody
// enabled can never lock its own author out (2026-09-19).
func TestSetNetworkSettings_AnUnenforcedWhitelistCannotLockAnyoneOut(t *testing.T) {
	cache, cl := newCache(t)
	caller := netip.MustParseAddr("203.0.113.7")
	if err := cache.SetNetworkSettings(t.Context(), cl,
		settings.NetworkSettings{AllowCIDRs: []string{"10.0.0.0/8"}}, caller); err != nil {
		t.Fatalf("SetNetworkSettings: %v", err)
	}
}

func TestSetNetworkSettings_RefusesEnforcingAnEmptyWhitelist(t *testing.T) {
	cache, cl := newCache(t)
	caller := netip.MustParseAddr("10.1.2.3")
	err := cache.SetNetworkSettings(t.Context(), cl,
		settings.NetworkSettings{AllowCIDRsEnabled: true}, caller)
	if err == nil {
		t.Fatal("a whitelist that allows nothing at all was accepted")
	}
}

func TestSetNetworkSettings_RefusesEnablingAProxyWithNoURL(t *testing.T) {
	cache, cl := newCache(t)
	err := cache.SetNetworkSettings(t.Context(), cl,
		settings.NetworkSettings{ProxyEnabled: true}, netip.Addr{})
	if err == nil {
		t.Fatal("a proxy was enabled with no URL to send anything to")
	}
}
