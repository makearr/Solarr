package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
)

// InboundWebhook is one incoming webhook: what a token may ask for. Only the token's hash is stored.
type InboundWebhook struct {
	ID                int64
	Name, TokenHash   string
	Enabled           bool
	Actions           []string
	CategoryIDs       []int64
	MediaServerID     int64
	LastUsedAt, Added int64
}

func inboundWebhookOf(row settingsdb.WebhookInbound) (InboundWebhook, error) {
	hook := InboundWebhook{
		ID: row.ID, Name: row.Name, TokenHash: row.TokenHash, Enabled: row.Enabled == 1, MediaServerID: row.MediaServerID,
		LastUsedAt: row.LastUsedAt.Int64, Added: row.CreatedAt,
	}
	if err := json.Unmarshal([]byte(row.Actions), &hook.Actions); err != nil {
		return InboundWebhook{}, fmt.Errorf("settings: incoming webhook %d actions: %w", row.ID, err)
	}
	if err := json.Unmarshal([]byte(row.CategoryIds), &hook.CategoryIDs); err != nil {
		return InboundWebhook{}, fmt.Errorf("settings: incoming webhook %d libraries: %w", row.ID, err)
	}
	return hook, nil
}

// InboundWebhooks lists every incoming webhook, oldest first.
func (c *Cache) InboundWebhooks(ctx context.Context) ([]InboundWebhook, error) {
	rows, err := c.queries.ListWebhookInbound(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list incoming webhooks: %w", err)
	}
	hooks := make([]InboundWebhook, 0, len(rows))
	for _, row := range rows {
		hook, err := inboundWebhookOf(row)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, hook)
	}
	return hooks, nil
}

// InboundWebhookByID reads one; ErrNotFound when there is none.
func (c *Cache) InboundWebhookByID(ctx context.Context, id int64) (InboundWebhook, error) {
	row, err := c.queries.GetWebhookInbound(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return InboundWebhook{}, fmt.Errorf("%w: incoming webhook %d", ErrNotFound, id)
	}
	if err != nil {
		return InboundWebhook{}, fmt.Errorf("settings: get incoming webhook %d: %w", id, err)
	}
	return inboundWebhookOf(row)
}

// SaveInboundWebhook inserts (ID 0, with the hash of its first token) or replaces what a token may do; a replace keeps the hash.
func (c *Cache) SaveInboundWebhook(ctx context.Context, hook InboundWebhook, at int64) (int64, error) {
	actions, _ := json.Marshal(nonNil(hook.Actions))
	libraries, _ := json.Marshal(nonNil(hook.CategoryIDs))
	id := hook.ID
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := c.queries.WithTx(tx)
		if id == 0 {
			var err error
			id, err = queries.InsertWebhookInbound(ctx, settingsdb.InsertWebhookInboundParams{
				Name: hook.Name, Enabled: boolToInt(hook.Enabled), TokenHash: hook.TokenHash, Actions: string(actions),
				CategoryIds: string(libraries), MediaServerID: hook.MediaServerID, CreatedAt: at,
			})
			return err
		}
		return queries.UpdateWebhookInbound(ctx, settingsdb.UpdateWebhookInboundParams{
			Name: hook.Name, Enabled: boolToInt(hook.Enabled), Actions: string(actions), CategoryIds: string(libraries), MediaServerID: hook.MediaServerID, ID: id,
		})
	})
	if err != nil {
		return 0, fmt.Errorf("settings: save incoming webhook: %w", err)
	}
	return id, nil
}

// SetInboundWebhookToken replaces the hash, so the old token stops working.
func (c *Cache) SetInboundWebhookToken(ctx context.Context, id int64, hash string) error {
	return c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).SetWebhookInboundToken(ctx, settingsdb.SetWebhookInboundTokenParams{TokenHash: hash, ID: id})
	})
}

// TouchInboundWebhook records when a token was last used.
func (c *Cache) TouchInboundWebhook(ctx context.Context, id, at int64) error {
	return c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).TouchWebhookInbound(ctx, settingsdb.TouchWebhookInboundParams{LastUsedAt: sql.NullInt64{Int64: at, Valid: true}, ID: id})
	})
}

// DeleteInboundWebhook removes one.
func (c *Cache) DeleteInboundWebhook(ctx context.Context, id int64) error {
	return c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).DeleteWebhookInbound(ctx, id)
	})
}
