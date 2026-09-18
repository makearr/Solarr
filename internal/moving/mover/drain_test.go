package mover_test

import (
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/placement"
)

func drainCandidate(id int64, fsID string, freeBytes int64) placement.RootCandidate {
	return placement.RootCandidate{
		RootFolderID: id, FSID: fsID, Online: true, Writable: true, AcceptsNew: true,
		FreeBytes: freeBytes, TotalBytes: freeBytes * 2,
	}
}

func TestAffinityKeyForPath_UsesTheTopLevelDirectory(t *testing.T) {
	cases := map[string]string{
		"Artist/Album (2020)/01.flac": "Artist",
		"loose.flac":                  "loose.flac",
		"Show/Season 1/e01.mkv":       "Show",
	}
	for input, want := range cases {
		if got := mover.AffinityKeyForPath(input); got != want {
			t.Errorf("AffinityKeyForPath(%q) = %q, want %q", input, got, want)
		}
	}
}

// A single artist's whole discography moves together onto whichever
// destination has the most free space.
func TestPlanDrain_MovesOneAffinityGroupOntoTheDestinationWithMostSpace(t *testing.T) {
	req := mover.DrainRequest{
		Source: placement.RootCandidate{RootFolderID: 1, FSID: "fs-a", Online: true, Writable: true},
		Files: []mover.FileOnRoot{
			{MediaObjectID: 1, RelPath: "Artist/Album/01.flac", SizeBytes: 100},
			{MediaObjectID: 2, RelPath: "Artist/Album/02.flac", SizeBytes: 100},
		},
		Destinations: []placement.RootCandidate{
			drainCandidate(2, "fs-b", 500),
			drainCandidate(3, "fs-c", 900),
		},
	}
	plan, err := mover.PlanDrain(req)
	if err != nil {
		t.Fatalf("PlanDrain: %v", err)
	}
	if len(plan.Moves) != 2 || len(plan.Refused) != 0 {
		t.Fatalf("plan = %+v, want two clean moves", plan)
	}
	for _, m := range plan.Moves {
		if m.DestRootFolderID != 3 {
			t.Errorf("move %+v landed on root %d, want the most-free root 3", m, m.DestRootFolderID)
		}
		if m.AffinityKey != "Artist" {
			t.Errorf("AffinityKey = %q, want %q", m.AffinityKey, "Artist")
		}
	}
}

// Two affinity groups: the larger one is placed first and claims the
// most-free root, so the second (placed after PlanDrain's own local
// bookkeeping decrements that root's remaining space) lands elsewhere
// rather than both racing for the same starting snapshot.
func TestPlanDrain_LaterGroupsSeeEarlierGroupsOwnConsumedCapacity(t *testing.T) {
	req := mover.DrainRequest{
		Source: placement.RootCandidate{RootFolderID: 1, FSID: "fs-a", Online: true, Writable: true},
		Files: []mover.FileOnRoot{
			{MediaObjectID: 1, RelPath: "BigArtist/Album/01.flac", SizeBytes: 700},
			{MediaObjectID: 2, RelPath: "SmallArtist/Album/01.flac", SizeBytes: 100},
		},
		Destinations: []placement.RootCandidate{
			// Root 2 alone has the most free space, but not enough left
			// over for the second group once the first has landed there.
			drainCandidate(2, "fs-b", 750),
			drainCandidate(3, "fs-c", 600),
		},
	}
	plan, err := mover.PlanDrain(req)
	if err != nil {
		t.Fatalf("PlanDrain: %v", err)
	}
	if len(plan.Refused) != 0 {
		t.Fatalf("plan refused unexpectedly: %+v", plan.Refused)
	}
	dest := map[int64]int64{}
	for _, m := range plan.Moves {
		dest[m.MediaObjectID] = m.DestRootFolderID
	}
	if dest[1] != 2 {
		t.Errorf("big group landed on root %d, want 2 (most free at the time it was placed)", dest[1])
	}
	if dest[2] != 3 {
		t.Errorf("small group landed on root %d, want 3 (root 2's remaining space is too tight after the big group)", dest[2])
	}
}

