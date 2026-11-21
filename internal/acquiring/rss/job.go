// Package rss is the RSS sync of every media type: each indexer's newest releases, judged against what is wanted
// without a search (section 10.1: RSS and search converge on one pipeline).
package rss

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// JobType is the one RSS sync, Lidarr's and Sonarr's alike.
const JobType = jobs.Type("acquire.rss_sync")

// Definition runs one sync at a time; the next scheduled sync is its retry.
var Definition = jobs.Definition{
	Name: "RSS sync", Type: JobType, Pool: jobs.PoolSearch, Mutating: true, MaxAttempts: 1,
}

// FeedLimit is how many of an indexer's newest releases one sync reads, as the *Arrs' RSS does.
const FeedLimit = 100

// Tally is what offering one feed did.
type Tally struct {
	// Judged counts wanted items a feed's releases were judged against.
	Judged int
	Sent   int
	Held   int
}

// Feed is what a media type brings to the sync: where its releases are listed and who wants them.
type Feed interface {
	// FeedIndexers lists the enabled indexers as this media type asks them, with its own categories.
	FeedIndexers(ctx context.Context) ([]judge.Conn, error)
	// FeedRequest asks an indexer for this media type's newest releases; Categories are the default when an
	// indexer names none, without which a feed is mostly another media type's.
	FeedRequest() indexer.SearchRequest
	// OfferFeed judges releases against the wanted items they could be for and sends what one accepts.
	OfferFeed(ctx context.Context, conn judge.Conn, raws []indexer.RawItem, at time.Time) Tally
}

// Source is one media type's feed, named for the log and for remembering how far each indexer was read.
type Source struct {
	Name string
	Feed Feed
}

// Config is what a Job needs; every field but Logger is required.
type Config struct {
	// Sources lists the media types that take part, read on every sync.
	Sources func() []Source
	Clock   clock.Clock
	Logger  *slog.Logger
}

// readMark is one media type's reading of one indexer.
type readMark struct {
	source    string
	indexerID int64
}

// conditionalSearcher is an indexer connection that can ask "anything new since?" with HTTP validators.
type conditionalSearcher interface {
	SearchIfChanged(ctx context.Context, request indexer.SearchRequest, seen indexer.Validators) ([]indexer.RawItem, indexer.Validators, bool, error)
}

// Job reads every indexer's feed for every media type and offers what is new.
type Job struct {
	config Config
	mu     sync.Mutex
	// seen remembers which releases each feed has offered; a restart reads it all again, which only judges
	// again what was judged before.
	seen map[readMark]*seenReleases
	// validators are what each feed last answered, sent back so an indexer that honours them answers 304.
	validators map[readMark]indexer.Validators
}

// NewJob builds the sync.
func NewJob(config Config) *Job {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Job{config: config, seen: map[readMark]*seenReleases{}, validators: map[readMark]indexer.Validators{}}
}

// Run reads each media type's feed from each of its indexers and offers the releases not read before. One indexer
// or one media type failing does not stop the others.
func (j *Job) Run(ctx context.Context, _ jobs.Job) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, source := range j.config.Sources() {
		conns, err := source.Feed.FeedIndexers(ctx)
		if err != nil {
			j.config.Logger.Warn("rss sync: indexers", "media_type", source.Name, "error", err)
			continue
		}
		releases := 0
		var total Tally
		read := make([]string, 0, len(conns))
		for _, conn := range conns {
			if err := ctx.Err(); err != nil {
				return err
			}
			request := source.Feed.FeedRequest()
			if len(conn.Categories) > 0 {
				request.Categories = conn.Categories
			}
			request.Limit = FeedLimit
			mark := readMark{source: source.Name, indexerID: conn.ID}
			raws, unchanged, err := j.fetch(ctx, mark, conn, request)
			if err != nil {
				j.config.Logger.Warn("rss sync: feed failed", "media_type", source.Name, "indexer", conn.Name, "error", err)
				continue
			}
			if unchanged {
				read = append(read, conn.Name+" not modified")
				continue
			}
			raws = j.morePages(ctx, mark, conn, request, raws)
			unread := j.unread(source.Name, conn.Name, mark, raws)
			releases += len(unread)
			read = append(read, fmt.Sprintf("%s %d new of %d", conn.Name, len(unread), len(raws)))
			tally := source.Feed.OfferFeed(ctx, conn, unread, j.config.Clock.Now())
			// Remembered only once offered, so a cancelled sync offers them again.
			j.seenOf(mark).add(unread)
			total.Judged, total.Sent, total.Held = total.Judged+tally.Judged, total.Sent+tally.Sent, total.Held+tally.Held
		}
		// Logged when nothing matched too: the feed being read is what tells a working RSS from a broken one.
		j.config.Logger.Info("rss sync: done", "media_type", source.Name, "releases", releases, "indexers", strings.Join(read, ", "),
			"items_judged", total.Judged, "sent", total.Sent, "held", total.Held)
	}
	return nil
}

