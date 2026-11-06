package judge

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
)

// ErrNoQualityProfile reports an item whose profile no longer exists.
var ErrNoQualityProfile = errors.New("judge: quality profile not found")

// RankedProfile is one of a library's profiles, in its priority order.
type RankedProfile struct {
	Name  string
	Rules score.Profile
	// Target is the library's target quality, which a decision records its closeness to; nil when it has none.
	Target *settings.QualityTarget
	// UpgradeRules rank for an item that already has files: the target's preference for over-quality applies only
	// when the library asked for it. Rules, the default, are for an item with nothing yet.
	UpgradeRules score.Profile
}

// ForUpgrade is the profiles as they judge an item that already has files.
func ForUpgrade(profiles []RankedProfile) []RankedProfile {
	upgrade := make([]RankedProfile, len(profiles))
	for i, profile := range profiles {
		profile.Rules = profile.UpgradeRules
		upgrade[i] = profile
	}
	return upgrade
}

// upgradeSlack is how much nearer to a target's number a release must be, in octaves of the distance, before it
// counts as better than the file it would replace.
const upgradeSlack = 0.5

// UpgradeBeats says a candidate beats the file an upgrade replaces. It is hesitant: a file that already has the
// target's container, codecs, resolution, frame rate or sample rate and channels is taken as on target whatever its
// bitrate (an earlier transcode, in Solarr or by Tdarr or FileFlows, lands near it, not on it), so a number alone never
// replaces it; for any other file a number must be clearly nearer, not just nearer. Equal tiers keep the incumbent.
func UpgradeBeats(profiles []RankedProfile, candidate, incumbent model.Decision, incumbentFile score.FileFacts, minScoreGain float64) bool {
	ignoreRates := len(profiles) > 0 && profiles[0].Target != nil && quality.MeetsTarget(*profiles[0].Target, incumbentFile)
	for i := 0; i < min(len(candidate.Quality.Tiers), len(incumbent.Quality.Tiers)); i++ {
		a, b := candidate.Quality.Tiers[i], incumbent.Quality.Tiers[i]
		if base, isDistance := strings.CutSuffix(a.Dimension, "_distance"); isDistance {
			if ignoreRates && rateDimensions[base] {
				continue
			}
			if a.Rank != math.MaxInt32 && b.Rank != math.MaxInt32 && b.Rank-a.Rank <= int(upgradeSlack*1_000_000) && a.Rank-b.Rank <= int(upgradeSlack*1_000_000) {
				continue
			}
		}
		if a.Rank != b.Rank {
			return a.Rank < b.Rank
		}
	}
	// Equal in every tier: only a score gain of at least minScoreGain wins (negative: nothing does), as BeatsIncumbent.
	gain := candidate.Score() - incumbent.Score()
	return minScoreGain >= 0 && gain > 0 && gain >= minScoreGain
}

// rateDimensions are the target distances that follow how a file was encoded rather than what it is.
var rateDimensions = map[string]bool{"video_bitrate": true, "audio_bitrate": true, "overall_bitrate": true}

