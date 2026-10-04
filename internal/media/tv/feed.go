package tv

import (
	"context"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/rss"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parsetv"
)

// feedTVCategory is Newznab's TV, for an indexer with no categories set.
const feedTVCategory = 5000

// Feed is TV's part of the RSS sync: a feed's releases are matched to the library's series by the title they name
// and offered to that series' wanted seasons. Every field but Logger is required.
type Feed struct {
	Grab     Grab
	Reader   library.SeriesReader
	Indexers func(ctx context.Context) ([]judge.Conn, error)
	// Libraries lists the TV libraries whose series take part.
	Libraries func() []int64
	Logger    *slog.Logger
}

// FeedIndexers implements rss.Feed.
func (f Feed) FeedIndexers(ctx context.Context) ([]judge.Conn, error) { return f.Indexers(ctx) }

// FeedRequest implements rss.Feed.
func (Feed) FeedRequest() indexer.SearchRequest {
	return indexer.SearchRequest{Function: "tvsearch", Categories: []int{feedTVCategory}}
}

// OfferFeed implements rss.Feed.
func (f Feed) OfferFeed(ctx context.Context, conn judge.Conn, raws []indexer.RawItem, at time.Time) rss.Tally {
	var tally rss.Tally
	if len(raws) == 0 {
		return tally
	}
	series, err := f.seriesByTitle(ctx)
	if err != nil {
		f.logger().Warn("rss sync: series titles", "error", err)
		return tally
	}
	byWork := map[int64][]indexer.RawItem{}
	var order []int64
	for _, raw := range raws {
		info, ok := parsetv.Parse(raw.Title, at)
		if !ok {
			continue
		}
		for _, workID := range namedSeries(series, info) {
			if _, seen := byWork[workID]; !seen {
				order = append(order, workID)
			}
			byWork[workID] = append(byWork[workID], raw)
		}
	}
	if len(order) == 0 {
		return tally
	}
	items, err := f.Reader.WantedSeasonItems(ctx, order)
	if err != nil {
		f.logger().Warn("rss sync: wanted seasons", "error", err)
		return tally
	}
	for _, item := range items {
		f.offer(ctx, conn, item.ID, byWork[item.WorkID], at, &tally)
	}
	return tally
}

// seriesByTitle maps every cleaned title and alias of the TV libraries' series to the works that answer to it.
func (f Feed) seriesByTitle(ctx context.Context) (map[string][]int64, error) {
	byTitle := map[string][]int64{}
	for _, categoryID := range f.Libraries() {
		titles, err := f.Reader.Titles(ctx, categoryID)
		if err != nil {
			return nil, err
		}
		for _, title := range titles {
			clean := parsetv.CleanTitle(title.Title)
			if clean == "" {
				continue
			}
			known := false
			for _, workID := range byTitle[clean] {
				known = known || workID == title.WorkID
			}
			if !known {
				byTitle[clean] = append(byTitle[clean], title.WorkID)
			}
		}
	}
	return byTitle, nil
}

// namedSeries is the works a release's title names, with or without the year it carries.
func namedSeries(series map[string][]int64, info parsetv.Info) []int64 {
	if works := series[parsetv.CleanTitle(info.SeriesTitle)]; len(works) > 0 {
		return works
	}
	return series[parsetv.CleanTitle(info.TitleWithoutYear)]
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

// JudgeFeed judges releases a feed listed against one wanted season, exactly as a search's would be; nothing is
// searched and the season's search record is left alone.
func (s Search) JudgeFeed(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (ItemSearchResult, error) {
	item, err := s.Desired.Get(ctx, itemID)
	if err != nil {
		return ItemSearchResult{}, err
	}
	w, err := s.wantedFor(ctx, item, 0)
	if err != nil || len(w.missing) == 0 {
		return ItemSearchResult{}, err
	}
	measure, err := s.yardstick(ctx, item, w)
	if err != nil {
		return ItemSearchResult{}, err
	}
	return s.pipeline().Feed(ctx, itemID, conn, raws, measure.evaluate, at)
}

// JudgeFeedAndSend is the RSS sync's grab: a feed's releases are judged against a wanted season and the best
// accepted one is sent as an RSS grab. Nothing is searched.
func (g Grab) JudgeFeedAndSend(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (GrabResult, error) {
	if _, inFlight, err := g.Dispatch.Acquisitions.InFlightForItem(ctx, itemID); err != nil {
		return GrabResult{}, err
	} else if inFlight {
		return GrabResult{AlreadyInFlight: true}, nil
	}
	found, err := g.Search.JudgeFeed(ctx, itemID, conn, raws, at)
	if err != nil {
		return GrabResult{Search: found}, err
	}
	return g.sendBest(ctx, itemID, found, dispatch.GrabRSS, at)
}
