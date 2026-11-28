package music_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/mediatool"

	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/media/music"
)

func TestImporter_Fit(t *testing.T) {
	importer := music.NewImporter(music.ImporterConfig{})
	for name, tc := range map[string]struct {
		files []string
		want  float64
	}{
		"an album":                {[]string{"A/01.flac", "A/02.mp3"}, batch.FitLikely},
		"an audiobook's own file": {[]string{"B/book.m4b"}, batch.FitPossible},
		"a video among the sound": {[]string{"A/01.flac", "A/clip.mkv"}, 0},
		"videos alone":            {[]string{"A/clip.mkv", "A/live.mp4"}, batch.FitPossible},
	} {
		unit := inspect.Unit{}
		for _, file := range tc.files {
			unit.Media = append(unit.Media, inspect.File{RelPath: file})
		}
		if got := importer.Fit(unit); got != tc.want {
			t.Errorf("%s: fit %v, want %v", name, got, tc.want)
		}
	}
}

// recordingVideoCommitter stands in for the committer of a batch of video.
type recordingVideoCommitter struct {
	music.UnitCommitter
	music.UnverifiedCommitter
	units []inspect.Unit
}

func (r *recordingVideoCommitter) CommitVideoUnit(_ context.Context, _ string, unit inspect.Unit, _ []mediatool.Probe, _ int64, _ []string) (music.Placed, error) {
	r.units = append(r.units, unit)
	return music.Placed{WorkID: 42, WorkCreated: true}, nil
}

// A unit of video is never matched as an album: it waits as a queued unit and, when unidentified units are added, lands as video works.
func TestImporter_ImportBatch_AVideoUnitIsAddedUnverifiedNotMatchedAsAnAlbum(t *testing.T) {
	committer := &recordingVideoCommitter{}
	importer := music.NewImporter(music.ImporterConfig{Tool: fakeTagTool{}, Committer: committer})
	unit := inspect.Unit{Directory: "Rel", Media: []inspect.File{{RelPath: "Rel/Katupoken - Sesoge.mkv", SizeBytes: 5}}}

	result, err := importer.ImportBatch(t.Context(), batch.Request{
		SourcePath: "/in", CategoryID: 3, Units: []inspect.Unit{unit}, UnitIDs: []int64{9}, Threshold: 0.9, AddUnidentified: true,
	})
	if err != nil {
		t.Fatalf("ImportBatch: %v", err)
	}
	wantDecision := library.UnitDecision{UnitID: 9, Decision: library.UnitQueue, Reason: music.ReasonVideoUnit}
	if !reflect.DeepEqual(result.Decisions, []library.UnitDecision{wantDecision}) {
		t.Errorf("decisions = %+v, want %+v", result.Decisions, wantDecision)
	}
	if len(committer.units) != 1 || result.Placements[0] == nil || result.Placements[0].WorkID != 42 {
		t.Errorf("committed %d units, placements %+v; want the video unit placed as work 42", len(committer.units), result.Placements)
	}
}
