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
	"github.com/makearr/Solarr/internal/media/works"
)

// feedMovieCategory is Newznab's Movies, for an indexer with no categories set.
const feedMovieCategory = 2000

// Feed is the movie module's part of the RSS sync, the shared one with movies matched by the title and year a
// release names. Every field but Logger is required.
type Feed struct {
	Grab     Grab
	Reader   library.SeriesReader
	Indexers func(ctx context.Context) ([]judge.Conn, error)
	// Libraries lists the movie libraries whose movies take part.
	Libraries func() []int64
	Logger    *slog.Logger
}

func (f Feed) shared() works.Feed {
	return works.Feed{
		Grab: f.Grab, Indexers: f.Indexers, Name: MediaType, Logger: f.Logger,
		Request: indexer.SearchRequest{Function: "movie", Categories: []int{feedMovieCategory}},
		Matcher: func(ctx context.Context) (works.Matcher, error) {
			held, err := f.heldByTitle(ctx)
			if err != nil {
				return nil, err
			}
			return func(raw indexer.RawItem, _ time.Time) []int64 {
				info, ok := parsemovie.Parse(raw.Title)
				if !ok || info.Year == 0 {
					return nil
				}
				return namedMovies(held, info)
			}, nil
		},
		Wanted: f.wanted,
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

// wanted is the movies' wanted items, as the shared feed offers them releases.
func (f Feed) wanted(ctx context.Context, workIDs []int64, at time.Time) ([]works.WantedItem, error) {
	items, err := f.Reader.WantedItemsOf(ctx, workIDs, at)
	if err != nil {
		return nil, err
	}
	out := make([]works.WantedItem, len(items))
	for index, item := range items {
		out[index] = works.WantedItem{ID: item.ID, WorkID: item.WorkID}
	}
	return out, nil
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
