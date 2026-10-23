package capacity_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/jobs/capacity"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

type reachState struct {
	online, auto bool
	reason       string
}

func reachOf(t *testing.T, cache *settings.Cache, id int64) reachState {
	t.Helper()
	root, found := cache.Snapshot().RootFolder(id)
	if !found {
		t.Fatalf("root folder %d is gone", id)
	}
	return reachState{online: root.Online, auto: root.OfflineAuto, reason: root.OfflineReason}
}

// An unreachable root goes offline as Solarr's own call and comes back once reached; an empty mount point that held
// files counts as unreachable (user, 2026-10-23).
func TestReachJob_TakesAnUnreachableRootOfflineAndBringsItBack(t *testing.T) {
	cache := newCache(t)
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/media/music/Artist", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	job := capacity.NewReachJob(capacity.Config{FS: fsys, Cache: cache})
	run := func() {
		t.Helper()
		if err := job.Run(t.Context(), jobs.Job{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}

	run()
	if got := reachOf(t, cache, root.ID); got != (reachState{online: true}) {
		t.Fatalf("reached with entries = %+v, want online", got)
	}

	if err := fsys.Remove("/media/music/Artist"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	run()
	want := reachState{online: false, auto: true, reason: "is empty although it held files: is the disk mounted?"}
	if got := reachOf(t, cache, root.ID); got != want {
		t.Fatalf("emptied = %+v, want %+v", got, want)
	}

	if err := fsys.MkdirAll("/media/music/Artist", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	run()
	if got := reachOf(t, cache, root.ID); got != (reachState{online: true}) {
		t.Errorf("back = %+v, want online again", got)
	}
}

// A root a person declared offline stays offline whatever a probe finds (Invariant 87).
func TestReachJob_NeverLiftsAPersonsOfflineDeclaration(t *testing.T) {
	cache := newCache(t)
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/media/music/Artist", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	if err := cache.SetRootFolderOnline(t.Context(), root.ID, false); err != nil {
		t.Fatalf("SetRootFolderOnline: %v", err)
	}
	if err := capacity.NewReachJob(capacity.Config{FS: fsys, Cache: cache}).Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := reachOf(t, cache, root.ID); got != (reachState{}) {
		t.Errorf("declared offline, then reached = %+v, want still offline and a person's call", got)
	}
}

func TestReach_NamesWhyARootCannotBeReached(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/media/empty", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if reached, _, _ := capacity.Reach(fsys, settings.RootFolder{Path: "/media/missing"}); reached {
		t.Error("a missing path was reached")
	}
	if reached, _, entries := capacity.Reach(fsys, settings.RootFolder{Path: "/media/empty"}); !reached || entries != 0 {
		t.Errorf("a new empty root = reached %v, entries %d; want reached, nothing in it", reached, entries)
	}
}
