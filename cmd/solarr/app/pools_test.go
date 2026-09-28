package app

import (
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/poll"
	"github.com/makearr/Solarr/internal/importing/downloadwatch"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/jobs"
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
