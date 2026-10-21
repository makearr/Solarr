package rss_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/rss"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type listing []indexer.RawItem

func (l listing) Search(context.Context, indexer.SearchRequest) ([]indexer.RawItem, error) {
	return l, nil
}

// wantsNothing is a media type with nothing wanted: every release is read, none judged.
type wantsNothing struct{ conns []judge.Conn }

func (w wantsNothing) FeedIndexers(context.Context) ([]judge.Conn, error) { return w.conns, nil }
func (wantsNothing) FeedRequest() indexer.SearchRequest {
	return indexer.SearchRequest{Function: "music"}
}
func (wantsNothing) OfferFeed(context.Context, judge.Conn, []indexer.RawItem, time.Time) rss.Tally {
	return rss.Tally{}
}

// A sync that matched nothing still says what each feed gave: found live 2026-10-21, a working RSS left no trace.
func TestJob_ReportsWhatEachFeedGaveWhenNothingMatched(t *testing.T) {
	var logged bytes.Buffer
	published := time.Unix(1_700_000_000, 0)
	feed := wantsNothing{conns: []judge.Conn{
		{ID: 1, Name: "Usenet one", Client: listing{{GUID: "a", PublishedAt: published}, {GUID: "b", PublishedAt: published}}},
		{ID: 2, Name: "Torrents", Client: listing{}},
	}}
	job := rss.NewJob(rss.Config{
		Sources: func() []rss.Source { return []rss.Source{{Name: "music", Feed: feed}} },
		Clock:   clock.NewFakeAtEpoch(),
		Logger:  slog.New(slog.NewTextHandler(&logged, nil)),
	})
	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	line := strings.TrimSpace(logged.String())
	for _, want := range []string{`msg="rss sync: done"`, "media_type=music", "releases=2",
		`indexers="Usenet one 2 new of 2, Torrents 0 new of 0"`, "items_judged=0"} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q lacks %s", line, want)
		}
	}
}
