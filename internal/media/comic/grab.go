package comic

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/cutoff"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/rss"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parsecomic"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// FeedCategories are the Newznab categories the type's releases are listed in: comics, then books in general.
var FeedCategories = []int{7030, 7000}

// Grab is the automatic search over the comic Search.
type Grab = works.Grab

// GrabResult is one automatic search: what was found and, when something was, what was sent.
type GrabResult = works.GrabResult

// NewSearching is the type's part of a search a person asks for: an item's missing parts, or one whole-numbered part.
func NewSearching(acquire Grab, conns func(ctx context.Context) ([]judge.Conn, error), clock clock.Clock) works.Searching {
	return works.Searching{Acquire: acquire, Conns: conns, Clock: clock, Name: MediaType, Work: "series", Part: wholeNumberOf}
}

// wholeNumberOf reads the number a part ref names ("issue:5"); a part with a decimal or a suffix is not searched alone.
func wholeNumberOf(ref string) (int, error) {
	_, number, _ := strings.Cut(ref, ":")
	whole, err := strconv.Atoi(number)
	if err != nil || whole < 0 {
		return 0, fmt.Errorf("comic: search: %q is not a part with a whole number", ref)
	}
	return whole, nil
}

// Feed is the type's part of the RSS sync, series matched by a title they hold that a release names.
type Feed struct {
	Grab      Grab
	Reader    library.SeriesReader
	Indexers  func(ctx context.Context) ([]judge.Conn, error)
	Libraries func() []int64
	Logger    *slog.Logger
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

// matcher names the series whose title a release's own series name equals.
func (f Feed) matcher(ctx context.Context) (works.Matcher, error) {
	held := map[string][]int64{}
	if f.Libraries != nil {
		for _, categoryID := range f.Libraries() {
			titles, err := f.Reader.TitlesHeld(ctx, MediaType, categoryID)
			if err != nil {
				return nil, err
			}
			for _, title := range titles {
				if key := titleKey(title.Title); len(key) >= 4 {
					held[key] = append(held[key], title.WorkID)
				}
			}
		}
	}
	return func(raw indexer.RawItem, _ time.Time) []int64 {
		return held[titleKey(parseSeries(raw.Title))]
	}, nil
}

// CutoffJob is the type's specialisation of the shared cutoff check.
type CutoffJob struct {
	Desired  library.Desired
	Reader   library.SeriesReader
	Settings *settings.Cache
	// Recycle moves a file to the recycle bin; replaced is true for an owned file an upgrade superseded.
	Recycle func(ctx context.Context, mediaObjectID, categoryID, keeperID int64, replaced bool) error
	Logger  *slog.Logger
}

func (j CutoffJob) shared() works.Cutoff {
	return works.Cutoff{
		Desired: j.Desired, Recycle: j.Recycle,
		Page: func(ctx context.Context, afterID, limit int64) ([]library.CutoffItem, error) {
			return j.Desired.CutoffPageOf(ctx, MediaType, afterID, limit)
		},
		Files: func(ctx context.Context, itemID int64) ([]works.File, error) { return partFiles(ctx, j.Reader, itemID) },
	}
}

// CutoffPage implements cutoff.Check.
func (j CutoffJob) CutoffPage(ctx context.Context, workID, afterID, limit int64) ([]library.CutoffItem, error) {
	return j.shared().CutoffPage(ctx, workID, afterID, limit)
}

// StateUnder implements cutoff.Previewing.
func (j CutoffJob) StateUnder(ctx context.Context, snapshot *settings.Snapshot, category settings.Category, item library.CutoffItem) (string, bool, error) {
	return j.shared().StateUnder(ctx, snapshot, category, item)
}

// CheckCutoff implements cutoff.Check.
func (j CutoffJob) CheckCutoff(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (cutoff.Outcome, error) {
	return j.shared().CheckCutoff(ctx, snapshot, item)
}

// partFiles are an item's files as the cutoff check judges them, one per part a file holds; Ref is the part's number.
func partFiles(ctx context.Context, reader library.SeriesReader, itemID int64) ([]works.File, error) {
	files, err := reader.ArchivePartFiles(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]works.File, len(files))
	for index, file := range files {
		_, number, _ := strings.Cut(file.Ref, ":")
		out[index] = works.File{
			MediaObjectID: file.MediaObjectID, Ref: number, AddedAt: file.AddedAt, Measured: file.Container != "",
			Monitored: file.Monitored, Facts: FileFacts(file),
		}
	}
	return out, nil
}

// FileFacts is what the probe measured of a comic file, as the facts a profile or a target judges: the archive format and
// the median page edges (Width is the short edge, Height the long one).
func FileFacts(file library.ArchivePartFile) score.Facts {
	return score.Facts{File: score.FileFacts{
		Container: file.Container, Extension: file.Container, Width: file.Short, Height: file.Long,
		Source: file.ReleaseSource, ReleaseGroup: file.ReleaseGroup, ProperRepack: file.Revision > 1, ReleaseKnown: file.Revision > 0,
	}}
}

// parseSeries is the series name a release title states.
func parseSeries(title string) string { return parsecomic.Parse(title).Series }
