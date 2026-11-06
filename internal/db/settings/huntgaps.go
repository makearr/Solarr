package settings

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// KeyHuntGaps switches the automatic search of old missing items (the gap share of the hunt) on or off.
const KeyHuntGaps = "search.hunt_gaps"

// HuntGaps is whether the automatic search also looks for missing items released before the freshness window; on by default.
func (s *Snapshot) HuntGaps() bool { return s.policyBool(KeyHuntGaps, true) }

// SetHuntGaps turns the search of old missing items on or off; new releases and upgrades are searched either way.
func (c *Cache) SetHuntGaps(ctx context.Context, cl clock.Clock, enabled bool) error {
	return c.setPolicies(ctx, cl, map[string]string{KeyHuntGaps: boolPolicy(enabled)})
}
