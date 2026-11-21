package rss_test

import (
	"bytes"
	"context"
	"fmt"
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

// conditionalListing answers 304 once it has been asked with the validator it handed out.
type conditionalListing struct {
	listing
	asked *[]string
}

func (c conditionalListing) SearchIfChanged(_ context.Context, _ indexer.SearchRequest, seen indexer.Validators) ([]indexer.RawItem, indexer.Validators, bool, error) {
	*c.asked = append(*c.asked, seen.ETag)
	if seen.ETag == "v1" {
		return nil, seen, true, nil
	}
	return c.listing, indexer.Validators{ETag: "v1"}, false, nil
}

func TestJob_AFeedAnswering304IsNotJudgedAgain(t *testing.T) {
	var logged bytes.Buffer
	var asked []string
	published := time.Unix(1_700_000_000, 0)
	feed := wantsNothing{conns: []judge.Conn{{ID: 1, Name: "Usenet one", Client: conditionalListing{listing{{GUID: "a", PublishedAt: published}}, &asked}}}}
	job := rss.NewJob(rss.Config{
		Sources: func() []rss.Source { return []rss.Source{{Name: "music", Feed: feed}} },
		Clock:   clock.NewFakeAtEpoch(),
		Logger:  slog.New(slog.NewTextHandler(&logged, nil)),
	})
	for range 2 {
		if err := job.Run(t.Context(), jobs.Job{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	if got := strings.Join(asked, "|"); got != "|v1" {
		t.Errorf("validators sent = %q, want the second sync to send v1", got)
	}
	if !strings.Contains(logged.String(), `indexers="Usenet one not modified"`) {
		t.Errorf("log lacks the not-modified feed: %s", logged.String())
	}
}

// switching is a feed whose answer the test changes between syncs.
type switching struct{ items *[]indexer.RawItem }

func (s switching) Search(context.Context, indexer.SearchRequest) ([]indexer.RawItem, error) {
	return *s.items, nil
}

// offering records the GUIDs each sync offered.
type offering struct {
	conns   []judge.Conn
	offered *[][]string
}

func (o offering) FeedIndexers(context.Context) ([]judge.Conn, error) { return o.conns, nil }
func (offering) FeedRequest() indexer.SearchRequest {
	return indexer.SearchRequest{Function: "music"}
}
func (o offering) OfferFeed(_ context.Context, _ judge.Conn, raws []indexer.RawItem, _ time.Time) rss.Tally {
	var guids []string
	for _, raw := range raws {
		guids = append(guids, raw.GUID)
	}
	*o.offered = append(*o.offered, guids)
	return rss.Tally{}
}

// A release posted before the newest one seen is still new when the indexer lists it late (RSS-1).
func TestJob_AReleaseListedLateButPostedEarlierIsOffered(t *testing.T) {
	var logged bytes.Buffer
	var offered [][]string
	at := func(seconds int64) time.Time { return time.Unix(1_700_000_000+seconds, 0) }
	items := []indexer.RawItem{{GUID: "A", PublishedAt: at(10)}, {GUID: "B", PublishedAt: at(20)}}
	feed := offering{conns: []judge.Conn{{ID: 1, Name: "Usenet", Client: switching{&items}}}, offered: &offered}
	job := rss.NewJob(rss.Config{
		Sources: func() []rss.Source { return []rss.Source{{Name: "music", Feed: feed}} },
		Clock:   clock.NewFakeAtEpoch(),
		Logger:  slog.New(slog.NewTextHandler(&logged, nil)),
	})
	run := func() {
		t.Helper()
		if err := job.Run(t.Context(), jobs.Job{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	run()
	items = []indexer.RawItem{{GUID: "C", PublishedAt: at(15)}, items[0], items[1]}
	run()
	run()
	want := [][]string{{"A", "B"}, {"C"}, nil}
	if fmt.Sprint(offered) != fmt.Sprint(want) {
		t.Errorf("offered per sync = %v, want %v", offered, want)
	}
}

func TestJob_WarnsWhenAFullPageHoldsNothingSeen(t *testing.T) {
	var logged bytes.Buffer
	var offered [][]string
	page := func(prefix string) []indexer.RawItem {
		items := make([]indexer.RawItem, rss.FeedLimit)
		for index := range items {
			items[index] = indexer.RawItem{GUID: fmt.Sprintf("%s-%d", prefix, index)}
		}
		return items
	}
	items := page("old")
	feed := offering{conns: []judge.Conn{{ID: 1, Name: "Usenet", Client: switching{&items}}}, offered: &offered}
	job := rss.NewJob(rss.Config{
		Sources: func() []rss.Source { return []rss.Source{{Name: "music", Feed: feed}} },
		Clock:   clock.NewFakeAtEpoch(),
		Logger:  slog.New(slog.NewTextHandler(&logged, nil)),
	})
	for range 2 {
		if err := job.Run(t.Context(), jobs.Job{}); err != nil {
			t.Fatal(err)
		}
		items = page("new")
	}
	if got := strings.Count(logged.String(), "feed moved further than one page between syncs"); got != 1 {
		t.Errorf("warnings = %d, want one (the first sync has nothing to compare with): %s", got, logged.String())
	}
}

// paged answers a feed of numbered releases newest first, honouring the offset like a Newznab indexer.
type paged struct{ total *int }

func (p paged) Search(_ context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	var items []indexer.RawItem
	for index := request.Offset; index < request.Offset+request.Limit && index < *p.total; index++ {
		items = append(items, indexer.RawItem{GUID: fmt.Sprintf("r-%d", *p.total-index)})
	}
	return items, nil
}

// A feed that moved on by more than one page between syncs is read further back until it meets what was seen (RSS-1).
func TestJob_AFeedThatMovedMoreThanAPageIsReadFurtherBack(t *testing.T) {
	var offered [][]string
	total := 300
	feed := offering{conns: []judge.Conn{{ID: 1, Name: "Usenet", Client: paged{&total}}}, offered: &offered}
	job := rss.NewJob(rss.Config{
		Sources: func() []rss.Source { return []rss.Source{{Name: "music", Feed: feed}} },
		Clock:   clock.NewFakeAtEpoch(),
		Logger:  slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	})
	run := func() {
		t.Helper()
		if err := job.Run(t.Context(), jobs.Job{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	run()
	total = 450
	run()
	if len(offered) != 2 || len(offered[1]) != 150 {
		t.Fatalf("second sync offered %d releases, want the 150 that arrived", len(offered[1]))
	}
}
