package franchisefacts_test

import (
	"context"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/franchisefacts"
)

type factsType struct {
	asked *[][]int64
	facts map[int64]media.FranchiseFact
}

func (factsType) Descriptor() media.Descriptor { return media.Descriptor{Key: "fake"} }

func (factsType) MissingFranchiseMembers(context.Context, int64, int64) ([]media.FranchiseMember, error) {
	return nil, nil
}

func (f factsType) FranchiseFacts(_ context.Context, refs []media.FranchiseRef) map[int64]media.FranchiseFact {
	var ids []int64
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	*f.asked = append(*f.asked, ids)
	return f.facts
}

func (factsType) MonitorFranchise(context.Context, int64, int64, bool) (int, error) { return 0, nil }

type libraries []settings.Category

func (l libraries) Categories() []settings.Category { return l }

// store keeps what the pass wrote.
type store struct {
	rows    []library.FranchiseRow
	totals  map[int64]int64
	posters map[int64]string
	checked map[int64]int64
}

func (s *store) InCategory(context.Context, string, int64) ([]library.FranchiseRow, error) {
	return s.rows, nil
}
func (s *store) SetTotal(_ context.Context, id, total int64) error { s.totals[id] = total; return nil }
func (s *store) SetPoster(_ context.Context, id int64, url string) error {
	s.posters[id] = url
	return nil
}
func (s *store) SetFactsChecked(_ context.Context, id, at int64) error {
	s.checked[id] = at
	return nil
}

func TestRun_FillsWhatTheProviderKnowsAndStampsEveryCollectionAsked(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	fake.Advance(30 * 24 * time.Hour)
	var asked [][]int64
	registry, err := media.NewRegistry(factsType{asked: &asked, facts: map[int64]media.FranchiseFact{10: {Total: 4, PosterURL: "https://img.example/10.jpg"}}})
	if err != nil {
		t.Fatal(err)
	}
	rows := &store{
		rows: []library.FranchiseRow{
			{ID: 10, Kind: library.FranchiseKind},
			{ID: 11, Kind: library.FranchiseKind},
			{ID: 12, Kind: library.FranchiseKind, FactsCheckedAt: fake.Now().Add(-time.Hour).Unix()},
			{ID: 13, Kind: library.FranchiseKind, Total: 3, PosterURL: "https://img.example/13.jpg"},
		},
		totals: map[int64]int64{}, posters: map[int64]string{}, checked: map[int64]int64{},
	}
	var told []int64
	service := franchisefacts.Service{
		Libraries: libraries{{ID: 1, MediaType: "fake"}}, Franchises: rows, Media: registry, Clock: fake, Logger: slog.New(slog.DiscardHandler),
		Filled: func(categoryID int64) { told = append(told, categoryID) },
	}

	if err := service.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if want := []int64{1}; !reflect.DeepEqual(told, want) {
		t.Errorf("libraries told of new facts = %v, want %v", told, want)
	}
	if want := [][]int64{{10, 11}}; !reflect.DeepEqual(asked, want) {
		t.Errorf("asked = %v, want %v: only collections lacking something and not checked this week", asked, want)
	}
	if rows.totals[10] != 4 || rows.posters[10] != "https://img.example/10.jpg" {
		t.Errorf("stored totals %v posters %v, want collection 10 filled", rows.totals, rows.posters)
	}
	if len(rows.checked) != 2 || rows.checked[11] != fake.Now().Unix() {
		t.Errorf("checked = %v, want 10 and 11 stamped, the one the provider knew nothing of included", rows.checked)
	}

	rows.rows[0].FactsCheckedAt, rows.rows[1].FactsCheckedAt = rows.checked[10], rows.checked[11]
	asked, told = nil, nil
	if err := service.Run(t.Context(), jobs.Job{}); err != nil || len(asked) != 0 || len(told) != 0 {
		t.Errorf("second run asked %v (err %v), want nothing within the week", asked, err)
	}
}

func TestRun_AsksAboutAtMostPerLibraryCollectionsAPass(t *testing.T) {
	var asked [][]int64
	registry, err := media.NewRegistry(factsType{asked: &asked})
	if err != nil {
		t.Fatal(err)
	}
	rows := &store{totals: map[int64]int64{}, posters: map[int64]string{}, checked: map[int64]int64{}}
	for id := int64(1); id <= franchisefacts.PerLibrary+5; id++ {
		rows.rows = append(rows.rows, library.FranchiseRow{ID: id, Kind: library.FranchiseKind})
	}
	fake := clock.NewFakeAtEpoch()
	fake.Advance(30 * 24 * time.Hour)
	service := franchisefacts.Service{Libraries: libraries{{ID: 1, MediaType: "fake"}}, Franchises: rows, Media: registry, Clock: fake, Logger: slog.New(slog.DiscardHandler)}

	if err := service.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || len(asked[0]) != franchisefacts.PerLibrary {
		t.Errorf("asked = %v, want one call about %d collections", asked, franchisefacts.PerLibrary)
	}
}
