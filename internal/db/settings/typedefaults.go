package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// KeyLibraryTypeDefaults holds, per media type, what a library of that type uses until it sets its own.
const KeyLibraryTypeDefaults = "library.type_defaults"

// TypeDefaults are one media type's defaults; a nil or empty field means the type sets none and the shipped behaviour stands.
type TypeDefaults struct {
	MetadataSources   []string `json:"metadata_sources,omitempty"`
	QualityProfileIDs []int64  `json:"quality_profile_ids,omitempty"`
	TargetID          *int64   `json:"target_id,omitempty"`
	TargetAuto        bool     `json:"target_auto,omitempty"`
	LanguageProfileID *int64   `json:"language_profile_id,omitempty"`
}

func (s *Snapshot) typeDefaultsAll() map[string]TypeDefaults {
	all := map[string]TypeDefaults{}
	if raw := s.policy[KeyLibraryTypeDefaults]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &all); err != nil {
			return map[string]TypeDefaults{}
		}
	}
	return all
}

// TypeDefaultsFor are the defaults a media type's libraries inherit; the zero value when the type sets none.
func (s *Snapshot) TypeDefaultsFor(mediaType string) TypeDefaults {
	return s.typeDefaultsAll()[mediaType]
}

// TypeMetadataSources is the provider order a library of the type and kind inherits: the type's own default, else what ships.
func (s *Snapshot) TypeMetadataSources(mediaType, kind string) []string {
	if own := s.TypeDefaultsFor(mediaType).MetadataSources; len(own) > 0 {
		return slices.Clone(own)
	}
	return DefaultMetadataSources(mediaType, kind)
}

// SetTypeDefaults replaces a media type's defaults after checking each against what exists; every problem is reported at once.
func (c *Cache) SetTypeDefaults(ctx context.Context, cl clock.Clock, mediaType string, defaults TypeDefaults) error {
	snapshot := c.Snapshot()
	var problems config.Problems
	if !slices.Contains(MediaTypes, mediaType) {
		problems = append(problems, config.Problem{Path: "media_type", Value: mediaType, Expectation: "a media type"})
	}
	seen := map[string]bool{}
	for index, source := range defaults.MetadataSources {
		if !validSourceFor(mediaType, source) || seen[source] {
			problems = append(problems, config.Problem{Path: fmt.Sprintf("metadata_sources[%d]", index), Value: source, Expectation: "a provider of " + mediaType + " libraries, once"})
		}
		seen[source] = true
	}
	listed := map[int64]bool{}
	for index, id := range defaults.QualityProfileIDs {
		profile, found := snapshot.QualityProfile(id)
		if !found || profile.MediaType != mediaType || listed[id] {
			problems = append(problems, config.Problem{Path: fmt.Sprintf("quality_profile_ids[%d]", index), Value: strconv.FormatInt(id, 10), Expectation: "a " + mediaType + " quality profile, once"})
		}
		listed[id] = true
	}
	if defaults.TargetID != nil {
		if target, found := snapshot.QualityTarget(*defaults.TargetID); !found || target.MediaType != TargetFamily(mediaType) {
			problems = append(problems, config.Problem{Path: "target_id", Value: strconv.FormatInt(*defaults.TargetID, 10), Expectation: "a " + TargetFamily(mediaType) + " quality target"})
		}
		defaults.TargetAuto = false
	}
	if defaults.LanguageProfileID != nil {
		if _, found := snapshot.LanguageProfile(*defaults.LanguageProfileID); !found {
			problems = append(problems, config.Problem{Path: "language_profile_id", Value: strconv.FormatInt(*defaults.LanguageProfileID, 10), Expectation: "an existing language profile"})
		}
	}
	if len(problems) > 0 {
		return problems
	}
	all := snapshot.typeDefaultsAll()
	if defaults.MetadataSources == nil && len(defaults.QualityProfileIDs) == 0 && defaults.TargetID == nil && !defaults.TargetAuto && defaults.LanguageProfileID == nil {
		delete(all, mediaType)
	} else {
		all[mediaType] = defaults
	}
	encoded, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return c.setPolicy(ctx, cl, KeyLibraryTypeDefaults, string(encoded))
}

// withTypeDefaults gives each library the type's default for what it has no value of its own, and marks which it took.
func withTypeDefaults(categories []Category, policy map[string]string, targets []QualityTarget, profiles []QualityProfile, languages []LanguageProfile) []Category {
	snapshot := &Snapshot{policy: policy}
	all := snapshot.typeDefaultsAll()
	if len(all) == 0 {
		return categories
	}
	targetByID := make(map[int64]QualityTarget, len(targets))
	for _, target := range targets {
		targetByID[target.ID] = target
	}
	profileByID := make(map[int64]QualityProfile, len(profiles))
	for _, profile := range profiles {
		profileByID[profile.ID] = profile
	}
	for i := range categories {
		category := &categories[i]
		defaults, set := all[category.MediaType]
		if !set {
			continue
		}
		if len(category.QualityProfileIDs) == 0 {
			var ids []int64
			for _, id := range defaults.QualityProfileIDs {
				if profile, found := profileByID[id]; found && profile.MediaType == category.MediaType {
					ids = append(ids, id)
				}
			}
			if len(ids) > 0 {
				category.QualityProfileIDs, category.QualityProfileID, category.QualityProfilesInherited = ids, &ids[0], true
			}
		}
		if category.QualityTarget == nil && !category.QualityTargetAuto {
			switch target, found := targetByID[derefInt64(defaults.TargetID)]; {
			case defaults.TargetID != nil && found:
				category.QualityTarget, category.QualityTargetInherited = &target, true
			case defaults.TargetAuto:
				category.QualityTargetAuto, category.QualityTargetInherited = true, true
				if len(category.QualityProfileIDs) > 0 {
					if auto, derived := AutoTarget(profileByID[category.QualityProfileIDs[0]], category.MediaType); derived {
						category.QualityTarget = &auto
					}
				}
			}
		}
		if category.LanguageProfileID == nil && defaults.LanguageProfileID != nil {
			if slices.ContainsFunc(languages, func(profile LanguageProfile) bool { return profile.ID == *defaults.LanguageProfileID }) {
				id := *defaults.LanguageProfileID
				category.LanguageProfileID, category.LanguageProfileInherited = &id, true
			}
		}
	}
	return categories
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// ownQualityProfileIDs are the profiles a library chose itself; none while it follows its type's.
func ownQualityProfileIDs(category Category) []int64 {
	if category.QualityProfilesInherited {
		return nil
	}
	return category.QualityProfileIDs
}

// CurrentCategory is a library as just saved, for a handler whose own access was frozen when its request began.
func (c *Cache) CurrentCategory(id int64) (Category, bool) { return c.Snapshot().Category(id) }
