package library

import (
	"context"
	"database/sql"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// WebhookDelivery is one delivery still to make.
type WebhookDelivery struct {
	ID, WebhookID, QueuedAt, Attempts, NextAt int64
	Event, Payload                            string
}

// WebhookPending keeps the outbox of outgoing webhooks.
type WebhookPending struct{ store *store.Store }

// NewWebhookPending wraps an opened library database.
func NewWebhookPending(opened *store.Store) WebhookPending { return WebhookPending{store: opened} }

// Add queues deliveries in chunks of 200.
func (w WebhookPending) Add(ctx context.Context, rows []WebhookDelivery) error {
	for start := 0; start < len(rows); start += pendingChunk {
		chunk := rows[start:min(start+pendingChunk, len(rows))]
		err := w.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for _, row := range chunk {
				if err := queries.AddWebhookPending(ctx, librarydb.AddWebhookPendingParams{WebhookID: row.WebhookID, Event: row.Event, Payload: row.Payload, QueuedAt: row.QueuedAt, NextAt: row.NextAt}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Due lists up to limit deliveries whose time has come, oldest first.
func (w WebhookPending) Due(ctx context.Context, now int64, limit int64) ([]WebhookDelivery, error) {
	rows, err := librarydb.New(w.store.Read()).ListDueWebhookPending(ctx, librarydb.ListDueWebhookPendingParams{NextAt: now, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]WebhookDelivery, len(rows))
	for index, row := range rows {
		out[index] = WebhookDelivery{ID: row.ID, WebhookID: row.WebhookID, Event: row.Event, Payload: row.Payload, QueuedAt: row.QueuedAt, Attempts: row.Attempts, NextAt: row.NextAt}
	}
	return out, nil
}

// Done removes a delivered (or dropped) row.
func (w WebhookPending) Done(ctx context.Context, id int64) error {
	return w.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteWebhookPending(ctx, id)
	})
}

// Retry counts a failed attempt and sets the next one.
func (w WebhookPending) Retry(ctx context.Context, id, nextAt int64) error {
	return w.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RetryWebhookPending(ctx, librarydb.RetryWebhookPendingParams{NextAt: nextAt, ID: id})
	})
}

// Clear drops every delivery of a deleted webhook.
func (w WebhookPending) Clear(ctx context.Context, webhookID int64) error {
	return w.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteWebhookPendingOf(ctx, webhookID)
	})
}

// Count answers how many deliveries wait for a webhook and when the first is due (0 when none).
func (w WebhookPending) Count(ctx context.Context, webhookID int64) (pending, nextAt int64, err error) {
	row, err := librarydb.New(w.store.Read()).CountWebhookPending(ctx, webhookID)
	return row.Pending, row.NextAt, err
}
