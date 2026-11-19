package artwork_test

import (
	"io"
	"testing"

	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func put(t *testing.T, fs fsx.FS, name string) {
	t.Helper()
	if err := fs.MkdirAll("/covers", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsx.WriteAtomic(fs, name, func(w io.Writer) error { _, err := w.Write([]byte("x")); return err }); err != nil {
		t.Fatal(err)
	}
}

func TestReadPathFindsAFileInItsShardOrStillLoose(t *testing.T) {
	fs := fsx.NewMem(clock.NewFakeAtEpoch())
	put(t, fs, "/covers/work-1-cover-full.webp")
	if got := artwork.ReadPath(fs, "/covers", "work-1-cover-full.webp"); got != "/covers/work-1-cover-full.webp" {
		t.Errorf("loose file read from %q", got)
	}

	moved, err := artwork.ShardCovers(fs, "/covers")
	if err != nil || moved != 1 {
		t.Fatalf("ShardCovers = %d, %v; want the one loose file moved", moved, err)
	}
	want := artwork.CoverPath("/covers", "work-1-cover-full.webp")
	if got := artwork.ReadPath(fs, "/covers", "work-1-cover-full.webp"); got != want {
		t.Errorf("moved file read from %q, want %q", got, want)
	}
	if again, _ := artwork.ShardCovers(fs, "/covers"); again != 0 {
		t.Errorf("second ShardCovers moved %d, want 0", again)
	}
}
