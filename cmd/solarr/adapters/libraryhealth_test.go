package adapters

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

// DB-8: the health list shows what ffprobe printed for a file it could not read, and nothing for one never tried.
func TestLibraryHealthShowsWhatFfprobeSaidForAFileItCouldNotRead(t *testing.T) {
	opened := newPushLibrary(t)
	physical := library.NewPhysical(opened)
	ids, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "a-broken.flac", SizeBytes: 5, QuickHash: "a", FirstSeenAt: time.Unix(0, 0)},
		{RelPath: "b-new.flac", SizeBytes: 5, QuickHash: "b", FirstSeenAt: time.Unix(0, 0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := physical.RecordProbeFailure(t.Context(), ids[0].MediaObjectID, 5, "no streams", `{ "streams": [] }`, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}

	files, err := (LibraryHealth{Physical: physical}).unprobedSample(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.RelPath] = f.FFprobeOutput
	}
	want := map[string]string{"a-broken.flac": `{ "streams": [] }`, "b-new.flac": ""}
	if len(got) != 2 || got["a-broken.flac"] != want["a-broken.flac"] || got["b-new.flac"] != "" {
		t.Errorf("unprobed sample = %v, want %v", got, want)
	}
}
