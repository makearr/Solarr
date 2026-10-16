package library_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestReleaseFilters_ReplaceMakesTheGivenRulesTheWholeSet(t *testing.T) {
	opened, _ := newLibrary(t)
	filters := library.NewReleaseFilters(opened)
	work := createWork(t, opened, "tv", "Invented Show")
	other := createWork(t, opened, "tv", "Invented Spinoff")

	first := []library.ReleaseFilter{
		{Kind: "must_contain", Value: "1080p"},
		{Prefix: "s02", Kind: "group_deny", Value: "FGT", IsRegex: true},
		{Kind: "prefer", Value: "NTb", Score: 10},
	}
	if err := filters.Replace(t.Context(), work.ID, first); err != nil {
		t.Fatal(err)
	}
	if err := filters.Replace(t.Context(), other.ID, first[:1]); err != nil {
		t.Fatal(err)
	}
	if got, err := filters.List(t.Context(), work.ID); err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("list = %+v, %v; want %+v", got, err, first)
	}

	second := []library.ReleaseFilter{{Kind: "must_not_contain", Value: "H264"}}
	if err := filters.Replace(t.Context(), work.ID, second); err != nil {
		t.Fatal(err)
	}
	if got, _ := filters.List(t.Context(), work.ID); !reflect.DeepEqual(got, second) {
		t.Errorf("after replace = %+v, want %+v", got, second)
	}
	if got, _ := filters.List(t.Context(), other.ID); len(got) != 1 {
		t.Errorf("another work's rules = %+v, want them untouched", got)
	}
}
