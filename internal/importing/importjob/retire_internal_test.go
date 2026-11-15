package importjob

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Once an archive's contents are in the library every volume of it goes to the recycle bin, not to a folder of the
// user's that is emptied directly (API-2 step 3); one that cannot be moved stays and does not stop the rest.
func TestRetireArchives_SendsEveryVolumeToTheBinAndKeepsWhatCannotMove(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	for _, name := range []string{"Album.part01.rar", "Album.part02.rar", "Other.zip", "notes.txt"} {
		if err := fsys.MkdirAll("/dl/sub", 0o755); err != nil {
			t.Fatal(err)
		}
		file, err := fsys.Create("/dl/sub/" + name)
		if err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
	}
	var recycled []string
	job := &Job{fsys: fsys, logger: slog.New(slog.DiscardHandler), recycleArchive: func(_ context.Context, _ *int64, _, file string) error {
		if file == "/dl/sub/Other.zip" {
			return errors.New("read-only")
		}
		recycled = append(recycled, file)
		return nil
	}}
	job.retireArchives(t.Context(), "/dl", nil)
	slices.Sort(recycled)
	if want := []string{"/dl/sub/Album.part01.rar", "/dl/sub/Album.part02.rar"}; !slices.Equal(recycled, want) {
		t.Errorf("recycled = %v, want %v", recycled, want)
	}
}
