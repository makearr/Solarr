package spec

import (
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// sameSite says whether target is the tracker itself: its address, a subdomain or sibling of it under one registrable
// domain, or one of the spec's own mirrors. Anything else (a CDN, an ad row, a typo-squatted mirror) must not see the
// owner's cookie or key.
func (d *Driver) sameSite(target string) bool {
	host := hostOf(target)
	if host == "" {
		return false
	}
	own := []string{d.baseURL}
	own = append(own, d.spec.Links...)
	own = append(own, d.spec.LegacyLinks...)
	for _, address := range own {
		other := hostOf(address)
		if other == "" {
			continue
		}
		if host == other {
			return true
		}
		site, err := publicsuffix.EffectiveTLDPlusOne(host)
		otherSite, otherErr := publicsuffix.EffectiveTLDPlusOne(other)
		if err == nil && otherErr == nil && site == otherSite {
			return true
		}
	}
	return false
}

func hostOf(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}
