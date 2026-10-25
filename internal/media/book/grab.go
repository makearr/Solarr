package book

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/rss"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Grab is Readarr's automatic search, the shared one over the book Search.
type Grab = works.Grab

// GrabResult is one automatic search: what was found and, when something was, what was sent.
type GrabResult = works.GrabResult

// NewSearching is the format's part of a search a person asks for: the whole book, as in Readarr.
func (f Format) NewSearching(acquire Grab, conns func(ctx context.Context) ([]judge.Conn, error), clock clock.Clock) works.Searching {
	return works.Searching{Acquire: acquire, Conns: conns, Clock: clock, Name: f.MediaType, Work: lower(f.One)}
}

// Feed is the format's part of the RSS sync, the shared one with books matched by a title they hold that a release
// names. Every field but Logger is required.
type Feed struct {
	Format   Format
	Grab     Grab
	Reader   library.SeriesReader
	Indexers func(ctx context.Context) ([]judge.Conn, error)
	// Libraries lists the libraries of the format whose books take part.
	Libraries func() []int64
	Logger    *slog.Logger
}

func (f Feed) shared() works.Feed {
	return works.Feed{
		Grab: f.Grab, Indexers: f.Indexers, Name: f.Format.MediaType, Logger: f.Logger,
		Request: indexer.SearchRequest{Function: "search", Categories: f.Format.FeedCategories},
		Matcher: f.matcher,
		Wanted: func(ctx context.Context, workIDs []int64, at time.Time) ([]works.WantedItem, error) {
			items, err := f.Reader.WantedItemsOf(ctx, workIDs, at)
			if err != nil {
				return nil, err
			}
			out := make([]works.WantedItem, len(items))
			for index, item := range items {
				out[index] = works.WantedItem{ID: item.ID, WorkID: item.WorkID}
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

// matcher names the books whose title a release holds as whole words; the search then holds it to the author too.
func (f Feed) matcher(ctx context.Context) (works.Matcher, error) {
	held := map[string][]int64{}
	if f.Libraries != nil {
		for _, categoryID := range f.Libraries() {
			titles, err := f.Reader.TitlesOf(ctx, f.Format.MediaType, categoryID)
			if err != nil {
				return nil, err
			}
			for _, title := range titles {
				// A title of one short word ("It") names too many releases to be worth offering them.
				if clean := parsebook.CleanTitle(title.Title); len(clean) >= 4 {
					held[clean] = append(held[clean], title.WorkID)
				}
			}
		}
	}
	return func(raw indexer.RawItem, _ time.Time) []int64 {
		words := " " + parsebook.CleanTitle(strings.NewReplacer(".", " ", "_", " ").Replace(raw.Title)) + " "
		seen := map[int64]bool{}
		var named []int64
		for title, workIDs := range held {
			if !strings.Contains(words, " "+title+" ") {
				continue
			}
			for _, workID := range workIDs {
				if !seen[workID] {
					seen[workID] = true
					named = append(named, workID)
				}
			}
		}
		return named
	}, nil
}
