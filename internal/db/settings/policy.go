package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/MediaNexus/internal/db/store/gen/settingsdb"
	"github.com/makearr/MediaNexus/internal/platform/clock"
)

// KeyAuthRequired is whether a caller must present a credential at all. Policy
// rather than bootstrap, so the interface can change it (ADR-21): config.yml
// seeds it on the first run and is not consulted again.
const KeyAuthRequired = "auth.required"

// AuthRequired reports whether callers must authenticate. Absent means yes: an
// instance whose policy row is missing protects itself rather than opening.
func (s *Snapshot) AuthRequired() bool {
	value, found := s.policy[KeyAuthRequired]
	if !found {
		return true
	}
	return value != "false"
}

// SetAuthRequired stores the policy and republishes the snapshot.
func (c *Cache) SetAuthRequired(ctx context.Context, cl clock.Clock, required bool) error {
	return c.setPolicy(ctx, cl, KeyAuthRequired, strconv.FormatBool(required))
}

// SeedAuthRequired writes what config.yml asked for, but only when nothing has
// been stored yet. A later edit to config.yml therefore cannot quietly reopen an
// instance that was closed from the interface, or the reverse.
func (c *Cache) SeedAuthRequired(ctx context.Context, cl clock.Clock, required bool) error {
	if _, err := c.queries.GetInstanceSetting(ctx, KeyAuthRequired); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("settings: read %s: %w", KeyAuthRequired, err)
	}
	return c.SetAuthRequired(ctx, cl, required)
}

func (c *Cache) setPolicy(ctx context.Context, cl clock.Clock, key, value string) error {
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetInstanceSetting(ctx, settingsdb.SetInstanceSettingParams{
			Key: key, ValueJson: value, UpdatedAt: cl.Now().Unix(),
		})
	})
}
