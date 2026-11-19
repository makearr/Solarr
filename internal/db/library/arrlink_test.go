package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestArrLinks_ForgetDropsOnlyThatConnectionsLinks(t *testing.T) {
	opened, _ := newLibrary(t)
	links := library.NewArrLinks(opened)
	for _, instance := range []int64{1, 2} {
		if err := links.Link(t.Context(), instance, library.ArrLink{SourceKind: "artist", SourceID: "42", LocalKind: "person", LocalID: 7}); err != nil {
			t.Fatal(err)
		}
	}
	if err := links.SetSynced(t.Context(), 1, library.ArrLink{SourceKind: "artist", SourceID: "42"}, true); err != nil {
		t.Fatal(err)
	}
	got, err := links.ForInstance(t.Context(), 1)
	synced := true
	want := []library.ArrLink{{SourceKind: "artist", SourceID: "42", LocalKind: "person", LocalID: 7, SyncedMonitored: &synced}}
	if err != nil || len(got) != 1 || got[0].SyncedMonitored == nil || *got[0].SyncedMonitored != true || got[0].LocalID != want[0].LocalID {
		t.Fatalf("links of 1 = %+v, %v", got, err)
	}
	if err := links.Forget(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if got, _ := links.ForInstance(t.Context(), 1); len(got) != 0 {
		t.Errorf("after Forget: %+v", got)
	}
	if got, _ := links.ForInstance(t.Context(), 2); len(got) != 1 {
		t.Errorf("another connection's links: %+v", got)
	}
}
