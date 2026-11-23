package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// Webhook is one outgoing webhook; URL and Secret are open here and sealed at rest.
type Webhook struct {
	ID                         int64
	Name, Preset, LastError    string
	Enabled                    bool
	URL, Secret                secrets.Secret
	Events                     []string
	CategoryIDs                []int64
	IncludePaths, IncludeAdult bool
	LastStatus                 int
	LastAt, CreatedAt          int64
}

func (c *Cache) webhookOf(row settingsdb.Webhook) (Webhook, error) {
	if c.cipher == nil {
		return Webhook{}, ErrSecretsUnavailable
	}
	hook := Webhook{
		ID: row.ID, Name: row.Name, Preset: row.Preset, LastError: row.LastError, Enabled: row.Enabled == 1,
		IncludePaths: row.IncludePaths == 1, IncludeAdult: row.IncludeAdult == 1, LastStatus: int(row.LastStatus.Int64), LastAt: row.LastAt.Int64, CreatedAt: row.CreatedAt,
	}
	address, err := c.cipher.Open(row.Url)
	if err != nil {
		return Webhook{}, fmt.Errorf("settings: open webhook %d address: %w", row.ID, err)
	}
	hook.URL = address
	if len(row.Secret) > 0 {
		if hook.Secret, err = c.cipher.Open(row.Secret); err != nil {
			return Webhook{}, fmt.Errorf("settings: open webhook %d secret: %w", row.ID, err)
		}
	}
	if err := json.Unmarshal([]byte(row.Events), &hook.Events); err != nil {
		return Webhook{}, fmt.Errorf("settings: webhook %d events: %w", row.ID, err)
	}
	if err := json.Unmarshal([]byte(row.CategoryIds), &hook.CategoryIDs); err != nil {
		return Webhook{}, fmt.Errorf("settings: webhook %d libraries: %w", row.ID, err)
	}
	return hook, nil
}

// Webhooks lists every webhook, oldest first.
func (c *Cache) Webhooks(ctx context.Context) ([]Webhook, error) {
	rows, err := c.queries.ListWebhooks(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list webhooks: %w", err)
	}
	hooks := make([]Webhook, 0, len(rows))
	for _, row := range rows {
		hook, err := c.webhookOf(row)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, hook)
	}
	return hooks, nil
}

// WebhookByID reads one webhook; ErrNotFound when there is none.
func (c *Cache) WebhookByID(ctx context.Context, id int64) (Webhook, error) {
	row, err := c.queries.GetWebhook(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Webhook{}, fmt.Errorf("%w: webhook %d", ErrNotFound, id)
	}
	if err != nil {
		return Webhook{}, fmt.Errorf("settings: get webhook %d: %w", id, err)
	}
	return c.webhookOf(row)
}

// SaveWebhook inserts a webhook (ID 0) or replaces one; an empty address or secret on a replace keeps the sealed one.
func (c *Cache) SaveWebhook(ctx context.Context, hook Webhook, at int64) (int64, error) {
	if c.cipher == nil {
		return 0, ErrSecretsUnavailable
	}
	if hook.ID != 0 {
		held, err := c.WebhookByID(ctx, hook.ID)
		if err != nil {
			return 0, err
		}
		if hook.URL == "" {
			hook.URL = held.URL
		}
		if hook.Secret == "" {
			hook.Secret = held.Secret
		}
	}
	address, err := c.cipher.Seal(hook.URL)
	if err != nil {
		return 0, err
	}
	var secret []byte
	if hook.Secret != "" {
		if secret, err = c.cipher.Seal(hook.Secret); err != nil {
			return 0, err
		}
	}
	events, _ := json.Marshal(nonNil(hook.Events))
	libraries, _ := json.Marshal(nonNil(hook.CategoryIDs))
	id := hook.ID
	err = c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := c.queries.WithTx(tx)
		if id == 0 {
			var err error
			id, err = queries.InsertWebhook(ctx, settingsdb.InsertWebhookParams{
				Name: hook.Name, Enabled: boolToInt(hook.Enabled), Preset: hook.Preset, Url: address, Secret: secret, Events: string(events), CategoryIds: string(libraries),
				IncludePaths: boolToInt(hook.IncludePaths), IncludeAdult: boolToInt(hook.IncludeAdult), CreatedAt: at,
			})
			return err
		}
		return queries.UpdateWebhook(ctx, settingsdb.UpdateWebhookParams{
			Name: hook.Name, Enabled: boolToInt(hook.Enabled), Preset: hook.Preset, Url: address, Secret: secret, Events: string(events), CategoryIds: string(libraries),
			IncludePaths: boolToInt(hook.IncludePaths), IncludeAdult: boolToInt(hook.IncludeAdult), ID: id,
		})
	})
	if err != nil {
		return 0, fmt.Errorf("settings: save webhook: %w", err)
	}
	return id, nil
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// DeleteWebhook removes a webhook.
func (c *Cache) DeleteWebhook(ctx context.Context, id int64) error {
	return c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).DeleteWebhook(ctx, id)
	})
}

// SetWebhookEnabled switches a webhook on or off; a 410 from the receiver switches it off.
func (c *Cache) SetWebhookEnabled(ctx context.Context, id int64, enabled bool) error {
	return c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).SetWebhookEnabled(ctx, settingsdb.SetWebhookEnabledParams{Enabled: boolToInt(enabled), ID: id})
	})
}

// SetWebhookResult records the last delivery: the status (0 when none was answered) and the error.
func (c *Cache) SetWebhookResult(ctx context.Context, id int64, status int, message string, at int64) error {
	return c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).SetWebhookResult(ctx, settingsdb.SetWebhookResultParams{
			LastStatus: sql.NullInt64{Int64: int64(status), Valid: true}, LastError: message, LastAt: sql.NullInt64{Int64: at, Valid: true}, ID: id,
		})
	})
}
