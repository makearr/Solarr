package movie

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
)

// Grab is Radarr's automatic search: search one movie, then send its best accepted release to a download client.
// Interactive search is Search alone, with a person choosing what to send.
type Grab struct {
	Search    Search
	Automatic dispatch.Automatic
}

// GrabResult is one automatic search: what was found and, when something was, what was sent.
type GrabResult struct {
	Search ItemSearchResult
	dispatch.Best
}

// SearchAndSend searches itemID unless a download for it is already on its way, and sends the best accepted release, or
// holds it when origin may wait. Finding nothing acceptable is not an error.
func (g Grab) SearchAndSend(ctx context.Context, itemID int64, conns []judge.Conn, origin dispatch.GrabOrigin, at time.Time) (GrabResult, error) {
	var found ItemSearchResult
	best, err := g.Automatic.Run(ctx, itemID, origin, at, func(ctx context.Context) ([]library.DecisionDetail, error) {
		var err error
		found, err = g.Search.SearchItem(ctx, itemID, conns, at)
		return found.Decisions, err
	})
	return GrabResult{Search: found, Best: best}, err
}

// JudgeFeedAndSend is the RSS sync's grab: releases a feed listed are judged against a wanted movie and the best
// accepted one is sent as an RSS grab. Nothing is searched.
func (g Grab) JudgeFeedAndSend(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (GrabResult, error) {
	var found ItemSearchResult
	best, err := g.Automatic.Run(ctx, itemID, dispatch.GrabRSS, at, func(ctx context.Context) ([]library.DecisionDetail, error) {
		var err error
		found, err = g.Search.JudgeFeed(ctx, itemID, conn, raws, at)
		return found.Decisions, err
	})
	return GrabResult{Search: found, Best: best}, err
}
