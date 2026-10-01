package settings

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// ADR-36's own keys, the same instance_setting shape the cache budget and
// network settings already use.
const (
	KeyPriorityUserRequestWeight = "priority.weight.user_request"
	KeyPriorityNewReleaseWeight  = "priority.weight.new_release"
	KeyPriorityGapWeight         = "priority.weight.gap"
	KeyPriorityUpgradeWeight     = "priority.weight.upgrade"
	KeyPriorityNewReleaseDays    = "priority.new_release_days"
	// KeySearchesPerHour is the automatic search budget (section 9.4); zero turns it off.
	KeySearchesPerHour = "search.automatic_per_hour"
	// KeyHuntMode is the order the automatic search takes wanted items in (section 9.3).
	KeyHuntMode = "search.hunt_mode"
)

// HuntModes are the orders section 9.3 names; the first is the default.
var HuntModes = []string{"oldest_searched", "newest_searched", "oldest_release", "newest_release", "random_oldest_searched"}

// DefaultSearchesPerHour spares an indexer's daily API limit; maxSearchesPerHour bounds a typo.
const (
	DefaultSearchesPerHour = 12
	maxSearchesPerHour     = 1000
)

// ADR-36/ADR-55's own shipped defaults -- duplicated from
// internal/identify/priority's own DefaultWeights/DefaultNewReleaseDays
// rather than imported: internal/db/settings and internal/identify/priority
// share no dependency edge (architecture §2), the same reasoning
// schedule.go's own cron parser duplication already gives, and a stage 5/6
// caller composing internal/identify/priority.Weights from this policy is
// where the two shapes meet, not here.
const (
	DefaultPriorityUserRequestWeight = 40
	DefaultPriorityNewReleaseWeight  = 30
	DefaultPriorityGapWeight         = 20
	DefaultPriorityUpgradeWeight     = 10
	DefaultPriorityNewReleaseDays    = 7
)

// PrioritySettings is ADR-36's own policy: the four trigger weights (bigger
// is more urgent) and ADR-55's release-date freshness window, in days. One
// set for the whole instance -- unlike a quality or language profile, ADR-36
// never scoped these per category.
type PrioritySettings struct {
	UserRequestWeight, NewReleaseWeight, GapWeight, UpgradeWeight int64
	NewReleaseDays                                                int64
	// SearchesPerHour is how many albums are searched automatically per hour; nil on a write leaves it alone.
	SearchesPerHour *int64
	// HuntMode is one of HuntModes; empty on a write leaves it alone.
	HuntMode string
}

// PrioritySettings reads the configured policy, or the shipped defaults for
// whatever nothing has stored yet.
func (s *Snapshot) PrioritySettings() PrioritySettings {
	perHour := s.policyInt(KeySearchesPerHour, DefaultSearchesPerHour)
	return PrioritySettings{
		SearchesPerHour:   &perHour,
		HuntMode:          s.huntMode(),
		UserRequestWeight: s.policyInt(KeyPriorityUserRequestWeight, DefaultPriorityUserRequestWeight),
		NewReleaseWeight:  s.policyInt(KeyPriorityNewReleaseWeight, DefaultPriorityNewReleaseWeight),
		GapWeight:         s.policyInt(KeyPriorityGapWeight, DefaultPriorityGapWeight),
		UpgradeWeight:     s.policyInt(KeyPriorityUpgradeWeight, DefaultPriorityUpgradeWeight),
		NewReleaseDays:    s.policyInt(KeyPriorityNewReleaseDays, DefaultPriorityNewReleaseDays),
	}
}

func (s *Snapshot) huntMode() string {
	if mode := s.policy[KeyHuntMode]; slices.Contains(HuntModes, mode) {
		return mode
	}
	return HuntModes[0]
}

// SetPrioritySettings writes ADR-36's policy wholesale, like the cache budget
// and network settings (ADR-44: validated on write).
func (c *Cache) SetPrioritySettings(ctx context.Context, cl clock.Clock, ps PrioritySettings) error {
	if problems := validatePrioritySettings(ps); len(problems) > 0 {
		return problems
	}
	for key, value := range map[string]int64{
		KeyPriorityUserRequestWeight: ps.UserRequestWeight, KeyPriorityNewReleaseWeight: ps.NewReleaseWeight,
		KeyPriorityGapWeight: ps.GapWeight, KeyPriorityUpgradeWeight: ps.UpgradeWeight,
		KeyPriorityNewReleaseDays: ps.NewReleaseDays,
	} {
		if err := c.setPolicy(ctx, cl, key, strconv.FormatInt(value, 10)); err != nil {
			return err
		}
	}
	if ps.HuntMode != "" {
		if err := c.setPolicy(ctx, cl, KeyHuntMode, ps.HuntMode); err != nil {
			return err
		}
	}
	if ps.SearchesPerHour != nil {
		return c.setPolicy(ctx, cl, KeySearchesPerHour, strconv.FormatInt(*ps.SearchesPerHour, 10))
	}
	return nil
}

func validatePrioritySettings(ps PrioritySettings) config.Problems {
	var problems config.Problems
	for name, weight := range map[string]int64{
		"user_request_weight": ps.UserRequestWeight, "new_release_weight": ps.NewReleaseWeight,
		"gap_weight": ps.GapWeight, "upgrade_weight": ps.UpgradeWeight,
	} {
		if weight < 0 {
			problems = append(problems, config.Problem{
				Path: name, Value: strconv.FormatInt(weight, 10), Expectation: "zero or a positive weight",
			})
		}
	}
	if ps.NewReleaseDays <= 0 {
		problems = append(problems, config.Problem{
			Path: "new_release_days", Value: strconv.FormatInt(ps.NewReleaseDays, 10),
			Expectation: "a positive number of days",
		})
	}
	if perHour := ps.SearchesPerHour; perHour != nil && (*perHour < 0 || *perHour > maxSearchesPerHour) {
		problems = append(problems, config.Problem{
			Path: "searches_per_hour", Value: strconv.FormatInt(*perHour, 10),
			Expectation: "0 (off) to " + strconv.Itoa(maxSearchesPerHour),
		})
	}
	if ps.HuntMode != "" && !slices.Contains(HuntModes, ps.HuntMode) {
		problems = append(problems, config.Problem{
			Path: "hunt_mode", Value: ps.HuntMode, Expectation: "one of " + strings.Join(HuntModes, ", "),
		})
	}
	return problems
}
