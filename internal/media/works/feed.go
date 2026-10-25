package works

import (
	"context"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/rss"
)

// Matcher answers the works of the libraries one release names, by the media type's own reading of its name.
type Matcher func(raw indexer.RawItem, at time.Time) []int64

// WantedItem is one wanted item of a work a feed may offer releases to.
type WantedItem struct {
	ID, WorkID int64
}

// Feed is a media type's part of the RSS sync: a feed's releases are matched to the libraries' works and offered to
// their wanted items. Every field but Logger is required.
type Feed struct {
	Grab     Grab
	Indexers func(ctx context.Context) ([]judge.Conn, error)
	// Request is the feed read from each indexer: the type's search function and categories.
	Request indexer.SearchRequest
	// Matcher builds the type's matcher over what the libraries hold now, once per feed.
	Matcher func(ctx context.Context) (Matcher, error)
	// Wanted lists the wanted items of the works, in the works' order.
	Wanted func(ctx context.Context, workIDs []int64, at time.Time) ([]WantedItem, error)
	// Name is the type's word for its work in the log ("movie").
	Name   string
	Logger *slog.Logger
}

// FeedIndexers implements rss.Feed.
func (f Feed) FeedIndexers(ctx context.Context) ([]judge.Conn, error) { return f.Indexers(ctx) }

// FeedRequest implements rss.Feed.
func (f Feed) FeedRequest() indexer.SearchRequest { return f.Request }

// OfferFeed implements rss.Feed.
func (f Feed) OfferFeed(ctx context.Context, conn judge.Conn, raws []indexer.RawItem, at time.Time) rss.Tally {
	var tally rss.Tally
	if len(raws) == 0 {
		return tally
	}
	names, err := f.Matcher(ctx)
	if err != nil {
		f.logger().Warn("rss sync: titles", "media_type", f.Name, "error", err)
		return tally
	}
	byWork := map[int64][]indexer.RawItem{}
	var order []int64
	for _, raw := range raws {
		for _, workID := range names(raw, at) {
			if _, seen := byWork[workID]; !seen {
				order = append(order, workID)
			}
			byWork[workID] = append(byWork[workID], raw)
		}
	}
	if len(order) == 0 {
		return tally
	}
	items, err := f.Wanted(ctx, order, at)
	if err != nil {
		f.logger().Warn("rss sync: wanted items", "media_type", f.Name, "error", err)
		return tally
	}
	for _, item := range items {
		f.offer(ctx, conn, item.ID, byWork[item.WorkID], at, &tally)
	}
	return tally
}

func (f Feed) offer(ctx context.Context, conn judge.Conn, itemID int64, raws []indexer.RawItem, at time.Time, tally *rss.Tally) {
	result, err := f.Grab.JudgeFeedAndSend(ctx, itemID, conn, raws, at)
	switch {
	case result.AlreadyInFlight:
		return
	case err != nil:
		f.logger().Warn("rss sync: judging failed", "item_id", itemID, "error", err)
		return
	}
	if len(result.Search.Decisions) == 0 {
		return
	}
	tally.Judged++
	if result.Sent != nil {
		tally.Sent++
		f.logger().Info("rss sync: release sent", "item_id", itemID, "release", result.Sent.CandidateTitle)
	}
	if result.Held {
		tally.Held++
	}
}

func (f Feed) logger() *slog.Logger {
	if f.Logger == nil {
		return slog.Default()
	}
	return f.Logger
}
