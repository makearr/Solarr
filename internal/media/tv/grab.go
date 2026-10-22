package tv

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
)

// Grab is Sonarr's automatic search: search one season, then send its best accepted release to a download client.
// Interactive search is Search alone, with a person choosing what to send.
type Grab struct {
	Search   Search
	Dispatch dispatch.Dispatch
}

// GrabResult is one automatic search: what was found and, when something was, what was sent.
type GrabResult struct {
	Search ItemSearchResult
	dispatch.Best
}

// SearchAndSend searches itemID unless a download for it is already on its way, and sends the best accepted release,
// or holds it when origin may wait. Finding nothing acceptable is not an error.
func (g Grab) SearchAndSend(ctx context.Context, itemID int64, conns []judge.Conn, origin dispatch.GrabOrigin, at time.Time) (GrabResult, error) {
	return g.SearchEpisodeAndSend(ctx, itemID, 0, conns, origin, at)
}

// SearchEpisodeAndSend is SearchAndSend for one episode of the season; zero is what the season is missing.
func (g Grab) SearchEpisodeAndSend(ctx context.Context, itemID int64, episode int, conns []judge.Conn, origin dispatch.GrabOrigin, at time.Time) (GrabResult, error) {
	return g.run(ctx, itemID, origin, at, func(ctx context.Context) (ItemSearchResult, error) {
		return g.Search.SearchEpisode(ctx, itemID, episode, conns, at)
	})
}

// JudgeFeedAndSend is the RSS sync's grab: a feed's releases are judged against a wanted season and the best
// accepted one is sent as an RSS grab. Nothing is searched.
func (g Grab) JudgeFeedAndSend(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (GrabResult, error) {
	return g.run(ctx, itemID, dispatch.GrabRSS, at, func(ctx context.Context) (ItemSearchResult, error) {
		return g.Search.JudgeFeed(ctx, itemID, conn, raws, at)
	})
}

// run is dispatch.Automatic with the library's free space as its headroom; the search's result is kept even when it fails.
func (g Grab) run(ctx context.Context, itemID int64, origin dispatch.GrabOrigin, at time.Time, search func(context.Context) (ItemSearchResult, error)) (GrabResult, error) {
	automatic := dispatch.Automatic{Dispatch: g.Dispatch, Name: "tv"}
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
