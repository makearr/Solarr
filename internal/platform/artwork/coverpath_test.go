package artwork_test

import (
	"fmt"
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

func TestCoverPathIsTheFixedShardOfTheName(t *testing.T) {
	if got := artwork.CoverPath("/covers", "abc-full.webp"); got != "/covers/7d/abc-full.webp" {
		t.Errorf("CoverPath = %q, want the fnv-1a shard 7d", got)
	}
}

func TestReadPathPrefersTheShardWhenBothExist(t *testing.T) {
	fs := fsx.NewMem(clock.NewFakeAtEpoch())
	const name = "work-2-cover-full.webp"
	put(t, fs, "/covers/"+name)
	sharded, err := artwork.ShardedFor(fs, "/covers", name)
	if err != nil {
		t.Fatal(err)
	}
	put(t, fs, sharded)
	if got := artwork.ReadPath(fs, "/covers", name); got != sharded {
		t.Errorf("ReadPath = %q, want the shard copy %q", got, sharded)
	}
}

func TestShardCoversMovesPicturesOnlyAndAChunkAtATime(t *testing.T) {
	fs := fsx.NewMem(clock.NewFakeAtEpoch())
	put(t, fs, "/covers/notes.txt")
	for index := range 1001 {
		put(t, fs, fmt.Sprintf("/covers/cover-%04d-full.webp", index))
	}
	first, err := artwork.ShardCovers(fs, "/covers")
	if err != nil || first != 1000 {
		t.Fatalf("first ShardCovers = %d, %v; want one chunk of 1000", first, err)
	}
	if second, _ := artwork.ShardCovers(fs, "/covers"); second != 1 {
		t.Errorf("second ShardCovers = %d, want the last 1", second)
	}
	if _, err := fs.Stat("/covers/notes.txt"); err != nil {
		t.Errorf("a file that is not a picture was moved: %v", err)
	}
}
