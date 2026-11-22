package app

import (
	"io"
	"log/slog"
	"testing"

	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// ART-2: the registered task moves every picture lying loose in the folder into its shard, over several chunks.
func TestCoverShardJob_MovesEveryLoosePictureIntoItsShard(t *testing.T) {
	mem := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := mem.MkdirAll("/covers", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"aaaa.webp", "bbbb.webp", "cccc.webp"} {
		file, err := mem.Create("/covers/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, "x"); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	job := coverShardJob{fsys: mem, dir: "/covers", logger: slog.New(slog.DiscardHandler)}
	if err := job.Run(t.Context(), jobs.Job{Type: coverShardJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, name := range []string{"aaaa.webp", "bbbb.webp", "cccc.webp"} {
		if _, err := mem.Stat(artwork.CoverPath("/covers", name)); err != nil {
			t.Errorf("%s is not in its shard: %v", name, err)
		}
		if _, err := mem.Stat("/covers/" + name); err == nil {
			t.Errorf("%s is still loose", name)
		}
	}
}
