package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// RecycleBinContents satisfies apiserver.RecycleBinContents over the recycle bin.
type RecycleBinContents struct{ Bin library.RecycleBin }

func (c RecycleBinContents) List(ctx context.Context) ([]apiserver.RecycleBinEntryRow, int64, int64, error) {
	entries, totalEntries, totalBytes, err := c.Bin.List(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	rows := make([]apiserver.RecycleBinEntryRow, len(entries))
	for i, entry := range entries {
		rows[i] = apiserver.RecycleBinEntryRow{
			ID: entry.ID, OriginalPath: entry.OriginalPath, Reason: string(entry.Reason), SizeBytes: entry.SizeBytes,
			RecycledAt: entry.RecycledAt, ExpiresAt: entry.ExpiresAt,
		}
	}
	return rows, totalEntries, totalBytes, nil
}

func (c RecycleBinContents) Empty(ctx context.Context) (int64, int64, int64, error) {
	result, err := c.Bin.Empty(ctx)
	return int64(result.Purged), result.BytesFreed, int64(len(result.Errors)), err
}
