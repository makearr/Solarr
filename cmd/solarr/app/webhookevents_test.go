package app

import (
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/service/webhook"
)

func TestHealthChanges(t *testing.T) {
	raised, cleared := healthChanges(map[string]string{"a": "x", "b": "y"}, map[string]string{"b": "y", "c": "z", "d": "w"})
	if !slices.Equal(raised, []string{"c", "d"}) || !slices.Equal(cleared, []string{"a"}) {
		t.Errorf("raised %v cleared %v", raised, cleared)
	}
	raised, cleared = healthChanges(map[string]string{"a": "x"}, map[string]string{"a": "changed message"})
	if len(raised) != 0 || len(cleared) != 0 {
		t.Errorf("a changed message is no change: %v %v", raised, cleared)
	}
}

func TestRecycledOccurrence(t *testing.T) {
	got := recycledOccurrence(library.Entry{OriginalPath: "Series/Series v02.cbz", SizeBytes: 892})
	want := webhook.Occurrence{Event: webhook.EventFileRecycled, Files: []webhook.File{{Name: "Series v02.cbz", Size: 892, Path: "Series/Series v02.cbz"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v want %+v", got, want)
	}
}
