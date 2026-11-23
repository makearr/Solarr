package library_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestSeriesUpsertSetsKeepsAndClearsTheProvisionalMark(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	works := library.NewWorks(opened)
	steps := []struct {
		name string
		mark library.MetadataMark
		want bool
	}{
		{"only a media server answered", library.MarkProvisional, true},
		{"a parts-only write leaves it", library.MarkKept, true},
		{"another provider answered", library.MarkSettled, false},
		{"a parts-only write does not set it", library.MarkKept, false},
		{"the media server alone again", library.MarkProvisional, true},
	}
	for index, step := range steps {
		write := sampleSeries()
		write.Mark = step.mark
		write.Description = step.name
		result, err := series.Upsert(t.Context(), seriesAt.Add(time.Duration(index)*time.Hour), write)
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		detail, err := works.Detail(t.Context(), result.WorkID)
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if detail.Provisional != step.want {
			t.Fatalf("%s: provisional = %v, want %v", step.name, detail.Provisional, step.want)
		}
	}
}

func TestMarkOfAMerge(t *testing.T) {
	if got := library.MarkOf(true); got != library.MarkProvisional {
		t.Errorf("MarkOf(true) = %v, want MarkProvisional", got)
	}
	if got := library.MarkOf(false); got != library.MarkSettled {
		t.Errorf("MarkOf(false) = %v, want MarkSettled", got)
	}
}
