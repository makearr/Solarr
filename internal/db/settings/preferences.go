package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
)

// SetPreference stores one user's preference value as raw JSON, encoded by
// the caller. Deliberately bypasses Cache's own write path (c.write, which
// reloads the whole snapshot and can trigger a settings.db backup on every
// call): unlike every other setting in this package, a preference changes on
// ordinary UI interaction (ADR-54's last-visited level, on every navigation
// click) rather than a rare config edit, and nothing else in this process
// needs to see it through the snapshot.
func (c *Cache) SetPreference(ctx context.Context, userID int64, key, valueJSON string) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).SetPreference(ctx, settingsdb.SetPreferenceParams{
			UserID: userID, Key: key, ValueJson: valueJSON,
		})
	})
	if err != nil {
		return fmt.Errorf("settings: set preference %q for user %d: %w", key, userID, err)
	}
	return nil
}

// GetPreference reads one user's stored preference, or ok=false if never set.
func (c *Cache) GetPreference(ctx context.Context, userID int64, key string) (valueJSON string, ok bool, err error) {
	row, err := c.queries.GetPreference(ctx, settingsdb.GetPreferenceParams{UserID: userID, Key: key})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("settings: get preference %q for user %d: %w", key, userID, err)
	}
	return row.ValueJson, true, nil
}
