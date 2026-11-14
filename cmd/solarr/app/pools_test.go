package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/poll"
	"github.com/makearr/Solarr/internal/importing/downloadwatch"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/moverjob"
	"github.com/makearr/Solarr/internal/platform/paginate"
	"github.com/makearr/Solarr/internal/scanning/probe"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// The maintenance pool has one slot and runs hours-long scans and probes: nothing a download
// needs to move on may wait there (found 2026-09-28, a finished download behind every rescan).
func TestTheDownloadLoopNeverWaitsInTheMaintenancePool(t *testing.T) {
	for _, definition := range []jobs.Definition{poll.Definition, downloadwatch.Definition, importjob.Definition} {
		if definition.Pool == jobs.PoolMaintenance {
			t.Errorf("%s runs in the maintenance pool", definition.Type)
		}
	}
	if importjob.Definition.Pool != jobs.PoolImport {
		t.Errorf("imports run in %s, want the import pool and its one slot (ADR-3)", importjob.Definition.Pool)
	}
}

// A job type whose pool has no workers is accepted, queued and never run. Every registered type must have some.
func TestEveryRegisteredJobTypeHasWorkers(t *testing.T) {
	server := newGoldenServer(t)
	staffed := server.process.dispatcher.InFlight()
	for _, jobType := range server.process.jobRegistry.Types() {
		definition, _ := server.process.jobRegistry.Lookup(jobType)
		if _, ok := staffed[definition.Pool]; !ok {
			t.Errorf("%s runs in pool %q, which the dispatcher gives no workers", jobType, definition.Pool)
		}
	}
}

// What the split is for: a sweep, the probe, a move and the nightly bookkeeping each wait only for their own kind.
func TestLongLibraryWorkDoesNotShareOnePool(t *testing.T) {
	server := newGoldenServer(t)
	pool := func(jobType jobs.Type) jobs.Pool {
		definition, found := server.process.jobRegistry.Lookup(jobType)
		if !found {
			t.Fatalf("job type %s is not registered", jobType)
		}
		return definition.Pool
	}
	got := map[string]jobs.Pool{
		"scan": pool(scan.JobType), "rescan": pool(scan.RescanJobType), "probe": pool(probe.JobType), "move": pool(moverjob.JobType),
		"statistics": pool(statisticsJobType), "database maintenance": pool(databaseMaintenanceJobType),
		"backup": pool(backupJobType),
	}
	want := map[string]jobs.Pool{
		"scan": jobs.PoolScan, "rescan": jobs.PoolRescan, "probe": jobs.PoolFiles, "move": jobs.PoolMove,
		"statistics": jobs.PoolHousekeeping, "database maintenance": jobs.PoolMaintenance,
		"backup": jobs.PoolMaintenance,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pools = %v\nwant %v", got, want)
	}
}

// A burst of sweeps and imports asks for fresh statistics many times; the library gets one job, a few minutes out.
func TestEnqueueAnalyze_FoldsABurstIntoOneJobFiveMinutesOut(t *testing.T) {
	server := newGoldenServer(t)
	before := time.Now()
	server.process.enqueueAnalyze(t.Context())
	server.process.enqueueAnalyze(t.Context())

	page, err := server.process.queue.ListPage(t.Context(), jobs.Visibility{System: true}, "", paginate.Cursor{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found []jobs.Job
	for _, job := range page.Items {
		if job.Type == databaseAnalyzeJobType {
			found = append(found, job)
		}
	}
	if len(found) != 1 || found[0].RunAfter.Before(before.Add(analyzeDelay-time.Minute)) {
		t.Errorf("statistics jobs = %+v, want one run no sooner than %v", found, analyzeDelay)
	}
}
