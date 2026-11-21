package library

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

type countingTitles struct {
	reads  int
	titles []SeriesTitle
}

func (c *countingTitles) TitlesOf(context.Context, string, int64) ([]SeriesTitle, error) {
	c.reads++
	return c.titles, nil
}

func newTitleIndexFixture() (*TitleIndex, *countingTitles, *clock.Fake) {
	source := &countingTitles{titles: []SeriesTitle{
		{WorkID: 2, Title: "Alias Two", Canonical: false},
		{WorkID: 2, Title: "Canonical Two", Canonical: true},
		{WorkID: 1, Title: "Canonical One", Canonical: true},
		{WorkID: 3, Title: "Other", Canonical: true},
	}}
	fake := clock.NewFakeAtEpoch()
	return &TitleIndex{source: source, clock: fake}, source, fake
}

func TestTitleIndex_AnswersTheRowsOfTheWantedKeysWithTheirCanonicalTitleOrderedByWork(t *testing.T) {
	index, _, _ := newTitleIndexFixture()
	rows, err := index.Lookup(t.Context(), "movie", 1, strings.ToLower, []string{"alias two", "canonical one"})
	if err != nil {
		t.Fatal(err)
	}
	want := []SeriesTitle{
		{WorkID: 1, Title: "Canonical One", Canonical: true},
		{WorkID: 2, Title: "Canonical Two", Canonical: true},
		{WorkID: 2, Title: "Alias Two"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("rows = %+v, want %+v", rows, want)
		}
	}
}

func TestTitleIndex_ReadsACategoryOncePerTTLAndAgainAfterInvalidate(t *testing.T) {
	index, source, fake := newTitleIndexFixture()
	lookup := func() {
		t.Helper()
		if _, err := index.Lookup(t.Context(), "movie", 1, strings.ToLower, []string{"other"}); err != nil {
			t.Fatal(err)
		}
	}
	lookup()
	lookup()
	if source.reads != 1 {
		t.Fatalf("reads = %d, want 1 for two lookups inside the TTL", source.reads)
	}
	index.Invalidate("movie", 1)
	lookup()
	if source.reads != 2 {
		t.Fatalf("reads = %d, want a read after Invalidate", source.reads)
	}
	fake.Advance(titleIndexTTL + time.Second)
	lookup()
	if source.reads != 3 {
		t.Fatalf("reads = %d, want a read after the TTL", source.reads)
	}
	if _, err := index.Lookup(t.Context(), "movie", 2, strings.ToLower, []string{"other"}); err != nil || source.reads != 4 {
		t.Fatalf("reads = %d, %v; want another category read of its own", source.reads, err)
	}
}

func TestTitleIndex_AllReadsACategoryOncePerTTLAndAgainAfterInvalidate(t *testing.T) {
	index, source, fake := newTitleIndexFixture()
	for range 3 {
		titles, err := index.All(t.Context(), "movie", 1)
		if err != nil || len(titles) != len(source.titles) {
			t.Fatalf("All = %d titles, %v; want every title", len(titles), err)
		}
	}
	if source.reads != 1 {
		t.Fatalf("reads = %d, want 1 for three calls inside the TTL", source.reads)
	}
	index.Invalidate("movie", 1)
	if _, err := index.All(t.Context(), "movie", 1); err != nil || source.reads != 2 {
		t.Fatalf("reads = %d, %v; want a read after Invalidate", source.reads, err)
	}
	fake.Advance(titleIndexTTL + time.Second)
	if _, err := index.All(t.Context(), "movie", 1); err != nil || source.reads != 3 {
		t.Fatalf("reads = %d, %v; want a read after the TTL", source.reads, err)
	}
}
