package httpx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// ProxyPolicy is ADR-58's global egress proxy: every outbound request uses it
// except a host matched by Bypass. A nil URL means no proxy at all.
type ProxyPolicy struct {
	URL *url.URL
	// Bypass is a CIDR (matched against a literal IP host) or a leading-dot
	// host suffix (".lan"); neither needs a DNS lookup to evaluate.
	Bypass []string
}

// LiveProxyPolicy reads the current policy fresh on every request, so a
// proxy change applies immediately -- unlike the listen port, no restart.
type LiveProxyPolicy func() ProxyPolicy

// ProxyFunc builds the func http.Transport.Proxy calls before every request.
// Returning nil means this one request goes direct.
func ProxyFunc(policy LiveProxyPolicy) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		current := policy()
		if current.URL == nil || bypassed(req.URL.Hostname(), current.Bypass) {
			return nil, nil
		}
		return current.URL, nil
	}
}

func bypassed(host string, patterns []string) bool {
	if literal, err := netip.ParseAddr(host); err == nil {
		for _, pattern := range patterns {
			if prefix, err := netip.ParsePrefix(pattern); err == nil && prefix.Contains(literal) {
				return true
			}
		}
		return false
	}

	host = strings.ToLower(host)
	for _, pattern := range patterns {
		pattern = strings.ToLower(pattern)
		if suffix, isSuffix := strings.CutPrefix(pattern, "."); isSuffix {
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == pattern {
			return true
		}
	}
	return false
}

// ProxyDial skips classification only for a dial to the configured proxy
// (ADR-47, Invariant 65: operator policy, not attacker input) -- everything else uses the full guard.
func ProxyDial(guard *Guard, policy LiveProxyPolicy) DialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if proxyHostPort := currentProxyHostPort(policy()); proxyHostPort != "" && sameHostPort(address, proxyHostPort) {
			return guard.DialUnchecked(ctx, network, address)
		}
		return guard.DialContext(ctx, network, address)
	}
}

func currentProxyHostPort(policy ProxyPolicy) string {
	if policy.URL == nil {
		return ""
	}
	return policy.URL.Host
}

// sameHostPort compares two "host:port" strings; both sides carry an
// explicit port (validated on write), so no default-port table is needed.
func sameHostPort(a, b string) bool {
	aHost, aPort, aErr := net.SplitHostPort(a)
	bHost, bPort, bErr := net.SplitHostPort(b)
	if aErr != nil || bErr != nil {
		return false
	}
	return strings.EqualFold(aHost, bHost) && aPort == bPort
}
