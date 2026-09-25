package adapters

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/jobs"
)

// History satisfies apiserver.History over the durable event log in jobs.db.
type History struct {
	Log jobs.History
}

func (a History) List(ctx context.Context, workID, beforeID int64, limit int) ([]apiserver.HistoryRow, error) {
	kinds := make([]string, len(download.HistoryKinds))
	for i, kind := range download.HistoryKinds {
		kinds[i] = string(kind)
	}
	query := jobs.HistoryQuery{Kinds: kinds, BeforeID: beforeID, Limit: limit}
	if workID != 0 {
		query.SubjectType, query.SubjectID = "work", workID
	}
	events, err := a.Log.List(ctx, query)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.HistoryRow, len(events))
	for i, event := range events {
		var data download.HistoryData
		// An unreadable payload still says what happened and when.
		_ = json.Unmarshal([]byte(event.Data), &data)
		row := apiserver.HistoryRow{
			ID: event.ID, At: time.Unix(event.At, 0).UTC(),
			Kind:       strings.TrimPrefix(event.Kind, "download."),
			CategoryID: data.CategoryID, Album: data.Album, Artist: data.Artist, ReleaseTitle: data.ReleaseTitle,
			Indexer: data.Indexer, Client: data.Client, Detail: data.Detail,
		}
		if event.SubjectType == "work" {
			subjectID := event.SubjectID
			row.WorkID = &subjectID
		}
		rows[i] = row
	}
	return rows, nil
}
