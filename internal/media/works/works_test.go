package works_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type categories struct{}

func (categories) Category(id int64) (settings.Category, bool) {
	return settings.Category{ID: id}, id == 7
}
func (categories) ProfileFor(settings.Category, int64) int64 { return 30 }

type source struct {
	added []works.Add
	held  map[string]int64
}

func (s *source) Lookup(context.Context, media.AddLookup) ([]media.AddCandidate, error) {
	return []media.AddCandidate{{Provider: "p", ExternalID: "1"}, {Provider: "p", ExternalID: "2"}}, nil
}

func (s *source) Held(_ context.Context, _, externalID string) (int64, bool) {
	id, held := s.held[externalID]
	return id, held
}

func (*source) Monitor() (string, string, func(string) bool) {
	return "all", "none", func(option string) bool { return slices.Contains([]string{"all", "future", "none"}, option) }
}

func (s *source) Add(_ context.Context, request works.Add) (media.AddResult, error) {
	s.added = append(s.added, request)
	return media.AddResult{WorkID: 9}, nil
}

func TestCatalog_ReadsTheDialogsChoicesTheSameForEveryType(t *testing.T) {
	added := &source{held: map[string]int64{"2": 44}}
	var allowed []string
	catalog := works.Catalog{Source: added, Categories: categories{}, DefaultProfileID: 1,
		AllowAgain: func(_ context.Context, provider, externalID string) error {
			allowed = append(allowed, provider+":"+externalID)
			return nil
		}}

	found, err := catalog.LookupWorks(t.Context(), media.AddLookup{Query: "an"})
	if err != nil || found[0].LibraryID != 0 || found[1].LibraryID != 44 {
		t.Errorf("found %+v, %v; want only the held one marked", found, err)
	}
	if _, err := catalog.LookupWorks(t.Context(), media.AddLookup{Query: "a"}); !errors.Is(err, media.ErrQueryTooShort) {
		t.Errorf("a one-letter query: %v", err)
	}

	own := int64(5)
	requests := []media.AddRequest{
		{CategoryID: 7, Provider: "p", ExternalID: "1", Monitored: true, UserID: 3},
		{CategoryID: 7, Provider: "p", ExternalID: "1", Monitored: true, Option: "future", ProfileID: &own},
		{CategoryID: 7, Provider: "p", ExternalID: "1", Monitored: false, Option: "future"},
	}
	for _, request := range requests {
		if _, err := catalog.AddWork(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	got := [][2]any{}
	for _, add := range added.added {
		got = append(got, [2]any{add.Option, add.ProfileID})
	}
	if want := [][2]any{{"all", int64(30)}, {"future", int64(5)}, {"none", int64(30)}}; !slices.Equal(got, want) {
		t.Errorf("options and profiles = %v, want %v", got, want)
	}
	if added.added[0].AddedBy == nil || *added.added[0].AddedBy != 3 || added.added[1].AddedBy != nil {
		t.Errorf("added by %v, %v", added.added[0].AddedBy, added.added[1].AddedBy)
	}
	if len(allowed) != 3 {
		t.Errorf("every add on purpose lifts an exclusion: %v", allowed)
	}
	if _, err := catalog.AddWork(t.Context(), media.AddRequest{CategoryID: 7, Monitored: true, Option: "pilot"}); !errors.Is(err, media.ErrInvalidAdd) {
		t.Errorf("an option the type lacks: %v", err)
	}
	if _, err := catalog.AddWork(t.Context(), media.AddRequest{CategoryID: 8, Monitored: true}); !errors.Is(err, media.ErrInvalidAdd) || len(added.added) != 3 {
		t.Errorf("a library that is not there: %v", err)
	}
}

func TestSearching_NarrowsToAPartOnlyForATypeThatReadsParts(t *testing.T) {
	whole := works.Searching{Name: "movie"}
	if _, err := whole.SearchWanted(t.Context(), media.WantedSearch{ItemID: 1, Part: "s1:e2"}); !errors.Is(err, media.ErrWholeItemOnly) {
		t.Errorf("a part of a movie: %v", err)
	}
	if _, err := whole.SearchWanted(t.Context(), media.WantedSearch{ItemID: 1, Terms: map[string]string{"artist": "x"}}); !errors.Is(err, media.ErrInvalidTerms) {
		t.Errorf("terms for a movie: %v", err)
	}
	refused := errors.New("not an episode")
	parted := works.Searching{Name: "tv", Part: func(string) (int, error) { return 0, refused }}
	if _, err := parted.SearchWanted(t.Context(), media.WantedSearch{ItemID: 1, Part: "x"}); !errors.Is(err, refused) {
		t.Errorf("a part the type cannot read: %v", err)
	}
	noIndexers := works.Searching{Name: "tv", Conns: func(context.Context) ([]judge.Conn, error) { return nil, nil }}
	if _, err := noIndexers.SearchWanted(t.Context(), media.WantedSearch{ItemID: 1}); !errors.Is(err, media.ErrNoIndexers) {
		t.Errorf("no indexer: %v", err)
	}
}

func TestRefreshJob_OneFailingNeverStopsTheRestAndIsMarked(t *testing.T) {
	noSource := errors.New("no source")
	var mu sync.Mutex
	var refreshed, marked []int64
	job := works.RefreshJob{
		Name: "movie", JobType: jobs.Type("movie.refresh"), NoSource: noSource, Clock: clock.NewFake(time.Unix(1_800_000_000, 0)),
		Due: func(context.Context) ([]library.RefreshCandidate, error) {
			return []library.RefreshCandidate{{WorkID: 1}, {WorkID: 2}, {WorkID: 3}}, nil
		},
		Refresh: func(_ context.Context, candidate library.RefreshCandidate) error {
			if candidate.WorkID == 2 {
				return noSource
			}
			mu.Lock()
			defer mu.Unlock()
			refreshed = append(refreshed, candidate.WorkID)
			return nil
		},
		MarkAttempt: func(_ context.Context, workID int64) error {
			mu.Lock()
			defer mu.Unlock()
			marked = append(marked, workID)
			return nil
		},
	}
	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(refreshed)
	if !slices.Equal(refreshed, []int64{1, 3}) || !slices.Equal(marked, []int64{2}) {
		t.Errorf("refreshed %v, marked %v", refreshed, marked)
	}
}
