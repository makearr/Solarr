package music

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/library"
)

// Grab is Lidarr's automatic search: search one album, then send its best accepted release to a
// download client. Interactive search is Search alone, with a person choosing what to send.
type Grab struct {
	Search   Search
	Dispatch Dispatch
}

// GrabResult is one automatic search: what was found and, when something was, what was sent.
type GrabResult struct {
	Search ItemSearchResult
	dispatch.Best
}

// SearchAndSend searches itemID unless a download for it is already on its way, and sends the
// best accepted release, or holds it when origin may wait. Finding nothing acceptable is not an error.
func (g Grab) SearchAndSend(
	ctx context.Context, itemID int64, indexers []IndexerConn, origin GrabOrigin, at time.Time,
) (GrabResult, error) {
	return g.run(ctx, itemID, origin, at, func(ctx context.Context) (ItemSearchResult, error) {
		return g.Search.SearchItem(ctx, itemID, indexers, at)
	})
}

// JudgeFeedAndSend is RSS sync's grab: releases a feed listed are judged against a wanted album,
// and the best accepted one is sent as an RSS grab. Nothing is searched.
func (g Grab) JudgeFeedAndSend(
	ctx context.Context, itemID int64, conn IndexerConn, raws []indexer.RawItem, at time.Time,
) (GrabResult, error) {
	return g.run(ctx, itemID, GrabRSS, at, func(ctx context.Context) (ItemSearchResult, error) {
		return g.Search.JudgeFeed(ctx, itemID, conn, raws, at)
	})
}

// run is dispatch.Automatic with the library's free space as its headroom; the search's result is kept even when it fails.
func (g Grab) run(ctx context.Context, itemID int64, origin GrabOrigin, at time.Time, search func(context.Context) (ItemSearchResult, error)) (GrabResult, error) {
	automatic := dispatch.Automatic{Dispatch: g.Dispatch, Name: "music"}
	if settings := g.Search.Settings; settings != nil {
		automatic.FreeSpace = func(categoryID int64) (int64, bool) { return settings.Snapshot().AcquisitionHeadroom(categoryID) }
	}
	var found ItemSearchResult
	best, err := automatic.Run(ctx, itemID, origin, at, func(ctx context.Context) ([]library.DecisionDetail, error) {
		var err error
		found, err = search(ctx)
		return found.Decisions, err
	})
	return GrabResult{Search: found, Best: best}, err
}
