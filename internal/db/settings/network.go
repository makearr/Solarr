package settings

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/config"
)

// ADR-58's own keys, the same instance_setting shape auth.required and the
// cache budget already use.
const (
	KeyNetworkPort        = "network.port"
	KeyNetworkProxyURL    = "network.proxy_url"
	KeyNetworkProxyBypass = "network.proxy_bypass"
	KeyNetworkAllowCIDRs  = "network.allow_cidrs"
)

// NetworkSettings is ADR-58's own policy. Port 0 means unset -- config.yml
// governs until the Network screen writes one; AllowCIDRs empty allows all.
type NetworkSettings struct {
	Port        int
	ProxyURL    string
	ProxyBypass []string
	AllowCIDRs  []string
}

// NetworkSettings reads the configured policy, or its zero-value defaults for
// whatever nothing has stored yet.
func (s *Snapshot) NetworkSettings() NetworkSettings {
	return NetworkSettings{
		Port:        int(s.policyInt(KeyNetworkPort, 0)),
		ProxyURL:    s.policy[KeyNetworkProxyURL],
		ProxyBypass: s.policyStringList(KeyNetworkProxyBypass),
		AllowCIDRs:  s.policyStringList(KeyNetworkAllowCIDRs),
	}
}

func (s *Snapshot) policyStringList(key string) []string {
	raw, found := s.policy[key]
	if !found || raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

// SetNetworkSettings writes ADR-58's policy wholesale (ADR-44: validated on
// write). callerAddress is refused a non-empty allow_cidrs that excludes it (Invariant 89).
func (c *Cache) SetNetworkSettings(ctx context.Context, cl clock.Clock, ns NetworkSettings, callerAddress netip.Addr) error {
	if problems := validateNetworkSettings(ns, callerAddress); len(problems) > 0 {
		return problems
	}

	proxyBypass, err := json.Marshal(ns.ProxyBypass)
	if err != nil {
		return err
	}
	allowCIDRs, err := json.Marshal(ns.AllowCIDRs)
	if err != nil {
		return err
	}

	for key, value := range map[string]string{
		KeyNetworkPort:        strconv.Itoa(ns.Port),
		KeyNetworkProxyURL:    ns.ProxyURL,
		KeyNetworkProxyBypass: string(proxyBypass),
		KeyNetworkAllowCIDRs:  string(allowCIDRs),
	} {
		if err := c.setPolicy(ctx, cl, key, value); err != nil {
			return err
		}
	}
	return nil
}

func validateNetworkSettings(ns NetworkSettings, callerAddress netip.Addr) config.Problems {
	var problems config.Problems

	if ns.Port != 0 && (ns.Port < 1 || ns.Port > 65535) {
		problems = append(problems, config.Problem{
			Path: "port", Value: strconv.Itoa(ns.Port), Expectation: "0 (unset) or a port between 1 and 65535",
		})
	}

	if ns.ProxyURL != "" {
		parsed, err := url.Parse(ns.ProxyURL)
		if err != nil || parsed.Host == "" || parsed.Port() == "" || !isSupportedProxyScheme(parsed.Scheme) {
			problems = append(problems, config.Problem{
				Path: "proxy_url", Value: ns.ProxyURL,
				Expectation: "a URL with scheme http, https or socks5 and an explicit port, e.g. socks5://host:1080",
			})
		}
	}

	for _, bypass := range ns.ProxyBypass {
		if strings.TrimSpace(bypass) == "" {
			problems = append(problems, config.Problem{
				Path: "proxy_bypass", Expectation: "a CIDR (10.0.0.0/8) or a host suffix (.lan), never empty",
			})
		}
	}

	prefixes := make([]netip.Prefix, 0, len(ns.AllowCIDRs))
	for _, raw := range ns.AllowCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			problems = append(problems, config.Problem{
				Path: "allow_cidrs", Value: raw, Expectation: "a CIDR such as 10.0.0.0/8 or 192.168.1.10/32",
			})
			continue
		}
		prefixes = append(prefixes, prefix)
	}

	// Only once every entry parses, or a lockout report obscures a typo.
	if len(problems) == 0 && len(prefixes) > 0 && !addressAllowedByAny(callerAddress, prefixes) {
		problems = append(problems, config.Problem{
			Path: "allow_cidrs", Value: strings.Join(ns.AllowCIDRs, ", "),
			Expectation: "must include the address making this change, or it would lock out the caller",
		})
	}

	return problems
}

func isSupportedProxyScheme(scheme string) bool {
	return scheme == "http" || scheme == "https" || scheme == "socks5"
}

// addressAllowedByAny reports whether address is inside any of prefixes; an
// invalid address is never allowed, since it cannot be verified safe.
func addressAllowedByAny(address netip.Addr, prefixes []netip.Prefix) bool {
	if !address.IsValid() {
		return false
	}
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
