package music

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/identify/parse"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// RSSJobType is Lidarr's RSS Sync: each indexer's newest music releases, grabbed for wanted albums
// without a search (section 10.1: RSS and search converge on one pipeline).
const RSSJobType = jobs.Type("music.rss_sync")

// RSSDefinition runs one sync at a time; the next scheduled sync is its retry.
var RSSDefinition = jobs.Definition{
	Name: "RSS sync", Type: RSSJobType, Pool: jobs.PoolSearch, Mutating: true, MaxAttempts: 1,
}

// rssAudioCategory is Newznab's Audio: without it an indexer's feed is mostly television (2026-09-30).
const rssAudioCategory = 3000

// rssFeedLimit is how many of an indexer's newest releases one sync reads, as Lidarr's RSS does.
const rssFeedLimit = 100

// rssAlbumsPerArtist bounds the wanted albums one release artist is judged against.
const rssAlbumsPerArtist = 200

// RSSConfig is what an RSSJob needs; every field but Logger is required.
type RSSConfig struct {
	Grab     Grab
	Indexers IndexerSource
	// WantedByArtist answers the wanted albums credited to an artist of that name.
	WantedByArtist func(ctx context.Context, artistName string, limit int64) ([]int64, error)
	Clock          clock.Clock
	Logger         *slog.Logger
}

// RSSJob reads every indexer's feed and grabs what a wanted album accepts.
type RSSJob struct {
	config RSSConfig
	mu     sync.Mutex
	// newest is the latest release each indexer's feed has been read up to; a restart reads it all
	// again, which only re-judges releases already judged.
	newest map[int64]time.Time
}

// NewRSSJob builds the sync.
func NewRSSJob(config RSSConfig) *RSSJob {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &RSSJob{config: config, newest: map[int64]time.Time{}}
}

// rssTally is what one sync did, for its log line.
type rssTally struct{ releases, artists, judged, sent, held int }

// Run reads each enabled indexer's newest audio releases and judges each against the wanted albums
// of the artist it names. One indexer failing does not stop the others.
func (j *RSSJob) Run(ctx context.Context, _ jobs.Job) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	indexers, err := j.config.Indexers(ctx)
	if err != nil {
		return err
	}
	var tally rssTally
	for _, conn := range indexers {
		if err := ctx.Err(); err != nil {
			return err
		}
		raws, err := conn.Client.Search(ctx, indexer.SearchRequest{
			Function: "music", Categories: []int{rssAudioCategory}, Limit: rssFeedLimit,
		})
		if err != nil {
			j.config.Logger.Warn("rss sync: feed failed", "indexer", conn.Name, "error", err)
			continue
		}
		j.syncFeed(ctx, conn, j.unread(conn.ID, raws), &tally)
	}
	if tally.sent+tally.held > 0 || tally.judged > 0 {
		j.config.Logger.Info("rss sync: done", "releases", tally.releases, "artists", tally.artists,
			"albums_judged", tally.judged, "sent", tally.sent, "held", tally.held)
	}
	return nil
}

// unread keeps the releases newer than the last sync read from this indexer, and moves its mark.
func (j *RSSJob) unread(indexerID int64, raws []indexer.RawItem) []indexer.RawItem {
	mark := j.newest[indexerID]
	kept := raws[:0:0]
	for _, raw := range raws {
		if raw.PublishedAt.IsZero() || raw.PublishedAt.After(mark) {
			kept = append(kept, raw)
		}
		if raw.PublishedAt.After(j.newest[indexerID]) {
			j.newest[indexerID] = raw.PublishedAt
		}
	}
	return kept
}

// syncFeed groups one feed's releases by the artist they name and offers each group to that
// artist's wanted albums.
func (j *RSSJob) syncFeed(ctx context.Context, conn IndexerConn, raws []indexer.RawItem, tally *rssTally) {
	byArtist := map[string][]indexer.RawItem{}
	var order []string
	for _, raw := range raws {
		artist := strings.TrimSpace(parse.Parse(raw.Title).Identity.Artist)
		if artist == "" || videoRelease(raw.Title) {
			continue
		}
		key := strings.ToLower(artist)
		if _, seen := byArtist[key]; !seen {
			order = append(order, artist)
		}
		byArtist[key] = append(byArtist[key], raw)
	}
	tally.releases += len(raws)
	for _, artist := range order {
		itemIDs, err := j.config.WantedByArtist(ctx, artist, rssAlbumsPerArtist)
		if err != nil {
			j.config.Logger.Warn("rss sync: wanted albums", "artist", artist, "error", err)
			continue
		}
		if len(itemIDs) > 0 {
			tally.artists++
		}
		for _, itemID := range itemIDs {
			j.offer(ctx, conn, itemID, byArtist[strings.ToLower(artist)], tally)
		}
	}
}

func (j *RSSJob) offer(ctx context.Context, conn IndexerConn, itemID int64, raws []indexer.RawItem, tally *rssTally) {
	result, err := j.config.Grab.JudgeFeedAndSend(ctx, itemID, conn, raws, j.config.Clock.Now())
	switch {
	case errors.Is(err, ErrTrackSearchNotSupported), result.AlreadyInFlight:
		return
	case err != nil:
		j.config.Logger.Warn("rss sync: judging failed", "item_id", itemID, "error", err)
		return
	}
	tally.judged++
	if result.Sent != nil {
		tally.sent++
		j.config.Logger.Info("rss sync: release sent", "item_id", itemID, "release", result.Sent.CandidateTitle)
	}
	if result.Held {
		tally.held++
	}
	if len(result.Refusals) > 0 {
		j.config.Logger.Info("rss sync: releases refused", "item_id", itemID, "refused", result.Refusals)
	}
}