// fetch reads one feed, conditionally when the connection can; unchanged is a 304.
func (j *Job) fetch(ctx context.Context, mark readMark, conn judge.Conn, request indexer.SearchRequest) (raws []indexer.RawItem, unchanged bool, err error) {
	conditional, ok := conn.Client.(conditionalSearcher)
	if !ok {
		raws, err = conn.Client.Search(ctx, request)
		return raws, false, err
	}
	raws, validators, unchanged, err := conditional.SearchIfChanged(ctx, request, j.validators[mark])
	if err == nil {
		j.validators[mark] = validators
	}
	return raws, unchanged, err
}

// maxFeedPages bounds how many pages one sync reads when the feed moved on by more than a page.
const maxFeedPages = 5

// morePages reads the pages behind a full first page that holds nothing seen, until one holds a seen release, comes up short
// or adds nothing (an indexer that ignores the offset), so a busy feed is not skipped over (RSS-1).
func (j *Job) morePages(ctx context.Context, mark readMark, conn judge.Conn, request indexer.SearchRequest, first []indexer.RawItem) []indexer.RawItem {
	seen := j.seenOf(mark)
	all, last := first, first
	have := make(map[string]struct{}, len(first))
	for _, raw := range first {
		have[releaseKey(raw)] = struct{}{}
	}
	for page := 1; page < maxFeedPages; page++ {
		if len(seen.order) == 0 || len(last) < FeedLimit || slices.ContainsFunc(last, seen.has) {
			break
		}
		request.Offset = page * FeedLimit
		next, err := conn.Client.Search(ctx, request)
		if err != nil {
			j.config.Logger.Warn("rss sync: a further feed page failed", "indexer", conn.Name, "error", err)
			break
		}
		added := 0
		for _, raw := range next {
			if _, dup := have[releaseKey(raw)]; !dup {
				have[releaseKey(raw)] = struct{}{}
				all = append(all, raw)
				added++
			}
		}
		if added == 0 {
			break
		}
		last = next
	}
	return all
}

// unread keeps the releases this feed has not offered before. The feed is ordered by when the indexer listed a
// release, not by its post date, so a release posted earlier than the newest one seen is still new.
func (j *Job) unread(source, indexerName string, mark readMark, raws []indexer.RawItem) []indexer.RawItem {
	seen := j.seenOf(mark)
	kept := raws[:0:0]
	for _, raw := range raws {
		if !seen.has(raw) {
			kept = append(kept, raw)
		}
	}
	if len(seen.order) > 0 && len(raws) >= FeedLimit && len(kept) == len(raws) {
		j.config.Logger.Warn("rss sync: feed moved further than one page between syncs", "media_type", source, "indexer", indexerName)
	}
	return kept
}

func (j *Job) seenOf(mark readMark) *seenReleases {
	seen, ok := j.seen[mark]
	if !ok {
		seen = &seenReleases{keys: map[string]struct{}{}}
		j.seen[mark] = seen
	}
	return seen
}

// maxSeenReleases bounds what one feed remembers: ten pages of it.
const maxSeenReleases = 10 * FeedLimit

// seenReleases is a bounded set of release keys, oldest forgotten first.
type seenReleases struct {
	keys  map[string]struct{}
	order []string
}

// releaseKey names a release across syncs: its GUID, else its title and link.
func releaseKey(raw indexer.RawItem) string {
	if raw.GUID != "" {
		return raw.GUID
	}
	return raw.Title + "|" + raw.Link
}

func (s *seenReleases) has(raw indexer.RawItem) bool {
	_, found := s.keys[releaseKey(raw)]
	return found
}

func (s *seenReleases) add(raws []indexer.RawItem) {
	for _, raw := range raws {
		key := releaseKey(raw)
		if _, found := s.keys[key]; found {
			continue
		}
		s.keys[key] = struct{}{}
		s.order = append(s.order, key)
	}
	for len(s.order) > maxSeenReleases {
		delete(s.keys, s.order[0])
		s.order = s.order[1:]
	}
}
