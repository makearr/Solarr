package music

import (
	"encoding/json"
	"strings"

	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// DefaultExcludedSecondaryTypes is section 19.6's default, since an artist subscription returning every bootleg gets switched off; matched case-insensitively by substring against MusicBrainz's vocabulary.
var DefaultExcludedSecondaryTypes = []string{"compilation", "live", "remix", "dj-mix", "mixtape", "demo", "interview", "audiobook", "audio drama", "field recording"}

// DefaultIncludedPrimaryTypes is what a subscription naming no types brings in; a release group with no primary type is admitted rather than silently dropped.
var DefaultIncludedPrimaryTypes = []string{"Album", "EP", "Single"}

// SubscriptionFilters admits or excludes release groups in an artist subscription's expansion (ADR-38, section 19.6); kept here because the vocabulary is media-type-specific (ADR-23).
type SubscriptionFilters struct {
	// IncludePrimaryTypes is which types the subscription brings in; a pointer so an explicit empty list differs from absent, which means the library's setting or the default.
	IncludePrimaryTypes *[]string `json:"include_primary_types,omitempty"`

	// ExcludeSecondaryTypes overrides the default; a pointer so an explicit empty list (exclude nothing) differs from absent, which gets the default, never a silent exclude-nothing.
	ExcludeSecondaryTypes *[]string `json:"exclude_secondary_types,omitempty"`
}

// studioAlbumType is Lidarr's name for "no secondary type": allowing it excludes nothing, so it never becomes an exclusion.
const studioAlbumType = "studio"

// FiltersOfLidarrProfile are the release types a Lidarr metadata profile allows: its allowed primary types, and every
// secondary type it does not allow as an exclusion.
func FiltersOfLidarrProfile(profile arrimport.LidarrMetadataProfile) SubscriptionFilters {
	primary, excluded := []string{}, []string{}
	for _, use := range profile.PrimaryAlbumTypes {
		if use.Allowed {
			primary = append(primary, use.AlbumType.Name)
		}
	}
	for _, use := range profile.SecondaryAlbumTypes {
		name := strings.ToLower(use.AlbumType.Name)
		if !use.Allowed && name != studioAlbumType {
			excluded = append(excluded, name)
		}
	}
	return SubscriptionFilters{IncludePrimaryTypes: &primary, ExcludeSecondaryTypes: &excluded}
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
