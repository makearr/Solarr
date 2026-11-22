package app

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"

	"github.com/makearr/Solarr/internal/jobs"
)

// The held tasks are released only when no scan is waiting or running, whatever else the queue holds (TRACK-18).
func TestScansIdle(t *testing.T) {
	for name, test := range map[string]struct {
		counts map[jobs.State]map[jobs.Pool]int64
		want   bool
	}{
		"nothing queued":            {map[jobs.State]map[jobs.Pool]int64{}, true},
		"only an import is running": {map[jobs.State]map[jobs.Pool]int64{jobs.StateRunning: {jobs.PoolImport: 2}}, true},
		"a finished scan":           {map[jobs.State]map[jobs.Pool]int64{jobs.StateSucceeded: {jobs.PoolScan: 4}}, true},
		"a scan waiting":            {map[jobs.State]map[jobs.Pool]int64{jobs.StatePending: {jobs.PoolScan: 1}}, false},
		"a scan running":            {map[jobs.State]map[jobs.Pool]int64{jobs.StateRunning: {jobs.PoolScan: 1}}, false},
		"a rescan leased":           {map[jobs.State]map[jobs.Pool]int64{jobs.StateLeased: {jobs.PoolRescan: 1}}, false},
	} {
		if got := scansIdle(test.counts); got != test.want {
			t.Errorf("%s: idle = %v, want %v", name, got, test.want)
		}
	}
}

// A finished single-file download is imported as the one file of its folder; a folder download whole (V1).
func TestUntrackedImportPayload(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	putFile(t, fsys, "/dl/solarr/Movie.mp4", "x")
	putFile(t, fsys, "/dl/solarr/Album/01.flac", "x")

	got := untrackedImportPayload(fsys, "/dl/solarr/Movie.mp4")
	want := importjob.Payload{SourcePath: "/dl/solarr", Origin: library.ImportOriginWatched, Only: []string{"Movie.mp4"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("file download = %+v, want %+v", got, want)
	}
	got = untrackedImportPayload(fsys, "/dl/solarr/Album")
	want = importjob.Payload{SourcePath: "/dl/solarr/Album", Origin: library.ImportOriginWatched}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("folder download = %+v, want %+v", got, want)
	}
}
