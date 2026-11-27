package settings

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// KeyShowCollections shows or hides collections in the interface; collection data keeps being collected either way.
const KeyShowCollections = "ui.show_collections"

// ShowCollections is whether the interface lists collections (navigation, item pages); off by default.
func (s *Snapshot) ShowCollections() bool { return s.policyBool(KeyShowCollections, false) }

// SetShowCollections shows or hides collections in the interface only; the collection jobs run regardless.
func (c *Cache) SetShowCollections(ctx context.Context, cl clock.Clock, enabled bool) error {
	return c.setPolicies(ctx, cl, map[string]string{KeyShowCollections: boolPolicy(enabled)})
}
