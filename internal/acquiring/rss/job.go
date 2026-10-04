// Package rss is the RSS sync of every media type: each indexer's newest releases, judged against what is wanted
// without a search (section 10.1: RSS and search converge on one pipeline).
package rss

import (
	"context"
	"log/slog"
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

// Job reads every indexer's feed for every media type and offers what is new.
type Job struct {
	config Config
	mu     sync.Mutex
	// newest is the latest release each feed was read up to; a restart reads it all again, which only judges
	// again what was judged before.
	newest map[readMark]time.Time
}

// NewJob builds the sync.
func NewJob(config Config) *Job {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Job{config: config, newest: map[readMark]time.Time{}}
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
		for _, conn := range conns {
			if err := ctx.Err(); err != nil {
				return err
			}
			request := source.Feed.FeedRequest()
			if len(conn.Categories) > 0 {
				request.Categories = conn.Categories
			}
			request.Limit = FeedLimit
			raws, err := conn.Client.Search(ctx, request)
			if err != nil {
				j.config.Logger.Warn("rss sync: feed failed", "media_type", source.Name, "indexer", conn.Name, "error", err)
				continue
			}
			unread := j.unread(readMark{source: source.Name, indexerID: conn.ID}, raws)
			releases += len(unread)
			tally := source.Feed.OfferFeed(ctx, conn, unread, j.config.Clock.Now())
			total.Judged, total.Sent, total.Held = total.Judged+tally.Judged, total.Sent+tally.Sent, total.Held+tally.Held
		}
		if total.Judged > 0 {
			j.config.Logger.Info("rss sync: done", "media_type", source.Name, "releases", releases,
				"items_judged", total.Judged, "sent", total.Sent, "held", total.Held)
		}
	}
	return nil
}

// unread keeps the releases newer than the last sync read from this feed, and moves its mark.
func (j *Job) unread(mark readMark, raws []indexer.RawItem) []indexer.RawItem {
	readTo := j.newest[mark]
	kept := raws[:0:0]
	for _, raw := range raws {
		if raw.PublishedAt.IsZero() || raw.PublishedAt.After(readTo) {
			kept = append(kept, raw)
		}
		if raw.PublishedAt.After(j.newest[mark]) {
			j.newest[mark] = raw.PublishedAt
		}
	}
	return kept
}
