package adapters_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func TestMonitoringAHandMadeCollectionMonitorsItsWorksAndThoseAddedWhileMonitored(t *testing.T) {
	cache := newSweepCache(t)
	songs, err := cache.CreateCategory(t.Context(), "music", "Music", 0)
	if err != nil {
		t.Fatal(err)
	}
	opened := newSuggestLibrary(t)
	works, err := library.NewIdentity(opened).ResolveWorks(t.Context(), clock.NewFakeAtEpoch().Now(), []library.WorkLink{
		{Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 1, Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "One", SortTitle: "one"}},
		{Provider: "musicbrainz:release-group", ExternalID: "rg-2", Confidence: 1, Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "Two", SortTitle: "two"}},
		{Provider: "musicbrainz:release-group", ExternalID: "rg-3", Confidence: 1, Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "Three", SortTitle: "three"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := library.NewFranchises(opened)
	collection, err := store.Create(t.Context(), "music", songs.ID, "Road trip")
	if err != nil {
		t.Fatal(err)
	}
	var monitored []int64
	franchises := adapters.LibraryFranchises{Cache: cache, Store: store, MonitorWork: func(_ context.Context, workID int64) error {
		monitored = append(monitored, workID)
		return nil
	}}
	if err := franchises.AddMember(t.Context(), songs.ID, collection, works[0]); err != nil {
		t.Fatal(err)
	}
	if len(monitored) != 0 {
		t.Fatalf("monitored = %v before the collection is, want none", monitored)
	}
	count, err := franchises.Monitor(t.Context(), songs.ID, collection, true)
	if err != nil || count != 1 {
		t.Fatalf("Monitor = %d, %v, want its one work", count, err)
	}
	if err := franchises.AddMember(t.Context(), songs.ID, collection, works[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := franchises.Monitor(t.Context(), songs.ID, collection, false); err != nil {
		t.Fatal(err)
	}
	if err := franchises.AddMember(t.Context(), songs.ID, collection, works[2]); err != nil {
		t.Fatal(err)
	}
	if want := []int64{works[0], works[1]}; !reflect.DeepEqual(monitored, want) {
		t.Errorf("monitored = %v, want %v: the member, then the work added while monitored", monitored, want)
	}
	if row, _ := store.Get(t.Context(), collection); row.Monitored {
		t.Errorf("the collection is still monitored after Unmonitor")
	}
}