// No destination can hold everything: the whole drain refuses, not just the
// group that would not fit.
func TestPlanDrain_RefusesTheWholeDrainWhenNoDestinationCanHoldAGroup(t *testing.T) {
	req := mover.DrainRequest{
		Source: placement.RootCandidate{RootFolderID: 1, FSID: "fs-a", Online: true, Writable: true},
		Files: []mover.FileOnRoot{
			{MediaObjectID: 1, RelPath: "Artist/Album/01.flac", SizeBytes: 1000},
			{MediaObjectID: 2, RelPath: "OtherArtist/Album/01.flac", SizeBytes: 50},
		},
		Destinations: []placement.RootCandidate{
			drainCandidate(2, "fs-b", 500),
		},
	}
	plan, err := mover.PlanDrain(req)
	if err != nil {
		t.Fatalf("PlanDrain: %v", err)
	}
	if len(plan.Moves) != 0 {
		t.Fatalf("plan.Moves = %+v, want none -- a drain refuses as a whole", plan.Moves)
	}
	if len(plan.Refused) != 2 {
		t.Fatalf("plan.Refused has %d entries, want 2 (every file across every group)", len(plan.Refused))
	}
	// "exceed the ... bytes the remaining roots can hold" is refusedDrainPlan's
	// own aggregate message, distinct from Plan's own generic per-group
	// guard text -- Plan's own drain-wide fallback also produces a reason
	// containing "drain refused", so asserting on that alone would not tell
	// the two apart (found via mutation testing: disabling this function's
	// own placeability check still passed a laxer version of this test,
	// since an unresolved destination falls through to Plan's own "not
	// known to this plan" guard, which drain-refuses just as well but with
	// no shortfall named in bytes).
	for _, r := range plan.Refused {
		if !strings.Contains(r.Reason, "exceed the") || !strings.Contains(r.Reason, "bytes short") {
			t.Errorf("reason %q does not name the drain-wide shortfall in bytes", r.Reason)
		}
	}
	if plan.Refused[0].Reason != plan.Refused[1].Reason {
		t.Errorf("refusal reasons differ across files (%q vs %q), want one shared drain-wide reason",
			plan.Refused[0].Reason, plan.Refused[1].Reason)
	}
}

// No destinations at all -- an empty root's own category has nowhere else
// to send its content -- refuses cleanly rather than panicking.
func TestPlanDrain_RefusesCleanlyWithNoDestinations(t *testing.T) {
	req := mover.DrainRequest{
		Source: placement.RootCandidate{RootFolderID: 1, FSID: "fs-a", Online: true, Writable: true},
		Files: []mover.FileOnRoot{
			{MediaObjectID: 1, RelPath: "Artist/Album/01.flac", SizeBytes: 100},
		},
	}
	plan, err := mover.PlanDrain(req)
	if err != nil {
		t.Fatalf("PlanDrain: %v", err)
	}
	if len(plan.Moves) != 0 || len(plan.Refused) != 1 {
		t.Fatalf("plan = %+v, want the one file refused and nothing moved", plan)
	}
}

// A read-only source without allow_rename refuses the whole drain --
// Plan's own existing guard, exercised here through PlanDrain's own
// composition rather than duplicated.
func TestPlanDrain_RefusesAReadOnlySourceWithoutAllowRename(t *testing.T) {
	req := mover.DrainRequest{
		Source:         placement.RootCandidate{RootFolderID: 1, FSID: "fs-a", Online: true, Writable: true},
		SourceReadOnly: true,
		Files: []mover.FileOnRoot{
			{MediaObjectID: 1, RelPath: "Artist/Album/01.flac", SizeBytes: 100},
		},
		Destinations: []placement.RootCandidate{drainCandidate(2, "fs-b", 1000)},
	}
	plan, err := mover.PlanDrain(req)
	if err != nil {
		t.Fatalf("PlanDrain: %v", err)
	}
	if len(plan.Moves) != 0 || len(plan.Refused) != 1 {
		t.Fatalf("plan = %+v, want the read-only source to refuse the whole drain", plan)
	}
}
