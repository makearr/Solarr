package sabnzbd

import (
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// Only Completed and Failed end a history entry; every post-processing stage keeps it in flight.
func TestHistoryState_OnlyCompletedAndFailedEndAnEntry(t *testing.T) {
	for status, want := range map[string]download.State{
		"Completed": download.StateCompleted, "Failed": download.StateFailed,
		"Extracting": download.StateProcessing, "Verifying": download.StateProcessing, "Repairing": download.StateProcessing,
		"Moving": download.StateProcessing, "Running": download.StateProcessing, "QuickCheck": download.StateProcessing,
		"Fetching": download.StateProcessing, "Queued": download.StateProcessing,
	} {
		if got := historyState(status); got != want {
			t.Errorf("historyState(%q) = %q, want %q", status, got, want)
		}
	}
}
