package music

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/rss"
	"github.com/makearr/Solarr/internal/identify/parse"
)

// feedAudioCategory is Newznab's Audio, for an indexer with no categories set: without one a feed is mostly
// television (2026-09-30).
const feedAudioCategory = 3000

// feedAlbumsPerArtist bounds the wanted albums one release artist is judged against.
const feedAlbumsPerArtist = 200

// Feed is music's part of the RSS sync: a feed's releases are grouped by the artist they name and offered to that
// artist's wanted albums. Every field but Logger is required.
type Feed struct {
	Grab     Grab
	Indexers IndexerSource
	// WantedByArtist answers the wanted albums credited to an artist of that name.
	WantedByArtist func(ctx context.Context, artistName string, limit int64) ([]int64, error)
	Logger         *slog.Logger
}

// FeedIndexers implements rss.Feed.
func (f Feed) FeedIndexers(ctx context.Context) ([]judge.Conn, error) { return f.Indexers(ctx) }

// FeedRequest implements rss.Feed.
func (Feed) FeedRequest() indexer.SearchRequest {
	return indexer.SearchRequest{Function: musicFunction, Categories: []int{feedAudioCategory}}
}

// OfferFeed implements rss.Feed.
func (f Feed) OfferFeed(ctx context.Context, conn judge.Conn, raws []indexer.RawItem, at time.Time) rss.Tally {
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
	var tally rss.Tally
	for _, artist := range order {
		itemIDs, err := f.WantedByArtist(ctx, artist, feedAlbumsPerArtist)
		if err != nil {
			f.logger().Warn("rss sync: wanted albums", "artist", artist, "error", err)
			continue
		}
		for _, itemID := range itemIDs {
			f.offer(ctx, conn, itemID, byArtist[strings.ToLower(artist)], at, &tally)
		}
	}
	return tally
}

func (f Feed) offer(ctx context.Context, conn judge.Conn, itemID int64, raws []indexer.RawItem, at time.Time, tally *rss.Tally) {
	result, err := f.Grab.JudgeFeedAndSend(ctx, itemID, conn, raws, at)
	switch {
	case errors.Is(err, ErrTrackSearchNotSupported), result.AlreadyInFlight:
		return
	case err != nil:
		f.logger().Warn("rss sync: judging failed", "item_id", itemID, "error", err)
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
	if len(result.Refusals) > 0 {
		f.logger().Info("rss sync: releases refused", "item_id", itemID, "refused", result.Refusals)
	}
}

func (f Feed) logger() *slog.Logger {
	if f.Logger == nil {
		return slog.Default()
	}
	return f.Logger
}
