package app

import (
	"testing"

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
