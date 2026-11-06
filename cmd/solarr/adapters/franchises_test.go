package adapters_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
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

func TestAHandLinkShowsWhoMadeItAndUnlinkingIsKept(t *testing.T) {
	cache := newSweepCache(t)
	first, err := cache.CreateCategory(t.Context(), "music", "Music", 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.CreateCategory(t.Context(), "music", "Soundtracks", 0)
	if err != nil {
		t.Fatal(err)
	}
	opened := newSuggestLibrary(t)
	store := library.NewFranchises(opened)
	registry, err := media.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	franchises := adapters.LibraryFranchises{Cache: cache, Registry: registry, Store: store}
	one, err := store.Create(t.Context(), "music", first.ID, "Film scores")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Create(t.Context(), "music", second.ID, "Film scores, again")
	if err != nil {
		t.Fatal(err)
	}
	if err := franchises.Link(t.Context(), first.ID, one, second.ID, other); err != nil {
		t.Fatal(err)
	}
	view, err := franchises.Get(t.Context(), first.ID, one)
	if err != nil || view.Links == nil || len(*view.Links) != 1 {
		t.Fatalf("links = %+v, %v; want the one collection", view.Links, err)
	}
	if link := (*view.Links)[0]; link.Reason == nil || *link.Reason != gen.FranchiseLinkReasonHand || link.CategoryId != second.ID {
		t.Errorf("link = %+v, want one made by hand in the second library", link)
	}
	if err := franchises.Unlink(t.Context(), first.ID, one, other); err != nil {
		t.Fatal(err)
	}
	if view, err = franchises.Get(t.Context(), first.ID, one); err != nil || view.Links != nil {
		t.Errorf("links after unlinking = %+v, %v", view.Links, err)
	}
}
