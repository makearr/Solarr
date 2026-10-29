package library_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestImportListsKeepEntriesAndStatePerListAndForgetThem(t *testing.T) {
	opened, _ := newLibrary(t)
	lists := library.NewImportLists(opened)
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	if state, err := lists.State(t.Context(), 1); err != nil || state != (library.ImportListState{ListID: 1}) {
		t.Fatalf("never run: state = %+v, err = %v", state, err)
	}
	first := library.ImportListEntry{ListID: 1, Provider: "tmdb", ExternalID: "1", Outcome: library.ListUnresolved, Title: "A", SeenAt: 1}
	other := library.ImportListEntry{ListID: 2, Provider: "tmdb", ExternalID: "1", Outcome: library.ListAdded, WorkID: 9, Title: "A", SeenAt: 1}
	if err := lists.Record(t.Context(), []library.ImportListEntry{first, other}); err != nil {
		t.Fatal(err)
	}
	resolved := first
	resolved.Outcome, resolved.WorkID, resolved.SeenAt = library.ListAdded, 5, 2
	if err := lists.Record(t.Context(), []library.ImportListEntry{resolved}); err != nil {
		t.Fatal(err)
	}
	if err := lists.RecordPreview(t.Context(), 1, at, "tmdb_list\n{}"); err != nil {
		t.Fatal(err)
	}
	if err := lists.RecordRun(t.Context(), 1, at.Add(time.Hour), 1, ""); err != nil {
		t.Fatal(err)
	}
	entries, err := lists.Entries(t.Context(), 1)
	if err != nil || !reflect.DeepEqual(entries, []library.ImportListEntry{resolved}) {
		t.Errorf("entries = %+v, err = %v; want the entry once, as last recorded", entries, err)
	}
	state, _ := lists.State(t.Context(), 1)
	want := library.ImportListState{ListID: 1, PreviewedOptions: "tmdb_list\n{}", PreviewedAt: at.Unix(), RunAt: at.Add(time.Hour).Unix(), RunAdded: 1}
	if state != want {
		t.Errorf("state = %+v, want %+v: a run keeps the preview", state, want)
	}

	if err := lists.Forget(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if entries, _ := lists.Entries(t.Context(), 1); len(entries) != 0 {
		t.Errorf("forgotten list kept %+v", entries)
	}
	if state, _ := lists.State(t.Context(), 1); state != (library.ImportListState{ListID: 1}) {
		t.Errorf("forgotten list kept state %+v", state)
	}
	if entries, _ := lists.Entries(t.Context(), 2); !reflect.DeepEqual(entries, []library.ImportListEntry{other}) {
		t.Errorf("another list's entries = %+v", entries)
	}
}
