package adapters_test

import (
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// An Undo sends back only what a bulk monitor changed, so works already in the wanted state must not be listed.
func TestBulkSetMonitored_ListsOnlyTheWorksWhoseStateChanged(t *testing.T) {
	cache := newSweepCache(t)
	opened := newSuggestLibrary(t)
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 0)
	if err != nil {
		t.Fatal(err)
	}
	identity, desired := library.NewIdentity(opened), library.NewDesired(opened)
	now := time.Unix(1_700_000_000, 0)
	var ids []int64
	for index, name := range []string{"already-monitored", "unmonitored", "second-unmonitored"} {
		resolved, err := identity.ResolveWorks(t.Context(), now, []library.WorkLink{{
			Provider: "musicbrainz:release-group", ExternalID: name, Confidence: 1,
			Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: name, SortTitle: name},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
			WorkID: resolved[0], CategoryID: category.ID, ProfileID: 1, Monitored: index == 0,
		}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, resolved[0])
	}
	monitor := adapters.WorksMonitor{
		Works: library.NewWorks(opened), Desired: desired, Categories: adapters.Categories{Cache: cache},
		DefaultProfileID: 1, Logger: slog.New(slog.DiscardHandler),
	}
	selection := apiserver.BulkSelection{IDs: ids, CategoryIDs: []int64{category.ID}}

	for _, step := range []struct {
		monitored bool
		preview   bool
		want      apiserver.BulkMonitorOutcome
	}{
		{true, true, apiserver.BulkMonitorOutcome{Matched: 3}},
		{true, false, apiserver.BulkMonitorOutcome{Matched: 3, Changed: []int64{ids[1], ids[2]}}},
		{true, false, apiserver.BulkMonitorOutcome{Matched: 3, Changed: []int64{}}},
		{false, false, apiserver.BulkMonitorOutcome{Matched: 3, Changed: ids}},
	} {
		got, err := monitor.BulkSetMonitored(t.Context(), selection, step.monitored, step.preview)
		if err != nil {
			t.Fatal(err)
		}
		if got.Matched != step.want.Matched || !slices.Equal(got.Changed, step.want.Changed) {
			t.Errorf("monitored=%v preview=%v: got %+v, want %+v", step.monitored, step.preview, got, step.want)
		}
	}
}
