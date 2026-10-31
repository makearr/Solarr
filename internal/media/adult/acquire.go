package adult

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/cutoff"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/rss"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parseadult"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Grab is the automatic search, the shared one over the scene Search.
type Grab = works.Grab

// GrabResult is one automatic search: what was found and, when something was, what was sent.
type GrabResult = works.GrabResult

// NewSearching is the module's part of a search a person asks for: the whole scene.
func NewSearching(acquire Grab, conns func(ctx context.Context) ([]judge.Conn, error), clock clock.Clock) works.Searching {
	return works.Searching{Acquire: acquire, Conns: conns, Clock: clock, Name: MediaType, Work: "scene"}
}

// Feed is the module's part of the RSS sync, the shared one with scenes matched by the studio and day a release names.
// Every field but Logger is required.
type Feed struct {
	Grab     Grab
	Reader   library.SeriesReader
	Indexers func(ctx context.Context) ([]judge.Conn, error)
	// Libraries lists the libraries whose scenes take part.
	Libraries func() []int64
	Logger    *slog.Logger
	Now       func() time.Time
}

func (f Feed) shared() works.Feed {
	return works.Feed{
		Grab: f.Grab, Indexers: f.Indexers, Name: MediaType, Logger: f.Logger,
		Request: indexer.SearchRequest{Function: "search", Categories: FeedCategories},
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

// heldScene is one scene the feed may offer a release to.
type heldScene struct {
	workID int64
	studio string
	title  string
}

// matcher names the scenes released on the day a release is dated; the search then holds it to the studio or title.
func (f Feed) matcher(ctx context.Context) (works.Matcher, error) {
	byDay := map[string][]heldScene{}
	if f.Libraries != nil {
		for _, categoryID := range f.Libraries() {
			dated, err := f.Reader.DatedOf(ctx, MediaType, categoryID, FieldStudio)
			if err != nil {
				return nil, err
			}
			for _, one := range dated {
				byDay[one.Date] = append(byDay[one.Date], heldScene{workID: one.WorkID, title: NameKey(one.Title), studio: compact(one.Field)})
			}
		}
	}
	return func(raw indexer.RawItem, _ time.Time) []int64 {
		info, ok := parseadult.ParseRelease(raw.Title)
		if !ok {
			return nil
		}
		words := " " + NameKey(raw.Title) + " "
		var named []int64
		for _, one := range byDay[info.Date] {
			if one.title != "" && strings.Contains(words, " "+one.title+" ") || one.studio != "" && compact(info.Studio) == one.studio {
				named = append(named, one.workID)
			}
		}
		return named
	}, nil
}

// CutoffJob is the module's specialisation of the shared cutoff check: a scene's files are versions of it, so two held on
// purpose are no contest and one at the cutoff completes it.
type CutoffJob struct {
	Desired  library.Desired
	Reader   library.SeriesReader
	Settings *settings.Cache
	// Recycle moves a file to the recycle bin; replaced is true for an owned file an upgrade superseded, false for a
	// download that measured no better. Nil leaves every file in place.
	Recycle func(ctx context.Context, mediaObjectID, categoryID int64, replaced bool) error
	Logger  *slog.Logger
}

func (j CutoffJob) shared() works.Cutoff {
	return works.Cutoff{
		Desired: j.Desired, Recycle: j.Recycle, Versions: true,
		Page: func(ctx context.Context, afterID, limit int64) ([]library.CutoffItem, error) {
			return j.Desired.CutoffPageOf(ctx, MediaType, afterID, limit)
		},
		Files: func(ctx context.Context, itemID int64) ([]works.File, error) { return itemFiles(ctx, j.Reader, itemID) },
	}
}

// CutoffPage implements cutoff.Check.
func (j CutoffJob) CutoffPage(ctx context.Context, workID, afterID, limit int64) ([]library.CutoffItem, error) {
	return j.shared().CutoffPage(ctx, workID, afterID, limit)
}

// CheckCutoff implements cutoff.Check.
func (j CutoffJob) CheckCutoff(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (cutoff.Outcome, error) {
	return j.shared().CheckCutoff(ctx, snapshot, item)
}
