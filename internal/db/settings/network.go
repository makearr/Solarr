package settings

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// ADR-58's own keys, the same instance_setting shape auth.required and the
// cache budget already use.
const (
	KeyNetworkPort              = "network.port"
	KeyNetworkProxyEnabled      = "network.proxy_enabled"
	KeyNetworkProxyURL          = "network.proxy_url"
	KeyNetworkProxyBypass       = "network.proxy_bypass"
	KeyNetworkAllowCIDRsEnabled = "network.allow_cidrs_enabled"
	KeyNetworkAllowCIDRs        = "network.allow_cidrs"
)

// DefaultLocalRanges fills both lists on an instance that has never been
// configured, so the ranges anyone would type are already there (2026-09-19).
// Neither list does anything until its own enable flag is turned on.
var DefaultLocalRanges = []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}

// NetworkSettings is ADR-58's own policy. Port 0 means unset -- config.yml
// governs until the Network screen writes one. The proxy and the whitelist
// each have their own enable flag, off until someone turns it on
// (2026-09-19), so a prefilled list is a suggestion rather than a rule.
type NetworkSettings struct {
	Port              int
	ProxyEnabled      bool
	ProxyURL          string
	ProxyBypass       []string
	AllowCIDRsEnabled bool
	AllowCIDRs        []string
}

// NetworkSettings reads the configured policy, or its zero-value defaults for
// whatever nothing has stored yet.
func (s *Snapshot) NetworkSettings() NetworkSettings {
	return NetworkSettings{
		Port:              int(s.policyInt(KeyNetworkPort, 0)),
		ProxyEnabled:      s.policyBool(KeyNetworkProxyEnabled, false),
		ProxyURL:          s.policy[KeyNetworkProxyURL],
		ProxyBypass:       s.policyStringListOr(KeyNetworkProxyBypass, DefaultLocalRanges),
		AllowCIDRsEnabled: s.policyBool(KeyNetworkAllowCIDRsEnabled, false),
		AllowCIDRs:        s.policyStringListOr(KeyNetworkAllowCIDRs, DefaultLocalRanges),
	}
}

func (s *Snapshot) policyBool(key string, fallback bool) bool {
	value, found := s.policy[key]
	if !found {
		return fallback
	}
	return value == "true"
}

func boolPolicy(value bool) string { return strconv.FormatBool(value) }

// policyStringListOr distinguishes "never configured", which takes the
// default, from a list the user deliberately emptied.
func (s *Snapshot) policyStringListOr(key string, fallback []string) []string {
	if raw, found := s.policy[key]; !found || raw == "" {
		return fallback
	}
	return s.policyStringList(key)
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

	return c.setPolicies(ctx, cl, map[string]string{
		KeyNetworkPort:              strconv.Itoa(ns.Port),
		KeyNetworkProxyEnabled:      boolPolicy(ns.ProxyEnabled),
		KeyNetworkProxyURL:          ns.ProxyURL,
		KeyNetworkProxyBypass:       string(proxyBypass),
		KeyNetworkAllowCIDRsEnabled: boolPolicy(ns.AllowCIDRsEnabled),
		KeyNetworkAllowCIDRs:        string(allowCIDRs),
	})
}

// ValidateNetworkSettings is SetNetworkSettings' check alone, for a caller with a write of its own to make first.
func ValidateNetworkSettings(ns NetworkSettings, callerAddress netip.Addr) error {
	if problems := validateNetworkSettings(ns, callerAddress); len(problems) > 0 {
		return problems
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

	if ns.ProxyEnabled && strings.TrimSpace(ns.ProxyURL) == "" {
		problems = append(problems, config.Problem{
			Path: "proxy_url", Expectation: "a proxy URL, or turn the proxy off",
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

	if ns.AllowCIDRsEnabled && len(ns.AllowCIDRs) == 0 {
		problems = append(problems, config.Problem{
			Path: "allow_cidrs", Expectation: "at least one CIDR, or turn the whitelist off",
		})
	}

	// Only once every entry parses, or a lockout report obscures a typo. An
	// off whitelist cannot lock anyone out, however it is filled in.
	if len(problems) == 0 && ns.AllowCIDRsEnabled && len(prefixes) > 0 &&
		!addressAllowedByAny(callerAddress, prefixes) {
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
