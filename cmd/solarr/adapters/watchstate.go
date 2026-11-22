package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// WatchState adapts the stored watch state to the API's reads.
type WatchState struct{ Store library.Playback }

func (a WatchState) WatchOf(ctx context.Context, pairs []apiserver.WatchPair, workIDs []int64) (map[int64]apiserver.WorkWatchRow, error) {
	found, err := a.Store.WatchOf(ctx, libraryPairs(pairs), workIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]apiserver.WorkWatchRow, len(found))
	for id, watch := range found {
		out[id] = apiserver.WorkWatchRow(watch)
	}
	return out, nil
}

func (a WatchState) PartsOf(ctx context.Context, pairs []apiserver.WatchPair, workID int64) ([]apiserver.PartPlayRow, error) {
	rows, err := a.Store.PartsOf(ctx, libraryPairs(pairs), workID)
	if err != nil {
		return nil, err
	}
	out := make([]apiserver.PartPlayRow, len(rows))
	for i, row := range rows {
		out[i] = apiserver.PartPlayRow{
			PartID: row.PartID, MediaRef: row.MediaRef, Title: row.Title, Pair: apiserver.WatchPair{ServerID: row.ServerID, ServerUserID: row.ServerUserID},
			Played: row.Played, PositionMS: row.PositionMS, DurationMS: row.DurationMS, LastPlayedAt: row.LastPlayedAt,
		}
	}
	return out, nil
}

func (a WatchState) WatchedWorks(ctx context.Context, selection apiserver.WatchedSelection) ([]apiserver.WatchedWorkRow, error) {
	rows, err := a.Store.WatchedWorks(ctx, library.WatchedQuery{
		RootFolderIDs: selection.RootFolderIDs, CategoryIDs: selection.CategoryIDs, MediaTypes: selection.MediaTypes,
		Pairs: libraryPairs(selection.Pairs), LastPlayedBefore: selection.LastPlayedBefore, After: selection.After, Limit: selection.Limit, IDs: selection.IDs,
	})
	if err != nil {
		return nil, err
	}
	out := make([]apiserver.WatchedWorkRow, len(rows))
	for i, row := range rows {
		out[i] = apiserver.WatchedWorkRow(row)
	}
	return out, nil
}

func (a WatchState) WorkFileIDs(ctx context.Context, workID int64) ([]int64, error) {
	return a.Store.WorkFileIDs(ctx, workID)
}