// LibraryProfiles is the profile an item picked for itself, else the library's ordered list, else the item's own; the
// item's own target quality or else the library's, when there is one, leads each profile's ranking, whatever the media type.
func LibraryProfiles(
	ctx context.Context, desired library.Desired, snapshot *settings.Snapshot, itemID, categoryID, itemProfileID int64,
) ([]RankedProfile, error) {
	ids := []int64{itemProfileID}
	category, categoryFound := snapshot.Category(categoryID)
	if categoryFound && len(category.QualityProfileIDs) > 0 {
		ids = category.QualityProfileIDs
	}
	override, overridden, err := desired.ProfileOverride(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if overridden {
		ids = []int64{override}
	}
	chosen := category.QualityTarget
	if targetID, own, err := desired.TargetOverride(ctx, itemID); err != nil {
		return nil, err
	} else if own {
		if target, found := snapshot.QualityTarget(targetID); found {
			chosen = &target
		}
	}
	if categoryFound && !overridden && len(category.QualityProfileIDs) == 0 && chosen != nil {
		return []RankedProfile{TargetOnly(*chosen)}, nil
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
		var target *settings.QualityTarget
		upgrade := rules
		if chosen != nil {
			upgrade = WithTarget(rules, *chosen)
			rules = WithFreshTarget(rules, *chosen)
			target = chosen
		}
		profiles = append(profiles, RankedProfile{Name: profile.Name, Rules: rules, Target: target, UpgradeRules: upgrade})
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

// TargetOnly is the profile of a library that has a target and no quality profile: nothing is refused but the old
// codecs nobody wants, and the target ranks what is found, then the source (a disc or a web download over a broadcast
// capture or a DVD).
func TargetOnly(target settings.QualityTarget) RankedProfile {
	base := score.Profile{Name: "Target only", Compare: []string{"score"}}
	if target.IsVideo() {
		base.Require.RejectCodecs = []string{"xvid", "divx"}
		base.Tiers = map[string][][]string{"source": {{"bluray", "webdl"}, {"remux"}, {"webrip"}, {"hdtv"}, {"dvd"}}}
		base.Compare = []string{"source", "score"}
	}
	return RankedProfile{
		Name: base.Name, Rules: WithFreshTarget(base, target), UpgradeRules: WithTarget(base, target), Target: &target,
	}
}

// EvaluateInOrder judges a release by the first profile it fits, so a release the first profile wants outranks one
// only a fallback wants; failing that, by the first whose rules merely accept it. A release no profile accepts is
// judged, and refused, by the first. The profile's position leads the quality vector (ADR-40: tiers, not weights).
func EvaluateInOrder(profiles []RankedProfile, facts score.Facts, languagePolicy *score.LanguagePolicy) model.Decision {
	return withTargetCloseness(evaluateInOrder(profiles, facts, languagePolicy), profiles, facts)
}

// withTargetCloseness records how near the release is to the library's target; it ranks nothing (the target's own
// tiers and distances already do), it only shows.
func withTargetCloseness(decision model.Decision, profiles []RankedProfile, facts score.Facts) model.Decision {
	if len(profiles) == 0 || profiles[0].Target == nil {
		return decision
	}
	closeness, ok := quality.Distance(*profiles[0].Target, facts.File)
	if !ok {
		return decision
	}
	label := fmt.Sprintf("%d%% of the target %q", closeness.Percent, profiles[0].Target.Name)
	if gaps := closeness.Gaps(); len(gaps) > 0 {
		label += ": " + gaps[0].Dimension + " " + gaps[0].Have + " for " + gaps[0].Want
	}
	decision.Components = append(decision.Components, model.ScoreComponent{Rule: "target-closeness", Label: label})
	return decision
}

func evaluateInOrder(profiles []RankedProfile, facts score.Facts, languagePolicy *score.LanguagePolicy) model.Decision {
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

// StrictlyBetter is ADR-40: higher by the quality tiers, or equal there and higher in score. A tie keeps what
// is owned.
func StrictlyBetter(candidate, incumbent model.Decision) bool {
	winner, _ := model.Compare(candidate.Quality, incumbent.Quality)
	return winner < 0 || (winner == 0 && candidate.Score() > incumbent.Score())
}

// BeatsIncumbent is StrictlyBetter for an upgrade: equal in the tiers, the score must also win by the profile's
// minimum delta (Invariant 21).
func BeatsIncumbent(candidate, incumbent model.Decision, minDelta float64) bool {
	winner, _ := model.Compare(candidate.Quality, incumbent.Quality)
	gain := candidate.Score() - incumbent.Score()
	return winner < 0 || (winner == 0 && gain > 0 && gain >= minDelta)
}

// UpgradePolicyFor is the upgrade policy of the category's first quality profile; the zero policy when it has none.
func UpgradePolicyFor(snapshot *settings.Snapshot, categoryID int64) score.UpgradePolicy {
	category, found := snapshot.Category(categoryID)
	if !found || len(category.QualityProfileIDs) == 0 {
		return score.UpgradePolicy{}
	}
	profile, found := snapshot.QualityProfile(category.QualityProfileIDs[0])
	if !found {
		return score.UpgradePolicy{}
	}
	policy, err := score.ParseUpgrade([]byte(profile.UpgradeJSON))
	if err != nil {
		return score.UpgradePolicy{}
	}
	return policy
}
