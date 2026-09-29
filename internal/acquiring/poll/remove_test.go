package poll_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/poll"
)

// Lidarr's "remove from queue": out of the client with its files, cancelled, in History, and
// nothing searched when the release is not blocklisted.
func TestRemove_TakesTheDownloadOutAndCancelsIt(t *testing.T) {
	e := newEnv(t)
	sent := e.send(t, "nzo-1")

	if err := e.job().Remove(t.Context(), sent.ID, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if got := e.state(t, sent.ID); got.State != poll.StateCancelled || got.LastError != "removed from the queue" {
		t.Errorf("state = %s (%q), want CANCELLED (removed from the queue)", got.State, got.LastError)
	}
	if !slices.Equal(e.client.removed, []string{"nzo-1+files"}) {
		t.Errorf("removed = %v, want the download and its files", e.client.removed)
	}
	if !slices.Equal(e.history, []string{"download.removed nzo-1 "}) {
		t.Errorf("history = %v", e.history)
	}
	if len(e.searched) != 0 {
		t.Errorf("searched %v, want nothing searched without a blocklist", e.searched)
	}
	blocked, err := e.blocklist.IsBlocked(t.Context(), sent.IndexerID, sent.CandidateGUID, "", 2)
	if err != nil || blocked {
		t.Errorf("IsBlocked = %v (%v), want the release still allowed", blocked, err)
	}
}

// With blocklist, the release is never grabbed again and the album is searched for another.
func TestRemove_WithBlocklistBlocksTheReleaseAndSearchesAgain(t *testing.T) {
	e := newEnv(t)
	sent := e.send(t, "nzo-1")

	if err := e.job().Remove(t.Context(), sent.ID, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	blocked, err := e.blocklist.IsBlocked(t.Context(), sent.IndexerID, sent.CandidateGUID, "", 1<<40)
	if err != nil || !blocked {
		t.Errorf("IsBlocked = %v (%v), want blocked for good", blocked, err)
	}
	if !slices.Equal(e.searched, []int64{*sent.DesiredItemID}) {
		t.Errorf("searched = %v, want the album searched again", e.searched)
	}
	if !slices.Equal(e.history, []string{"download.removed nzo-1 blocklisted"}) {
		t.Errorf("history = %v", e.history)
	}
}

// Files already moving into the library are not pulled out from under the import.
func TestRemove_RefusesADownloadBeingImported(t *testing.T) {
	e := newEnv(t)
	sent := e.send(t, "nzo-1")
	if err := e.acquisitions.SetState(t.Context(), sent.ID, poll.StateImporting, "", 2); err != nil {
		t.Fatal(err)
	}

	if err := e.job().Remove(t.Context(), sent.ID, false); !errors.Is(err, poll.ErrNotRemovable) {
		t.Errorf("Remove = %v, want ErrNotRemovable", err)
	}
	if len(e.client.removed) != 0 || e.state(t, sent.ID).State != poll.StateImporting {
		t.Errorf("removed %v, state %s: want nothing touched", e.client.removed, e.state(t, sent.ID).State)
	}
}

// The next poll no longer sees the download in the client; a removal is not a failure.
func TestRemove_ALaterPollLeavesItCancelled(t *testing.T) {
	e := newEnv(t)
	sent := e.send(t, "nzo-1")
	e.client.downloads = []download.Download{{ClientRef: "nzo-1", Name: "Heka-Zanuvode", State: download.StateDownloading}}
	if err := e.job().Remove(t.Context(), sent.ID, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	e.client.downloads = nil

	e.run(t)

	if got := e.state(t, sent.ID).State; got != poll.StateCancelled {
		t.Errorf("state = %s, want CANCELLED", got)
	}
	if len(e.history) != 1 || len(e.searched) != 0 {
		t.Errorf("history %v, searched %v: want only the removal", e.history, e.searched)
	}
}
