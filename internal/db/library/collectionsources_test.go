package library_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func TestWorksToCheckAsksAWorkAgainOnlyOnceItsAnswerIsOlderThanTheAge(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	opened, _ := newLibraryAt(t, fake)
	franchises := library.NewFranchises(opened)
	var ids []int64
	for _, external := range []string{"101", "102"} {
		stored, err := library.NewSeries(opened).Upsert(t.Context(), fake.Now(), library.SeriesWrite{
			Work: library.WorkIdentity{MediaType: "tv", CanonicalTitle: "Show " + external, SortTitle: "show " + external},
			IDs:  []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: external}},
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, stored.WorkID)
	}
	due := func(maxAge time.Duration) []string {
		t.Helper()
		works, err := franchises.WorksToCheck(t.Context(), "tv", "tvdb", "lists", maxAge, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, work := range works {
			out = append(out, work.ExternalID)
		}
		return out
	}
	if err := franchises.MarkChecked(t.Context(), "lists", ids[:1]); err != nil {
		t.Fatal(err)
	}
	if got := due(30 * 24 * time.Hour); !reflect.DeepEqual(got, []string{"102"}) {
		t.Errorf("due = %v, want the one never asked", got)
	}
	fake.Advance(31 * 24 * time.Hour)
	if got := due(30 * 24 * time.Hour); !reflect.DeepEqual(got, []string{"101", "102"}) {
		t.Errorf("due a month on = %v, want both", got)
	}
	if got := due(0); !reflect.DeepEqual(got, []string{"102"}) {
		t.Errorf("due with no age = %v, want the one never asked: asked once is enough", got)
	}
}
