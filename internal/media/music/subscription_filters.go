package music

import (
	"encoding/json"
	"strings"

	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// DefaultExcludedSecondaryTypes is section 19.6's own default: an artist
// subscription that returns every bootleg is the version of this feature
// people switch off. Matched case-insensitively and by substring against
// MusicBrainz's own secondary-type vocabulary, since "Mixtape/Street" is the
// real value for what a user thinks of as "mixtape".
var DefaultExcludedSecondaryTypes = []string{"compilation", "live", "remix", "dj-mix", "mixtape"}

// DefaultIncludedPrimaryTypes is what a subscription naming no types brings
// in: the three anyone means by "their albums" (2026-09-19). Matched
// case-insensitively against MusicBrainz's own primary-type vocabulary. A
// release group with no primary type at all is admitted -- refusing what a
// provider simply did not say would silently drop real albums.
var DefaultIncludedPrimaryTypes = []string{"Album", "EP", "Single"}

// SubscriptionFilters is what admits or excludes one release group from an
// artist subscription's expansion (ADR-38's preview-before-enable shape,
// applied to subscriptions per section 19.6). Kept in internal/media/music, not
// internal/db/library: which vocabulary a filter names is media-type-specific
// (ADR-23), library.Subscription's FiltersJSON stays opaque.
type SubscriptionFilters struct {
	// IncludePrimaryTypes is which release-group types the subscription
	// brings in at all -- "Albums EP Singles by default", the user's own
	// words (2026-09-19), overridable per artist. A pointer for the same
	// reason ExcludeSecondaryTypes is: an explicit empty list is a choice.
	// Absent means the library's own setting, which the caller resolves
	// before this is stored, or DefaultIncludedPrimaryTypes if it did not.
	IncludePrimaryTypes *[]string `json:"include_primary_types,omitempty"`

	// ExcludeSecondaryTypes overrides DefaultExcludedSecondaryTypes when
	// present, a pointer so an explicit empty list ("exclude nothing" --
	// todo 4.4's preview lets a user choose exactly that) is distinguished
	// from absent. A subscription stored with no filters at all (nil, or
	// the key simply missing from stored JSON) gets the default -- never a
	// silent "exclude nothing", which is exactly the behaviour that makes
	// people turn discography monitoring off.
	ExcludeSecondaryTypes *[]string `json:"exclude_secondary_types,omitempty"`
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

// exclusions is the effective exclude list: the subscription's own explicit
// choice (however short, including empty -- "exclude nothing" is a real
// choice), or the package default when it named none at all.
func (f SubscriptionFilters) exclusions() []string {
	if f.ExcludeSecondaryTypes != nil {
		return *f.ExcludeSecondaryTypes
	}
	return DefaultExcludedSecondaryTypes
}

// inclusions is the effective primary-type list, the subscription's own
// choice or the package default.
func (f SubscriptionFilters) inclusions() []string {
	if f.IncludePrimaryTypes != nil {
		return *f.IncludePrimaryTypes
	}
	return DefaultIncludedPrimaryTypes
}

// Admits reports whether rg passes this filter: its primary type must be one
// the subscription asked for, and none of its secondary types may be excluded.
func (f SubscriptionFilters) Admits(rg musicbrainz.ReleaseGroup) bool {
	if !f.admitsPrimary(rg.PrimaryType) {
		return false
	}
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

func (f SubscriptionFilters) admitsPrimary(primary string) bool {
	if primary == "" {
		return true
	}
	for _, included := range f.inclusions() {
		if strings.EqualFold(primary, included) {
			return true
		}
	}
	return false
}
