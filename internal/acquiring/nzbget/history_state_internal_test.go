package nzbget

import (
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// Every status NZBGet documents for a history entry, with the whole state it maps to.
func TestHistoryState_CoversEveryDocumentedStatus(t *testing.T) {
	for status, want := range map[string]download.State{
		"SUCCESS/ALL": download.StateCompleted, "SUCCESS/UNPACK": download.StateCompleted, "SUCCESS/PAR": download.StateCompleted,
		"SUCCESS/HEALTH": download.StateCompleted, "SUCCESS/GOOD": download.StateCompleted, "SUCCESS/MARK": download.StateCompleted,
		"WARNING/SCRIPT": download.StateCompleted, "WARNING/SPACE": download.StateFailed, "WARNING/PASSWORD": download.StateFailed,
		"WARNING/DAMAGED": download.StateFailed, "WARNING/REPAIRABLE": download.StateFailed, "WARNING/HEALTH": download.StateFailed,
		"FAILURE/PAR": download.StateFailed, "FAILURE/UNPACK": download.StateFailed, "FAILURE/MOVE": download.StateFailed,
		"FAILURE/SCAN": download.StateFailed, "FAILURE/BAD": download.StateFailed, "FAILURE/HEALTH": download.StateFailed,
		"FAILURE/FETCH": download.StateFailed, "FAILURE/HIDDEN": download.StateFailed,
		"DELETED/MANUAL": download.StateRemoved, "DELETED/DUPE": download.StateRemoved, "DELETED/COPY": download.StateRemoved,
		"DELETED/GOOD": download.StateRemoved, "DELETED/BAD": download.StateFailed, "DELETED/SCAN": download.StateFailed,
		"DELETED/HEALTH": download.StateFailed,
	} {
		if got := historyState(status); got != want {
			t.Errorf("historyState(%q) = %q, want %q", status, got, want)
		}
	}
}
