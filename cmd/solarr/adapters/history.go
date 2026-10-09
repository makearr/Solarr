package adapters

import (
	"context"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/statistics"
)

// History satisfies apiserver.History over the download history in statistics.db.
type History struct {
	History statistics.History
}

func (a History) List(ctx context.Context, workID, beforeID int64, limit int) ([]apiserver.HistoryRow, error) {
	entries, err := a.History.List(ctx, workID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.HistoryRow, len(entries))
	for i, entry := range entries {
		row := apiserver.HistoryRow{
			ID: entry.ID, At: time.Unix(entry.At, 0).UTC(), Kind: strings.TrimPrefix(entry.Kind, "download."),
			CategoryID: entry.CategoryID, Album: entry.WorkTitle, Artist: entry.LeadName, ReleaseTitle: entry.ReleaseTitle,
			Indexer: entry.Indexer, Client: entry.Client, Detail: entry.Detail,
		}
		if entry.WorkID != 0 {
			workID := entry.WorkID
			row.WorkID = &workID
		}
		rows[i] = row
	}
	return rows, nil
}
