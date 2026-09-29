package importjob_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// timed skips a measurement outside `make bench`, the same rule
// internal/db/library/bench_test.go's own timed enforces -- a latency figure
// means nothing under the unit suite's parallel, -race-instrumented run.
func timed(tb testing.TB) {
	tb.Helper()
	if os.Getenv("SOLARR_BENCH") == "" {
		tb.Skip("timing measurement: run make bench, or set SOLARR_BENCH=1")
	}
}

// benchUnits is how many two-file "albums" the synthetic tree carries. Not
// ADR-22's 500k (that is library scale; a real adoption run's own cold/warm
// cost is disk and ffprobe time, already documented separately, notes.md's
// "a cold-cache walk of the real library is ~7x slower than warm") -- this is
// a scale that keeps make bench in the seconds and still resolves a real
// regression against the framework overhead this benchmark actually
// isolates: WALK/GROUP, one zero-cost fake probe call per cluster, and the
// two library.db write transactions AddUnits/SetUnitDecisions pay per
// cluster.
const benchUnits = 3_000

// perUnitTarget is the ADR-28 ceiling for this benchmark's own overhead --
// walking, grouping and two small write transactions per unit, with the
// ffprobe subprocess and any real filesystem cost stubbed to zero. It exists
// to catch a regression in *our* code (an accidental N+1 write, a lost
// batch), not to model real import throughput, which is dominated by ffprobe
// and disk I/O this benchmark deliberately does not include.
// Measured at ~1.9ms/unit on 2026-09-10 (see notes.md); this leaves headroom
// for CI noise while still catching a real regression -- reverting
// SetUnitDecisions to one transaction per unit, for instance, adds back the
// per-unit GetImportUnit read this benchmark's own single-unit-per-cluster
// tree exercises (a cluster with several units, the case that also drops the
// per-unit RecomputeImportSessionCounts call, is not this tree's shape).
const perUnitTarget = 3 * time.Millisecond

// zeroCostTool answers ReadTags instantly from an in-memory table, isolating
// the job's own overhead from the real ffprobe subprocess cost
// documentation/media-tools.md already measures separately.
type zeroCostTool struct{ byPath map[string]mediatool.Tags }

func (z *zeroCostTool) Available(context.Context) error { return nil }

func (z *zeroCostTool) ReadTags(_ context.Context, paths []string) ([]mediatool.Probe, error) {
	probes := make([]mediatool.Probe, len(paths))
	for i, p := range paths {
		probes[i] = mediatool.Probe{Path: p, Tags: z.byPath[p]}
	}
	return probes, nil
}

func (z *zeroCostTool) WriteTags(context.Context, []mediatool.TagEdit) ([]error, error) {
	return nil, nil
}

func (z *zeroCostTool) Fingerprint(context.Context, []string) ([]mediatool.Fingerprint, error) {
	return nil, nil
}

// buildBenchTree writes benchUnits albums of two files each into tool's own
// table, every album carrying its own confident, distinct release-group MBID
// so every unit auto-commits -- both AddUnits and SetUnitDecisions' COMMIT
// path run for every unit, not just the QUEUE default.
func buildBenchTree(t *testing.T, fsys *fsx.Mem, tool *zeroCostTool) {
	t.Helper()
	for i := range benchUnits {
		dir := fmt.Sprintf("Artist %d/Album %d", i, i)
		mbid := fmt.Sprintf("%08d-0000-4000-8000-000000000000", i)
		for _, name := range []string{"01 Track.flac", "02 Track.flac"} {
			absolute := filepath.Join(root, dir, name)
			writeFile(t, fsys, absolute, 65*1024)
			tool.byPath[absolute] = mediatool.Tags{"musicbrainz_releasegroupid": mbid}
		}
	}
}

func TestADR28_ImportJobProcessesUnitsUnderItsTarget(t *testing.T) {
	timed(t)

	fx := newFixture(t)
	tool := &zeroCostTool{byPath: map[string]mediatool.Tags{}}
	buildBenchTree(t, fx.fsys, tool)

	job := importjob.NewJob(importjob.Config{
		FS: fx.fsys, Sessions: library.NewImportSessions(fx.store), Policy: fixedPolicy(0.90),
		Tool: tool, Clock: fx.clock, Logger: slog.New(slog.DiscardHandler),
	})

	started := time.Now()
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	elapsed := time.Since(started)

	done, err := fx.sessions().List(t.Context(), library.ImportDone, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(done) != 1 || done[0].UnitsCommitted != benchUnits {
		t.Fatalf("session = %+v, want %d units committed", done, benchUnits)
	}

	perUnit := elapsed / benchUnits
	t.Logf("import job: %d units in %s (%s/unit), limit %s/unit", benchUnits, elapsed, perUnit, perUnitTarget)
	if perUnit > perUnitTarget {
		t.Errorf("import job took %s/unit, over the limit of %s/unit", perUnit, perUnitTarget)
	}
}
