package library_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
)

func dateParts(t *testing.T, opened *store.Store, dates map[int64]string) {
	t.Helper()
	err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		for id, date := range dates {
			if _, err := tx.ExecContext(ctx, "UPDATE work_part SET date = ? WHERE id = ?", date, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDetectEventsMakesOneOfNumberedEpisodesAcrossAFranchisesSeries(t *testing.T) {
	opened, _ := newLibrary(t)
	crossovers := library.NewCrossovers(opened)
	franchises := library.NewFranchises(opened)
	var works [3]int64
	var parts [3]int64
	for i, title := range []string{"Invented Show", "Invented Spinoff", "Unrelated Show"} {
		work := createWork(t, opened, "tv", title)
		works[i] = work.ID
		createDesiredInCategory(t, opened, work.ID, 7)
	}
	parts[0] = createPart(t, opened, works[0], "s03:e09", "s03:e09", "Great Night (2)").ID
	parts[1] = createPart(t, opened, works[1], "s01:e08", "s01:e08", "Great Night (1)").ID
	parts[2] = createPart(t, opened, works[2], "s01:e01", "s01:e01", "Great Night (3)").ID
	dateParts(t, opened, map[int64]string{parts[0]: "2018-12-10", parts[1]: "2018-12-09", parts[2]: "2018-12-11"})
	for _, work := range works[:2] {
		if _, err := franchises.Join(t.Context(), "tv", "wikidata", "Q1", "Invented Universe", work); err != nil {
			t.Fatal(err)
		}
	}

	made, err := crossovers.DetectEvents(t.Context(), "tv", 7)
	if err != nil || made != 1 {
		t.Fatalf("made = %d, %v; want one event", made, err)
	}
	events, err := crossovers.List(t.Context(), "tv")
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v, %v", events, err)
	}
	var order []string
	for _, member := range events[0].Members {
		order = append(order, member.PartTitle)
	}
	if events[0].Name != "Great Night" || !reflect.DeepEqual(order, []string{"Great Night (1)", "Great Night (2)"}) {
		t.Errorf("event = %q %v; want the franchise's two episodes in part order, not the unrelated series'", events[0].Name, order)
	}
	if again, err := crossovers.DetectEvents(t.Context(), "tv", 7); err != nil || again != 0 {
		t.Errorf("a second pass made %d, %v; the episodes are in an event already", again, err)
	}

	if err := crossovers.Delete(t.Context(), events[0].ID); err != nil {
		t.Fatal(err)
	}
	if again, err := crossovers.DetectEvents(t.Context(), "tv", 7); err != nil || again != 0 {
		t.Errorf("after a person deleted it the pass made %d, %v; it is not found again", again, err)
	}
}
