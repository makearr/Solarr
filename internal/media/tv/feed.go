package tv

import (
	"context"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/rss"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/media/works"
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

func (f Feed) shared() works.Feed {
	return works.Feed{
		Grab: f.Grab.shared(), Indexers: f.Indexers, Name: MediaType, Logger: f.Logger,
		Request: indexer.SearchRequest{Function: "tvsearch", Categories: []int{feedTVCategory}},
		Matcher: func(ctx context.Context) (works.Matcher, error) {
			series, err := f.seriesByTitle(ctx)
			if err != nil {
				return nil, err
			}
			return func(raw indexer.RawItem, at time.Time) []int64 {
				info, ok := parsetv.Parse(raw.Title, at)
				if !ok {
					return nil
				}
				return namedSeries(series, info)
			}, nil
		},
		Wanted: func(ctx context.Context, workIDs []int64, _ time.Time) ([]works.WantedItem, error) {
			seasons, err := f.Reader.WantedSeasonItems(ctx, workIDs)
			if err != nil {
				return nil, err
			}
			out := make([]works.WantedItem, len(seasons))
			for index, season := range seasons {
				out[index] = works.WantedItem{ID: season.ID, WorkID: season.WorkID}
			}
			return out, nil
		},
	}
}

// FeedIndexers implements rss.Feed.
func (f Feed) FeedIndexers(ctx context.Context) ([]judge.Conn, error) {
	return f.shared().FeedIndexers(ctx)
}

// FeedRequest implements rss.Feed.
func (f Feed) FeedRequest() indexer.SearchRequest { return f.shared().FeedRequest() }

// OfferFeed implements rss.Feed.
func (f Feed) OfferFeed(ctx context.Context, conn judge.Conn, raws []indexer.RawItem, at time.Time) rss.Tally {
	return f.shared().OfferFeed(ctx, conn, raws, at)
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
