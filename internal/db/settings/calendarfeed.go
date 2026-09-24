package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// SetCalendarFeed gives a user a new calendar feed token, revoking any previous one.
func (c *Cache) SetCalendarFeed(
	ctx context.Context, cl clock.Clock, userID int64, token secrets.Secret, tokenHash string,
) error {
	if _, found := c.Snapshot().User(userID); !found {
		return fmt.Errorf("%w: user %d", ErrNotFound, userID)
	}
	if c.cipher == nil {
		return ErrSecretsUnavailable
	}
	sealed, err := c.cipher.Seal(token)
	if err != nil {
		return fmt.Errorf("settings: seal calendar feed token: %w", err)
	}
	return c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).UpsertCalendarFeed(ctx, settingsdb.UpsertCalendarFeedParams{
			UserID: userID, TokenHash: tokenHash, TokenSealed: sealed, CreatedAt: cl.Now().Unix(),
		})
	})
}

// CalendarFeed answers the user's current token so the link can be shown again; false when none.
func (c *Cache) CalendarFeed(ctx context.Context, userID int64) (secrets.Secret, bool, error) {
	row, err := c.queries.GetCalendarFeed(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("settings: read calendar feed: %w", err)
	}
	if c.cipher == nil {
		return "", false, ErrSecretsUnavailable
	}
	token, err := c.cipher.Open(row.TokenSealed)
	if err != nil {
		return "", false, fmt.Errorf("settings: open calendar feed token: %w", err)
	}
	return token, true, nil
}

// DeleteCalendarFeed revokes the user's token; the old link stops working at once.
func (c *Cache) DeleteCalendarFeed(ctx context.Context, userID int64) error {
	return c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).DeleteCalendarFeed(ctx, userID)
	})
}

// UserByCalendarFeedHash is the user a feed token belongs to, ErrNotFound for a revoked or
// unknown one.
func (c *Cache) UserByCalendarFeedHash(ctx context.Context, tokenHash string) (User, error) {
	row, err := c.queries.GetCalendarFeedByHash(ctx, tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("%w: no such calendar feed", ErrNotFound)
	}
	if err != nil {
		return User{}, fmt.Errorf("settings: look up calendar feed: %w", err)
	}
	user, found := c.Snapshot().User(row.UserID)
	if !found {
		return User{}, fmt.Errorf("%w: user %d", ErrNotFound, row.UserID)
	}
	return user, nil
}
