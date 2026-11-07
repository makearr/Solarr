package app

import (
	"context"
	"log/slog"
	"testing"

	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

type existing map[int64]bool

func (e existing) ExistingObjectIDs(_ context.Context, ids []int64) (map[int64]bool, error) {
	found := map[int64]bool{}
	for _, id := range ids {
		found[id] = e[id]
	}
	return found, nil
}

func TestPreviewCleanup_RemovesThePicturesOfDeletedFilesOnly(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	for _, path := range []string{"/previews/0/5-frame.jpg", "/previews/0/5-sheet.jpg", "/previews/0/6-frame.jpg", "/previews/2/2001-frame.jpg"} {
		putFile(t, fsys, path, "x")
	}
	job := previewCleanupJob{fs: fsys, dir: "/previews", physical: existing{6: true}, logger: slog.New(slog.DiscardHandler)}
	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for path, want := range map[string]bool{"/previews/0/5-frame.jpg": false, "/previews/0/5-sheet.jpg": false, "/previews/0/6-frame.jpg": true, "/previews/2/2001-frame.jpg": false} {
		_, err := fsys.Stat(path)
		if (err == nil) != want {
			t.Errorf("%s present = %v, want %v", path, err == nil, want)
		}
	}
	if _, err := fsys.Stat("/previews/2"); err == nil {
		t.Error("the emptied folder was left behind")
	}
}
