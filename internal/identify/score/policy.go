package score

import (
	"encoding/json"
	"fmt"
	"time"
)

// Profile is ADR-40's quality profile, parsed from settings.QualityProfile.RulesJSON
// -- production JSON, the design doc's YAML is the same shape for readability.
type Profile struct {
	Name    string                `json:"quality_profile"`
	Require Requirements          `json:"require"`
	Compare []string              `json:"compare"`
	Tiers   map[string][][]string `json:"tiers"`
	// Targets keys on a dimension name ("bitrate", "sample_rate", "bit_depth", ...)
	// -- ADR-40 amended 2026-09-04: any numeric dimension may carry a target, not
	// just bitrate. A dimension's own "<dimension>_distance" compare entry reads
	// this; a dimension absent here keeps the plain "higher is better" numeric
	// comparison, so an existing bitrate-only profile needs no changes at all.
	Targets map[string][]Target `json:"targets"`
	Scoring []ScoringRule       `json:"scoring"`
	Upgrade UpgradePolicy       `json:"upgrade"`
}

// Requirements is ADR-40 stage 1 -- every entry is evaluated, never short-circuited,
// so a rejected candidate's UI can show everything wrong at once.
type Requirements struct {
	Resolution    *ResolutionRange `json:"resolution,omitempty"`
	AudioLanguage *AnyOfClause     `json:"audio_language,omitempty"`
	Bitrate       string           `json:"bitrate,omitempty"` // "reject_below_tier_min"
	RejectCodecs  []string         `json:"reject_codecs,omitempty"`
}

// ResolutionRange bounds are resolved against tiers["resolution"], not a numeric
// scale, since "720p" only means something relative to a profile's own tier order.
type ResolutionRange struct {
	Min string `json:"min"`
	Max string `json:"max"`
}

// AnyOfClause is ADR-40/section 19.8's any_of gate: at least one listed value must
// be present, never all of them.
type AnyOfClause struct {
	AnyOf []string `json:"any_of"`
}

// Target supplies the ideal/min/max for one dimension, for files matching When,
// checked in declaration order -- the first match wins. Values are strings so a
// dimension can carry a human-readable unit ("20Mbps", "48kHz") the way a release
// name or a *Arr-style quality definition would state it; parseTargetValue
// converts a dimension's own unit to the number Facts reports.
type Target struct {
	When    map[string]string `json:"when"`
	Min     string            `json:"min,omitempty"`
	Ideal   string            `json:"ideal"`
	Max     string            `json:"max,omitempty"`
	Weights TargetWeights     `json:"weights"`
}

// TargetWeights scales the over/under asymmetry of a dimension's distance
// (ADR-40). Parse defaults a zero field per-dimension: bitrate defaults to
// symmetric 1/1, sample_rate and bit_depth default over below under (0.5/1),
// so a name is not required to also copy the asymmetry that makes it useful.
type TargetWeights struct {
	Over  float64 `json:"over"`
	Under float64 `json:"under"`
}

// ScoringRule is one *Arr-Custom-Format-shaped rule (ADR-40 stage 3): every
// required spec must match, and at least one non-required spec must too if any exist.
type ScoringRule struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"`
	Specs []Spec  `json:"specs"`
}

// Spec is one condition within a ScoringRule; exactly one operator field is set.
// Negate inverts that operator's own result before the required/any-of logic combines it.
type Spec struct {
	Field    string    `json:"field"`
	In       *InClause `json:"in,omitempty"`
	Has      string    `json:"has,omitempty"`
	Matches  string    `json:"matches,omitempty"`
	GTE      *float64  `json:"gte,omitempty"`
	GT       *float64  `json:"gt,omitempty"`
	LTE      *float64  `json:"lte,omitempty"`
	LT       *float64  `json:"lt,omitempty"`
	Eq       *string   `json:"eq,omitempty"`
	Ne       *string   `json:"ne,omitempty"`
	Negate   bool      `json:"negate,omitempty"`
	Required bool      `json:"required,omitempty"`
}

// InClause accepts a literal list or the "$preferred" placeholder, which Evaluate
// substitutes with a caller-supplied allowlist -- the profile never hardcodes it.
type InClause struct {
	Placeholder bool
	Values      []string
}

func (clause *InClause) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		if single == "$preferred" {
			clause.Placeholder = true
			return nil
		}
		clause.Values = []string{single}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("score: \"in\" must be a string or list of strings: %w", err)
	}
	clause.Values = list
	return nil
}

// UpgradePolicy is parsed but deliberately not evaluated -- deciding whether an
// upgrade is allowed needs an incumbent file and acquisition history (stage 6).
type UpgradePolicy struct {
	MinDelta     float64  `json:"min_delta"`
	MinFileAge   Duration `json:"min_file_age"`
	MaxPerWindow int      `json:"max_per_window"`
	Window       Duration `json:"window"`
}

// Duration decodes a Go duration string ("24h", "168h") from JSON.
type Duration time.Duration

func (d *Duration) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		return nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("score: invalid duration %q: %w", raw, err)
	}
	*d = Duration(parsed)
	return nil
}

// Parse decodes a quality profile's rules JSON, applying the defaults an omitted
// block implies.
func Parse(data []byte) (Profile, error) {
	var profile Profile
	if err := json.Unmarshal(data, &profile); err != nil {
		return Profile{}, fmt.Errorf("score: parsing profile: %w", err)
	}
	for dimension, targets := range profile.Targets {
		if _, declaredAsTier := profile.Tiers[dimension]; declaredAsTier {
			return Profile{}, fmt.Errorf(
				"score: %q is declared in both \"tiers\" and \"targets\" -- a dimension is one or the other, never both (Invariant 72)",
				dimension)
		}
		for index := range targets {
			defaultTargetWeights(dimension, &targets[index].Weights)
		}
	}
	return profile, nil
}

// defaultTargetWeights fills a zero Over or Under with the dimension's own
// default, not a single global one: bitrate stays symmetric (1/1), sample_rate
// and bit_depth over-weight below under-weight (0.5/1) so overshooting a target
// costs less than undershooting it -- ADR-40's amendment, confirmed with the
// user. A caller who sets one side explicitly keeps it; only a zero field
// (JSON omitted it) is defaulted.
func defaultTargetWeights(dimension string, weights *TargetWeights) {
	over, under := 1.0, 1.0
	if dimension == "sample_rate" || dimension == "bit_depth" {
		over = 0.5
	}
	if weights.Over == 0 {
		weights.Over = over
	}
	if weights.Under == 0 {
		weights.Under = under
	}
}

// ParseUpgrade decodes an UpgradeJSON column stored separately from the rules.
func ParseUpgrade(data []byte) (UpgradePolicy, error) {
	var policy UpgradePolicy
	if len(data) == 0 {
		return policy, nil
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		return UpgradePolicy{}, fmt.Errorf("score: parsing upgrade policy: %w", err)
	}
	return policy, nil
}
