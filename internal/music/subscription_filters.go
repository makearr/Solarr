package music

import (
	"encoding/json"
	"strings"

	"github.com/makearr/MediaNexus/internal/musicbrainz"
)

// DefaultExcludedSecondaryTypes is section 19.6's own default: an artist
// subscription that returns every bootleg is the version of this feature
// people switch off. Matched case-insensitively and by substring against
// MusicBrainz's own secondary-type vocabulary, since "Mixtape/Street" is the
// real value for what a user thinks of as "mixtape".
var DefaultExcludedSecondaryTypes = []string{"compilation", "live", "remix", "dj-mix", "mixtape"}

// SubscriptionFilters is what admits or excludes one release group from an
// artist subscription's expansion (ADR-38's preview-before-enable shape,
// applied to subscriptions per section 19.6). Kept in internal/music, not
// internal/library: which vocabulary a filter names is media-type-specific
// (ADR-23), library.Subscription's FiltersJSON stays opaque.
type SubscriptionFilters struct {
	// ExcludeSecondaryTypes overrides DefaultExcludedSecondaryTypes when
	// non-empty. A subscription stored with no filters at all (empty JSON)
	// gets the default -- never "exclude nothing", which is exactly the
	// behaviour that makes people turn discography monitoring off.
	ExcludeSecondaryTypes []string `json:"exclude_secondary_types,omitempty"`
}

// ParseSubscriptionFilters decodes a stored subscription's opaque
// FiltersJSON, defaulting to zero value (which Admits then reads as "use
// DefaultExcludedSecondaryTypes") for an empty or unparsable string.
func ParseSubscriptionFilters(raw string) SubscriptionFilters {
	var filters SubscriptionFilters
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &filters)
	}
	return filters
}

// exclusions is the effective exclude list: the subscription's own, or the
// package default when it named none.
func (f SubscriptionFilters) exclusions() []string {
	if len(f.ExcludeSecondaryTypes) > 0 {
		return f.ExcludeSecondaryTypes
	}
	return DefaultExcludedSecondaryTypes
}

// Admits reports whether rg passes this filter -- true unless one of its
// secondary types matches an excluded one.
func (f SubscriptionFilters) Admits(rg musicbrainz.ReleaseGroup) bool {
	for _, secondary := range rg.SecondaryTypes {
		lowered := strings.ToLower(secondary)
		for _, excluded := range f.exclusions() {
			if strings.Contains(lowered, strings.ToLower(excluded)) {
				return false
			}
		}
	}
	return true
}
