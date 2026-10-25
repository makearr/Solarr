// Package works is what TV, movies, books and audiobooks do alike with the works of a library: add them, search for
// what they want and send the best release, read the indexers' feeds, settle and judge their files against the
// cutoff, place files a person names and read them again from their sources. Each media type keeps only its
// specialisation (what an item wants, what a release is, which files a work has, which ids name it); the rest is here.
// Music is not built on it: its albums are identified and grabbed track by track.
package works

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
)

// Searcher is a media type's search of one wanted item, as the grab, a person's search and the feed ask it.
type Searcher interface {
	// SearchPart searches the item, narrowed to one of its parts (an episode); zero is whatever the item lacks.
	SearchPart(ctx context.Context, itemID int64, part int, conns []judge.Conn, at time.Time) (judge.Result, error)
	// JudgeFeed judges releases a feed listed against the item as a search's would be; nothing is searched.
	JudgeFeed(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (judge.Result, error)
}

// Grab is the *Arrs' automatic search: search one wanted item, then send its best accepted release to a download
// client, or hold it when its origin may wait. Interactive search is the Searcher alone, a person choosing what to send.
type Grab struct {
	Search    Searcher
	Automatic dispatch.Automatic
}

// GrabResult is one automatic search: what was found and, when something was, what was sent.
type GrabResult struct {
	Search judge.Result
	dispatch.Best
}

// SearchAndSend searches itemID unless a download for it is already on its way, and sends the best accepted release.
// Finding nothing acceptable is not an error.
func (g Grab) SearchAndSend(ctx context.Context, itemID int64, conns []judge.Conn, origin dispatch.GrabOrigin, at time.Time) (GrabResult, error) {
	return g.SearchPartAndSend(ctx, itemID, 0, conns, origin, at)
}

// SearchPartAndSend is SearchAndSend for one part of the item; zero is what the item is missing.
func (g Grab) SearchPartAndSend(ctx context.Context, itemID int64, part int, conns []judge.Conn, origin dispatch.GrabOrigin, at time.Time) (GrabResult, error) {
	return g.run(ctx, itemID, origin, at, func(ctx context.Context) (judge.Result, error) {
		return g.Search.SearchPart(ctx, itemID, part, conns, at)
	})
}

// JudgeFeedAndSend is the RSS sync's grab: releases a feed listed are judged against a wanted item and the best
// accepted one is sent as an RSS grab. Nothing is searched.
func (g Grab) JudgeFeedAndSend(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (GrabResult, error) {
	return g.run(ctx, itemID, dispatch.GrabRSS, at, func(ctx context.Context) (judge.Result, error) {
		return g.Search.JudgeFeed(ctx, itemID, conn, raws, at)
	})
}

// run is dispatch.Automatic over one search; the search's result is kept even when it fails.
func (g Grab) run(ctx context.Context, itemID int64, origin dispatch.GrabOrigin, at time.Time, search func(context.Context) (judge.Result, error)) (GrabResult, error) {
	var found judge.Result
	best, err := g.Automatic.Run(ctx, itemID, origin, at, func(ctx context.Context) ([]library.DecisionDetail, error) {
		var err error
		found, err = search(ctx)
		return found.Decisions, err
	})
	return GrabResult{Search: found, Best: best}, err
}
