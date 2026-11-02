package franchisewatch_test

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/service/franchisewatch"
)

type call struct {
	CategoryID, FranchiseID int64
	Monitored               bool
}

// franchisingType follows each franchise by adding perFranchise works, failing for the ids in failing.
type franchisingType struct {
	key          media.Key
	perFranchise int
	failing      map[int64]error
	calls        *[]call
}

func (f franchisingType) Descriptor() media.Descriptor { return media.Descriptor{Key: f.key} }

func (f franchisingType) MissingFranchiseMembers(context.Context, int64, int64) ([]media.FranchiseMember, error) {
	return nil, nil
}

func (f franchisingType) FranchiseFacts(context.Context, []media.FranchiseRef) map[int64]media.FranchiseFact {
	return nil
}

func (f franchisingType) MonitorFranchise(_ context.Context, categoryID, franchiseID int64, monitored bool) (int, error) {
	*f.calls = append(*f.calls, call{categoryID, franchiseID, monitored})
	if err := f.failing[franchiseID]; err != nil {
		return 0, err
	}
	return f.perFranchise, nil
}

type plainType struct{}

func (plainType) Descriptor() media.Descriptor { return media.Descriptor{Key: "plain"} }

type libraries []settings.Category

func (l libraries) Categories() []settings.Category { return l }

type franchises map[int64][]library.FranchiseRow

func (f franchises) InCategory(_ context.Context, _ string, categoryID int64) ([]library.FranchiseRow, error) {
	return f[categoryID], nil
}

func run(t *testing.T, perFranchise int, failing map[int64]error, libs libraries, rows franchises) []call {
	t.Helper()
	var calls []call
	registry, err := media.NewRegistry(franchisingType{key: "fake", perFranchise: perFranchise, failing: failing, calls: &calls}, plainType{})
	if err != nil {
		t.Fatal(err)
	}
	service := franchisewatch.Service{Libraries: libs, Franchises: rows, Media: registry, Logger: slog.New(slog.DiscardHandler)}
	if err := service.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	return calls
}

func TestRun_FollowsOnlyMonitoredProviderFranchisesOnceEach(t *testing.T) {
	calls := run(t, 1, nil,
		libraries{{ID: 1, MediaType: "fake"}, {ID: 2, MediaType: "fake"}, {ID: 3, MediaType: "plain"}},
		franchises{
			1: {
				{ID: 10, Kind: library.FranchiseKind, Monitored: true},
				{ID: 11, Kind: library.FranchiseKind, Monitored: false},
				{ID: 12, Kind: library.CustomKind, Monitored: true},
			},
			2: {{ID: 10, Kind: library.FranchiseKind, Monitored: true}, {ID: 13, Kind: library.FranchiseKind, Monitored: true}},
			3: {{ID: 14, Kind: library.FranchiseKind, Monitored: true}},
		})
	want := []call{{1, 10, true}, {2, 13, true}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %+v, want %+v", calls, want)
	}
}

func TestRun_StopsOnceTheLimitIsReachedAndLeavesTheRestForNextTime(t *testing.T) {
	var rows []library.FranchiseRow
	for id := int64(1); id <= 10; id++ {
		rows = append(rows, library.FranchiseRow{ID: id, Kind: library.FranchiseKind, Monitored: true})
	}
	// Ten works per franchise: the third franchise reaches 30 >= 25, so the fourth is not asked.
	calls := run(t, 10, nil, libraries{{ID: 1, MediaType: "fake"}}, franchises{1: rows})
	want := []call{{1, 1, true}, {1, 2, true}, {1, 3, true}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %+v, want %+v", calls, want)
	}
}

func TestRun_OneFailingFranchiseNeverStopsTheOthers(t *testing.T) {
	calls := run(t, 1, map[int64]error{1: errors.New("provider down"), 2: media.ErrNotWired}, libraries{{ID: 1, MediaType: "fake"}},
		franchises{1: {
			{ID: 1, Kind: library.FranchiseKind, Monitored: true},
			{ID: 2, Kind: library.FranchiseKind, Monitored: true},
			{ID: 3, Kind: library.FranchiseKind, Monitored: true},
		}})
	want := []call{{1, 1, true}, {1, 2, true}, {1, 3, true}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %+v, want %+v", calls, want)
	}
}
