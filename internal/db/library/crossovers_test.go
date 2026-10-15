package library_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

// One broadcast listed under two series is held once: linking the two lets the file of one cover the other.
func TestLinkSameContent_OneFileCoversBothSeries(t *testing.T) {
	opened, queries := newLibrary(t)
	crossovers := library.NewCrossovers(opened)
	first := createWork(t, opened, "tv", "Invented Show")
	second := createWork(t, opened, "tv", "Invented Spinoff")
	held := createPart(t, opened, first.ID, "s01:e01", "s01:e01", "Pilot")
	listed := createPart(t, opened, second.ID, "s02:e05", "s02:e05", "Pilot")
	firstItem := createDesired(t, opened, first.ID, "SET", 1)
	secondItem := createDesired(t, opened, second.ID, "SET", 1)
	object := createObject(t, opened, "broadcast", 1)
	attach(t, opened, firstItem.ID, object.ID, "primary", "s01:e01")

	if err := crossovers.LinkSameContent(t.Context(), held.ID, listed.ID); err != nil {
		t.Fatal(err)
	}
	item, err := queries.GetDesiredItem(t.Context(), secondItem.ID)
	if err != nil || item.PresentCount != 1 || item.State != "COMPLETE" {
		t.Errorf("the spinoff's season = %+v, %v; want its episode held by the one file", item, err)
	}
	links, err := crossovers.SameContentOf(t.Context(), []int64{listed.ID})
	want := []library.SameContent{{PartID: listed.ID, OtherPartID: held.ID, OtherWorkID: first.ID, OtherRef: "s01:e01", OtherWorkTitle: "Invented Show"}}
	if err != nil || !reflect.DeepEqual(links, want) {
		t.Errorf("links = %+v, %v; want %+v", links, err, want)
	}
}

// An event keeps its parts in watch order across series, and is found from every series it touches.
func TestCrossover_KeepsItsPartsInOrderAndIsFoundFromEachSeries(t *testing.T) {
	opened, _ := newLibrary(t)
	crossovers := library.NewCrossovers(opened)
	first := createWork(t, opened, "tv", "Invented Show")
	second := createWork(t, opened, "tv", "Invented Spinoff")
	opening := createPart(t, opened, first.ID, "s03:e09", "s03:e09", "Night One")
	closing := createPart(t, opened, second.ID, "s01:e08", "s01:e08", "Night Two")

	id, err := crossovers.Create(t.Context(), "tv", "Invented Event", []int64{opening.ID, closing.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, workID := range []int64{first.ID, second.ID} {
		events, err := crossovers.ForWork(t.Context(), workID)
		if err != nil || len(events) != 1 || events[0].ID != id {
			t.Fatalf("work %d's events = %+v, %v", workID, events, err)
		}
	}
	if err := crossovers.Update(t.Context(), id, "Invented Event (Director's Order)", []int64{closing.ID, opening.ID}); err != nil {
		t.Fatal(err)
	}
	event, err := crossovers.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, member := range event.Members {
		order = append(order, member.WorkTitle+" "+member.MediaRef+" "+member.PartTitle)
	}
	if event.Name != "Invented Event (Director's Order)" || !reflect.DeepEqual(order, []string{"Invented Spinoff s01:e08 Night Two", "Invented Show s03:e09 Night One"}) {
		t.Errorf("event = %q %v", event.Name, order)
	}
	if err := crossovers.Delete(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := crossovers.Get(t.Context(), id); !errors.Is(err, library.ErrNotACrossover) {
		t.Errorf("after delete: %v, want ErrNotACrossover", err)
	}
}
