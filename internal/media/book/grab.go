package book

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/rss"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Grab is Readarr's automatic search: search one book, then send its best accepted release to a download client.
type Grab struct {
	Search    Search
	Automatic dispatch.Automatic
}

// GrabResult is one automatic search: what was found and, when something was, what was sent.
type GrabResult struct {
	Search judge.Result
	dispatch.Best
}

// SearchAndSend searches itemID unless a download for it is already on its way, and sends the best accepted release, or
// holds it when origin may wait. Finding nothing acceptable is not an error.
func (g Grab) SearchAndSend(ctx context.Context, itemID int64, conns []judge.Conn, origin dispatch.GrabOrigin, at time.Time) (GrabResult, error) {
	var found judge.Result
	best, err := g.Automatic.Run(ctx, itemID, origin, at, func(ctx context.Context) ([]library.DecisionDetail, error) {
		var err error
		found, err = g.Search.SearchItem(ctx, itemID, conns, at)
		return found.Decisions, err
	})
	return GrabResult{Search: found, Best: best}, err
}

// JudgeFeedAndSend is the RSS sync's grab: releases a feed listed are judged against a wanted book and the best
// accepted one is sent as an RSS grab. Nothing is searched.
func (g Grab) JudgeFeedAndSend(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (GrabResult, error) {
	var found judge.Result
	best, err := g.Automatic.Run(ctx, itemID, dispatch.GrabRSS, at, func(ctx context.Context) ([]library.DecisionDetail, error) {
		var err error
		found, err = g.Search.JudgeFeed(ctx, itemID, conn, raws, at)
		return found.Decisions, err
	})
	return GrabResult{Search: found, Best: best}, err
}

// Searching is the book module's part of a search a person asks for: the whole book, as in Readarr.
type Searching struct {
	Acquire Grab
	Conns   func(ctx context.Context) ([]judge.Conn, error)
	Clock   clock.Clock
}

// SearchWanted implements media.Searching.
func (s Searching) SearchWanted(ctx context.Context, search media.WantedSearch) (judge.Result, error) {
	conns, err := s.prepare(ctx, search)
	if err != nil {
		return judge.Result{}, err
	}
	return s.Acquire.Search.SearchItem(ctx, search.ItemID, conns, s.Clock.Now())
}

// GrabWanted implements media.Searching.
func (s Searching) GrabWanted(ctx context.Context, search media.WantedSearch) (judge.Result, dispatch.Best, error) {
	conns, err := s.prepare(ctx, search)
	if err != nil {
		return judge.Result{}, dispatch.Best{}, err
	}
	result, err := s.Acquire.SearchAndSend(ctx, search.ItemID, conns, dispatch.GrabUser, s.Clock.Now())
	return result.Search, result.Best, err
}

func (s Searching) prepare(ctx context.Context, search media.WantedSearch) ([]judge.Conn, error) {
	if len(search.Terms) > 0 {
		return nil, fmt.Errorf("%w: a book is searched by its own title and author", media.ErrInvalidTerms)
	}
	if search.Part != "" {
		return nil, media.ErrWholeItemOnly
	}
	return media.SearchConns(ctx, "book", s.Conns)
}

// Feed is the book module's part of the RSS sync: a feed's releases are matched to the library's books by the title
// they name and offered to the ones that are wanted. Every field but Logger is required.
type Feed struct {
	Grab     Grab
	Reader   library.SeriesReader
	Indexers func(ctx context.Context) ([]judge.Conn, error)
	// Libraries lists the libraries of the format whose books take part.
	Libraries func() []int64
	Logger    *slog.Logger
}

// FeedIndexers implements rss.Feed.
func (f Feed) FeedIndexers(ctx context.Context) ([]judge.Conn, error) { return f.Indexers(ctx) }

// FeedRequest implements rss.Feed.
func (f Feed) FeedRequest() indexer.SearchRequest {
	return indexer.SearchRequest{Function: "search", Categories: f.Grab.Search.Format.FeedCategories}
}

// OfferFeed implements rss.Feed.
func (f Feed) OfferFeed(ctx context.Context, conn judge.Conn, raws []indexer.RawItem, at time.Time) rss.Tally {
	var tally rss.Tally
	if len(raws) == 0 || f.Libraries == nil {
		return tally
	}
	held := map[string][]int64{}
	for _, categoryID := range f.Libraries() {
		titles, err := f.Reader.TitlesOf(ctx, f.Grab.Search.Format.MediaType, categoryID)
		if err != nil {
			f.logger().Warn("rss sync: book titles", "error", err)
			return tally
		}
		for _, title := range titles {
			// A title of one short word ("It") names too many releases to be worth offering them.
			if clean := parsebook.CleanTitle(title.Title); len(clean) >= 4 {
				held[clean] = append(held[clean], title.WorkID)
			}
		}
	}
	byWork := map[int64][]indexer.RawItem{}
	var order []int64
	for _, raw := range raws {
		words := " " + parsebook.CleanTitle(strings.NewReplacer(".", " ", "_", " ").Replace(raw.Title)) + " "
		seen := map[int64]bool{}
		for title, works := range held {
			if !strings.Contains(words, " "+title+" ") {
				continue
			}
			for _, workID := range works {
				if seen[workID] {
					continue
				}
				seen[workID] = true
				if _, known := byWork[workID]; !known {
					order = append(order, workID)
				}
				byWork[workID] = append(byWork[workID], raw)
			}
		}
	}
	if len(order) == 0 {
		return tally
	}
	items, err := f.Reader.WantedItemsOf(ctx, order, at)
	if err != nil {
		f.logger().Warn("rss sync: wanted books", "error", err)
		return tally
	}
	for _, item := range items {
		result, err := f.Grab.JudgeFeedAndSend(ctx, item.ID, conn, byWork[item.WorkID], at)
		switch {
		case result.AlreadyInFlight:
			continue
		case err != nil:
			f.logger().Warn("rss sync: judging failed", "item_id", item.ID, "error", err)
			continue
		}
		if len(result.Search.Decisions) == 0 {
			continue
		}
		tally.Judged++
		if result.Sent != nil {
			tally.Sent++
			f.logger().Info("rss sync: release sent", "item_id", item.ID, "release", result.Sent.CandidateTitle)
		}
		if result.Held {
			tally.Held++
		}
	}
	return tally
}

func (f Feed) logger() *slog.Logger {
	if f.Logger == nil {
		return slog.Default()
	}
	return f.Logger
}
