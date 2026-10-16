package movie

import (
	"context"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/rss"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/identify/parsetv"
)

// feedMovieCategory is Newznab's Movies, for an indexer with no categories set.
const feedMovieCategory = 2000

// Feed is the movie module's part of the RSS sync: a feed's releases are matched to the library's movies by the title
// and year they name and offered to the ones that are wanted. Every field but Logger is required.
type Feed struct {
	Grab     Grab
	Reader   library.SeriesReader
	Indexers func(ctx context.Context) ([]judge.Conn, error)
	// Libraries lists the movie libraries whose movies take part.
	Libraries func() []int64
	Logger    *slog.Logger
}

// FeedIndexers implements rss.Feed.
func (f Feed) FeedIndexers(ctx context.Context) ([]judge.Conn, error) { return f.Indexers(ctx) }

// FeedRequest implements rss.Feed.
func (Feed) FeedRequest() indexer.SearchRequest {
	return indexer.SearchRequest{Function: "movie", Categories: []int{feedMovieCategory}}
}

// OfferFeed implements rss.Feed.
func (f Feed) OfferFeed(ctx context.Context, conn judge.Conn, raws []indexer.RawItem, at time.Time) rss.Tally {
	var tally rss.Tally
	if len(raws) == 0 {
		return tally
	}
	held, err := f.heldByTitle(ctx)
	if err != nil {
		f.logger().Warn("rss sync: movie titles", "error", err)
		return tally
	}
	byWork := map[int64][]indexer.RawItem{}
	var order []int64
	for _, raw := range raws {
		info, ok := parsemovie.Parse(raw.Title)
		if !ok || info.Year == 0 {
			continue
		}
		for _, workID := range namedMovies(held, info) {
			if _, seen := byWork[workID]; !seen {
				order = append(order, workID)
			}
			byWork[workID] = append(byWork[workID], raw)
		}
	}
	if len(order) == 0 {
		return tally
	}
	items, err := f.Reader.WantedItemsOf(ctx, order, at)
	if err != nil {
		f.logger().Warn("rss sync: wanted movies", "error", err)
		return tally
	}
	for _, item := range items {
		f.offer(ctx, conn, item.ID, byWork[item.WorkID], at, &tally)
	}
	return tally
}

// heldTitle is one name a held movie answers to, with its year.
type heldTitle struct {
	workID int64
	year   int
}

// heldByTitle maps every cleaned title and alias of the movie libraries' movies to the works that answer to it.
func (f Feed) heldByTitle(ctx context.Context) (map[string][]heldTitle, error) {
	byTitle := map[string][]heldTitle{}
	for _, categoryID := range f.Libraries() {
		titles, err := f.Reader.TitlesOf(ctx, MediaType, categoryID)
		if err != nil {
			return nil, err
		}
		for _, title := range titles {
			clean := parsetv.CleanTitle(title.Title)
			if clean == "" {
				continue
			}
			byTitle[clean] = append(byTitle[clean], heldTitle{workID: title.WorkID, year: int(title.Year)})
		}
	}
	return byTitle, nil
}

// namedMovies is the works a release's title and year name.
func namedMovies(held map[string][]heldTitle, info parsemovie.Info) []int64 {
	var works []int64
	seen := map[int64]bool{}
	for _, title := range append([]string{info.Title}, info.AllTitles...) {
		for _, candidate := range held[parsetv.CleanTitle(title)] {
			if !seen[candidate.workID] && yearsAgree(candidate.year, info.Year) {
				seen[candidate.workID] = true
				works = append(works, candidate.workID)
			}
		}
	}
	return works
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
