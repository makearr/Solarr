package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/settingsdb"
)

// Authenticated is who a credential belongs to, and which key was used, so the
// key's last-used timestamp can be updated without a second lookup.
type Authenticated struct {
	User     User
	APIKeyID int64
}

// UserByAPIKeyHash resolves an API key to its owner.
func (c *Cache) UserByAPIKeyHash(ctx context.Context, keyHash string) (Authenticated, error) {
	row, err := c.queries.GetUserByAPIKeyHash(ctx, keyHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Authenticated{}, fmt.Errorf("%w: no such api key", ErrNotFound)
	}
	if err != nil {
		return Authenticated{}, fmt.Errorf("settings: look up api key: %w", err)
	}

	return Authenticated{
		User: User{
			ID:          row.ID,
			Username:    row.Username,
			HasPassword: row.PasswordHash.Valid && row.PasswordHash.String != "",
			Role:        Role(row.Role),
			CreatedAt:   time.Unix(row.CreatedAt, 0).UTC(),
			Tier:        Tier(row.UiTier),
		},
		APIKeyID: row.KeyID,
	}, nil
}

// CreateAPIKey stores a key's hash against a user. The plaintext never reaches
// this package.
func (c *Cache) CreateAPIKey(ctx context.Context, cl clock.Clock, userID int64,
	name, keyHash string) (APIKey, error) {
	if _, found := c.Snapshot().User(userID); !found {
		return APIKey{}, fmt.Errorf("%w: user %d", ErrNotFound, userID)
	}

	var created settingsdb.ApiKey
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		row, err := c.queries.WithTx(tx).CreateAPIKey(ctx, settingsdb.CreateAPIKeyParams{
			UserID: userID, Name: name, KeyHash: keyHash, CreatedAt: cl.Now().Unix(),
		})
		created = row
		return err
	})
	if err != nil {
		return APIKey{}, fmt.Errorf("settings: create api key %q: %w", name, err)
	}
	return APIKey{
		ID: created.ID, UserID: created.UserID, Name: created.Name,
		CreatedAt: time.Unix(created.CreatedAt, 0).UTC(),
	}, nil
}

// TouchAPIKey records that a key was used.
func (c *Cache) TouchAPIKey(ctx context.Context, cl clock.Clock, keyID int64) error {
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).TouchAPIKey(ctx, settingsdb.TouchAPIKeyParams{
			LastUsedAt: sql.NullInt64{Int64: cl.Now().Unix(), Valid: true},
			ID:         keyID,
		})
	})
	if err != nil {
		return fmt.Errorf("settings: touch api key %d: %w", keyID, err)
	}
	return nil
}

// ListAPIKeys returns a user's keys, without their hashes.
func (c *Cache) ListAPIKeys(ctx context.Context, userID int64) ([]APIKey, error) {
	rows, err := c.queries.ListAPIKeysForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("settings: list api keys for %d: %w", userID, err)
	}
	keys := make([]APIKey, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, APIKey{
			ID: row.ID, UserID: row.UserID, Name: row.Name,
			CreatedAt:  time.Unix(row.CreatedAt, 0).UTC(),
			LastUsedAt: unixOrZero(row.LastUsedAt),
		})
	}
	return keys, nil
}

// DeleteAPIKey revokes a key.
func (c *Cache) DeleteAPIKey(ctx context.Context, keyID int64) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).DeleteAPIKey(ctx, keyID)
	})
	if err != nil {
		return fmt.Errorf("settings: delete api key %d: %w", keyID, err)
	}
	return nil
}

func unixOrZero(value sql.NullInt64) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return time.Unix(value.Int64, 0).UTC()
}
