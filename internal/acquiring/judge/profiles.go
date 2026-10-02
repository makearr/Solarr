package judge

import (
	"context"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/score"
)

// ErrNoQualityProfile reports an item whose profile no longer exists.
var ErrNoQualityProfile = errors.New("judge: quality profile not found")

// RankedProfile is one of a library's profiles, in its priority order.
type RankedProfile struct {
	Name  string
	Rules score.Profile
}

// LibraryProfiles is the profile an item picked for itself, else the library's ordered list, else the item's own.
func LibraryProfiles(
	ctx context.Context, desired library.Desired, snapshot *settings.Snapshot, itemID, categoryID, itemProfileID int64,
) ([]RankedProfile, error) {
	ids := []int64{itemProfileID}
	if category, found := snapshot.Category(categoryID); found && len(category.QualityProfileIDs) > 0 {
		ids = category.QualityProfileIDs
	}
	override, overridden, err := desired.ProfileOverride(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if overridden {
		ids = []int64{override}
	}
	profiles := make([]RankedProfile, 0, len(ids))
	for _, id := range ids {
		profile, found := snapshot.QualityProfile(id)
		if !found {
			return nil, ErrNoQualityProfile
		}
		rules, err := score.Parse([]byte(profile.RulesJSON))
		if err != nil {
			return nil, fmt.Errorf("quality profile %d: %w", id, err)
		}
		profiles = append(profiles, RankedProfile{Name: profile.Name, Rules: rules})
	}
	return profiles, nil
}

// LanguagePolicyFor resolves the category's language profile; nil when none is assigned (language stays optional).
func LanguagePolicyFor(snapshot *settings.Snapshot, categoryID int64) *score.LanguagePolicy {
	category, found := snapshot.Category(categoryID)
	if !found || category.LanguageProfileID == nil {
		return nil
	}
	profile, found := snapshot.LanguageProfile(*category.LanguageProfileID)
	if !found {
		return nil
	}
	return &score.LanguagePolicy{
		AudioRequired: profile.AudioRequired, AudioPreferred: profile.AudioPreferred,
		SubtitleRequired: profile.SubtitleRequired, SubtitlePreferred: profile.SubtitlePreferred,
		PreferOriginalAudio: profile.PreferOriginalAudio, RejectDubbed: profile.RejectDubbed,
	}
}

// EvaluateInOrder judges a release by the first profile it fits, so a release the first profile wants outranks one
// only a fallback wants; failing that, by the first whose rules merely accept it. A release no profile accepts is
// judged, and refused, by the first. The profile's position leads the quality vector (ADR-40: tiers, not weights).
func EvaluateInOrder(profiles []RankedProfile, facts score.Facts, languagePolicy *score.LanguagePolicy) model.Decision {
	evaluate := func(rules score.Profile) model.Decision {
		if languagePolicy != nil {
			return score.EvaluateWithLanguage(rules, facts, nil, *languagePolicy, facts.File.AudioLanguages, facts.File.SubtitleLanguages)
		}
		return score.Evaluate(rules, facts, nil)
	}
	fallback, fallbackIndex := model.Decision{}, -1
	for index, profile := range profiles {
		decision := evaluate(profile.Rules)
		if decision.Outcome != model.OutcomeAccept {
			continue
		}
		if fits, _ := score.Fits(profile.Rules, facts); fits {
			return rankedBy(decision, index, profile.Name, "fits")
		}
		if fallbackIndex < 0 {
			fallback, fallbackIndex = decision, index
		}
	}
	if fallbackIndex >= 0 {
		return rankedBy(fallback, len(profiles)+fallbackIndex, profiles[fallbackIndex].Name, "accepted, fits none")
	}
	return evaluate(profiles[0].Rules)
}

// WithRevision appends Sonarr's revision after the quality tiers when the library prefers propers: a proper or
// repack beats the same quality, never a better one, and before any additive score.
func WithRevision(decision model.Decision, proper, prefer bool) model.Decision {
	if !prefer {
		return decision
	}
	tier := model.TierValue{Dimension: "revision", Rank: 1, Label: "original"}
	if proper {
		tier.Rank, tier.Label = 0, "proper or repack"
	}
	decision.Quality.Tiers = append(decision.Quality.Tiers, tier)
	return decision
}

func rankedBy(decision model.Decision, rank int, name, how string) model.Decision {
	tier := model.TierValue{Dimension: "profile_priority", Rank: rank, Label: name, Detail: fmt.Sprintf("%s (%s)", name, how)}
	decision.Quality.Tiers = append([]model.TierValue{tier}, decision.Quality.Tiers...)
	decision.Components = append(decision.Components, model.ScoreComponent{
		Rule: "profile-priority", Label: fmt.Sprintf("Wanted by %s (%s)", name, how),
	})
	return decision
}

// StrictlyBetter is Invariant 72: higher by the quality tiers, or equal there and higher in score. A tie keeps what
// is owned.
func StrictlyBetter(candidate, incumbent model.Decision) bool {
	winner, _ := model.Compare(candidate.Quality, incumbent.Quality)
	return winner < 0 || (winner == 0 && candidate.Score() > incumbent.Score())
}
