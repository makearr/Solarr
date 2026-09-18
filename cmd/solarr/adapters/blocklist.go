package adapters

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// Blocklist satisfies apiserver.Blocklist over library.BlocklistStore --
// section 10.11's failure memory, which had a complete store and no way to
// read it.
type Blocklist struct {
	Store library.BlocklistStore
}

func (a Blocklist) List(ctx context.Context, afterID int64, pageSize int) ([]apiserver.BlocklistRow, error) {
	entries, err := a.Store.List(ctx, afterID, int64(pageSize))
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.BlocklistRow, len(entries))
	for i, entry := range entries {
		row := apiserver.BlocklistRow{
			ID: entry.ID, IndexerID: entry.IndexerID, GUID: entry.GUID,
			ContentHash: entry.ContentHash, Title: entry.Title,
			Reason: string(entry.Reason), Source: string(entry.Source),
			CreatedAt: time.Unix(entry.CreatedAt, 0),
		}
		if entry.ExpiresAt != nil {
			expires := time.Unix(*entry.ExpiresAt, 0)
			row.ExpiresAt = &expires
		}
		rows[i] = row
	}
	return rows, nil
}

func (a Blocklist) Remove(ctx context.Context, id int64) error { return a.Store.Remove(ctx, id) }
