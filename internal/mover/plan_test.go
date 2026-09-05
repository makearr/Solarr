package mover_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/mover"
	"github.com/makearr/MediaNexus/internal/placement"
)

func onlineWritable(id int64, fsID string, freeBytes int64) placement.RootCandidate {
	return placement.RootCandidate{
		RootFolderID: id, FSID: fsID, Online: true, Writable: true, FreeBytes: freeBytes, TotalBytes: freeBytes * 2,
	}
}

// A single file with no group and an affordable destination moves, and
// SameFilesystem reflects whether source and destination share an FSID.
func TestPlan_MovesASingleFile(t *testing.T) {
	req := mover.Request{
		Reason: mover.ReasonRename,
		Files: []mover.FileToMove{
			{
				MediaObjectID: 1, SourceRootFolderID: 1, SourceRelPath: "Old/track.flac",
				SizeBytes: 100, AffinityKey: "work:1", DestRootFolderID: 1, DestRelPath: "New/track.flac",
			},
		},
		Roots: map[int64]placement.RootCandidate{1: onlineWritable(1, "fs-a", 1000)},
	}
	plan, err := mover.Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Moves) != 1 || len(plan.Refused) != 0 {
		t.Fatalf("plan = %+v, want one clean move", plan)
	}
	if !plan.Moves[0].SameFilesystem {
		t.Error("SameFilesystem = false, want true: source and destination share an fs id")
	}
}

func TestPlan_DifferentFilesystemsAreNotSameFilesystem(t *testing.T) {
	req := mover.Request{
		Reason: mover.ReasonCategory,
		Files: []mover.FileToMove{
			{MediaObjectID: 1, SourceRootFolderID: 1, SizeBytes: 100, AffinityKey: "a", DestRootFolderID: 2},
		},
		Roots: map[int64]placement.RootCandidate{
			1: onlineWritable(1, "fs-a", 1000),
			2: onlineWritable(2, "fs-b", 1000),
		},
	}
	plan, err := mover.Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Moves) != 1 || plan.Moves[0].SameFilesystem {
		t.Fatalf("plan = %+v, want SameFilesystem=false across different fs ids", plan)
	}
}

// Whole artists move together: two files sharing an affinity key must both
// appear or both be refused, never split.
func TestPlan_KeepsOneAffinityGroupTogether(t *testing.T) {
	req := mover.Request{
		Reason: mover.ReasonRebalance,
		Files: []mover.FileToMove{
			{MediaObjectID: 1, SourceRootFolderID: 1, SizeBytes: 100, AffinityKey: "artist:1", DestRootFolderID: 2},
			{MediaObjectID: 2, SourceRootFolderID: 1, SizeBytes: 100, AffinityKey: "artist:1", DestRootFolderID: 2},
		},
		Roots: map[int64]placement.RootCandidate{
			1: onlineWritable(1, "fs-a", 1000),
			2: onlineWritable(2, "fs-b", 1000),
		},
	}
	plan, err := mover.Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Moves) != 2 {
		t.Fatalf("got %d moves, want both files in the one affinity group", len(plan.Moves))
	}
}

// Larger affinity groups are ordered first, so Apply (built later) evacuates
// the biggest chunks of a drain first, exactly ADR-51's own requirement.
func TestPlan_OrdersLargestAffinityGroupFirst(t *testing.T) {
	req := mover.Request{
		Reason: mover.ReasonDrain,
		Files: []mover.FileToMove{
			{MediaObjectID: 1, SourceRootFolderID: 1, SizeBytes: 10, AffinityKey: "small", DestRootFolderID: 2},
			{MediaObjectID: 2, SourceRootFolderID: 1, SizeBytes: 500, AffinityKey: "big", DestRootFolderID: 2},
			{MediaObjectID: 3, SourceRootFolderID: 1, SizeBytes: 500, AffinityKey: "big", DestRootFolderID: 2},
		},
		Roots: map[int64]placement.RootCandidate{
			1: onlineWritable(1, "fs-a", 2000),
			2: onlineWritable(2, "fs-b", 2000),
		},
	}
	plan, err := mover.Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Moves) != 3 {
		t.Fatalf("got %d moves, want 3", len(plan.Moves))
	}
	if plan.Moves[0].AffinityKey != "big" || plan.Moves[1].AffinityKey != "big" {
		t.Errorf("moves = %+v, want the 1000-byte \"big\" group first", plan.Moves)
	}
}

func TestPlan_RefusesIntoAReadOnlyDestination(t *testing.T) {
	dest := onlineWritable(2, "fs-b", 1000)
	dest.ReadOnly = true
	req := mover.Request{
		Reason: mover.ReasonRename,
		Files: []mover.FileToMove{
			{MediaObjectID: 1, SourceRootFolderID: 1, SizeBytes: 100, AffinityKey: "a", DestRootFolderID: 2},
		},
		Roots: map[int64]placement.RootCandidate{1: onlineWritable(1, "fs-a", 1000), 2: dest},
	}
	plan, err := mover.Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Moves) != 0 || len(plan.Refused) != 1 {
		t.Fatalf("plan = %+v, want the move refused", plan)
	}
}

// A true in-place rename -- source and destination are the same read-only
// root -- is section 11.8's own one exception: allow_rename permits it,
// where a cross-root move into a read-only root never can (the test right
// above this one). Both roles are the same RootCandidate, so ReadOnly is
// set on the one entry both source and dest resolve to.
func TestPlan_AllowsARenameInPlaceOnAReadOnlyRootWithAllowRename(t *testing.T) {
	root := onlineWritable(1, "fs-a", 1000)
	root.ReadOnly = true
	req := mover.Request{
		Reason: mover.ReasonRename,
		Files: []mover.FileToMove{
			{MediaObjectID: 1, SourceRootFolderID: 1, SourceRelPath: "Old/track.flac",
				SizeBytes: 100, AffinityKey: "a", DestRootFolderID: 1, DestRelPath: "New/track.flac"},
		},
		Roots:             map[int64]placement.RootCandidate{1: root},
		SourceAllowRename: map[int64]bool{1: true},
	}
	plan, err := mover.Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Moves) != 1 || len(plan.Refused) != 0 {
		t.Fatalf("plan = %+v, want the in-place rename allowed", plan)
	}
}

// The same read-only root without allow_rename still refuses -- allow_rename
// is what section 11.8's table conditions the exception on, not read-only
// alone.
func TestPlan_RefusesARenameInPlaceOnAReadOnlyRootWithoutAllowRename(t *testing.T) {
	root := onlineWritable(1, "fs-a", 1000)
	root.ReadOnly = true
	req := mover.Request{
		Reason: mover.ReasonRename,
		Files: []mover.FileToMove{
			{MediaObjectID: 1, SourceRootFolderID: 1, SourceRelPath: "Old/track.flac",
				SizeBytes: 100, AffinityKey: "a", DestRootFolderID: 1, DestRelPath: "New/track.flac"},
		},
		Roots: map[int64]placement.RootCandidate{1: root},
	}
	plan, err := mover.Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Moves) != 0 || len(plan.Refused) != 1 {
		t.Fatalf("plan = %+v, want the in-place rename refused without allow_rename", plan)
	}
}

func TestPlan_RefusesOutOfAReadOnlySourceUnlessAllowRename(t *testing.T) {
	req := mover.Request{
		Reason: mover.ReasonRename,
		Files: []mover.FileToMove{
			{MediaObjectID: 1, SourceRootFolderID: 1, SizeBytes: 100, AffinityKey: "a", DestRootFolderID: 2},
		},
		Roots:          map[int64]placement.RootCandidate{1: onlineWritable(1, "fs-a", 1000), 2: onlineWritable(2, "fs-b", 1000)},
		SourceReadOnly: map[int64]bool{1: true},
	}
	plan, err := mover.Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Moves) != 0 || len(plan.Refused) != 1 {
		t.Fatalf("plan = %+v, want refused: read-only source, no allow_rename", plan)
	}

	req.SourceAllowRename = map[int64]bool{1: true}
	plan, err = mover.Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Moves) != 1 {
		t.Fatalf("plan = %+v, want the move allowed once allow_rename is set", plan)
	}
}

func TestPlan_RefusesWhenTheReserveWouldBeViolated(t *testing.T) {
	dest := onlineWritable(2, "fs-b", 150)
	dest.MinFreeBytes = 100
	req := mover.Request{
		Reason: mover.ReasonRebalance,
		Files: []mover.FileToMove{
			{MediaObjectID: 1, SourceRootFolderID: 1, SizeBytes: 100, AffinityKey: "a", DestRootFolderID: 2},
		},
		Roots: map[int64]placement.RootCandidate{1: onlineWritable(1, "fs-a", 1000), 2: dest},
	}
	plan, err := mover.Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Moves) != 0 || len(plan.Refused) != 1 {
		t.Fatalf("plan = %+v, want refused: 150-100=50 free after, below the 100 reserve", plan)
	}
}

// A same-root rename consumes no new capacity at all -- a rename(2) never
// duplicates bytes -- so a group whose total size would flunk the reserve
// check under the ordinary cross-root formula must still be allowed when
// source and destination are the same root.
func TestPlan_SameRootRenameIsExemptFromTheReserveCheck(t *testing.T) {
	root := onlineWritable(1, "fs-a", 150)
	root.MinFreeBytes = 100
	req := mover.Request{
		Reason: mover.ReasonRename,
		Files: []mover.FileToMove{
			{MediaObjectID: 1, SourceRootFolderID: 1, SourceRelPath: "Old/track.flac",
				SizeBytes: 100, AffinityKey: "a", DestRootFolderID: 1, DestRelPath: "New/track.flac"},
		},
		Roots: map[int64]placement.RootCandidate{1: root},
	}
	plan, err := mover.Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Moves) != 1 || len(plan.Refused) != 0 {
		t.Fatalf("plan = %+v, want the in-place rename allowed despite 150-100=50 being below the 100 reserve", plan)
	}
}

// Drain's own stated guarantee: any shortfall refuses every group, not just
// the one that could not fit -- "it does not fill the other disks and then
// fail". A non-drain reason must not have this behaviour: other groups still
// move even when one is refused.
func TestPlan_DrainRefusesTheWholeBatchOnAnyShortfall(t *testing.T) {
	tightDest := onlineWritable(2, "fs-b", 50)

	files := []mover.FileToMove{
		{MediaObjectID: 1, SourceRootFolderID: 1, SizeBytes: 500, AffinityKey: "too-big", DestRootFolderID: 2},
		{MediaObjectID: 2, SourceRootFolderID: 1, SizeBytes: 10, AffinityKey: "fits-fine", DestRootFolderID: 2},
	}
	roots := map[int64]placement.RootCandidate{1: onlineWritable(1, "fs-a", 1000), 2: tightDest}

	drainPlan, err := mover.Plan(mover.Request{Reason: mover.ReasonDrain, Files: files, Roots: roots})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(drainPlan.Moves) != 0 || len(drainPlan.Refused) != 2 {
		t.Fatalf("drain plan = %+v, want every file refused once any group does not fit", drainPlan)
	}

	rebalancePlan, err := mover.Plan(mover.Request{Reason: mover.ReasonRebalance, Files: files, Roots: roots})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(rebalancePlan.Moves) != 1 || rebalancePlan.Moves[0].AffinityKey != "fits-fine" {
		t.Fatalf("rebalance plan = %+v, want the group that fits to still move", rebalancePlan)
	}
}

func TestPlan_ErrorsWhenOneAffinityGroupDisagreesOnDestination(t *testing.T) {
	req := mover.Request{
		Reason: mover.ReasonRebalance,
		Files: []mover.FileToMove{
			{MediaObjectID: 1, SourceRootFolderID: 1, SizeBytes: 100, AffinityKey: "artist:1", DestRootFolderID: 2},
			{MediaObjectID: 2, SourceRootFolderID: 1, SizeBytes: 100, AffinityKey: "artist:1", DestRootFolderID: 3},
		},
		Roots: map[int64]placement.RootCandidate{
			1: onlineWritable(1, "fs-a", 1000), 2: onlineWritable(2, "fs-b", 1000), 3: onlineWritable(3, "fs-c", 1000),
		},
	}
	if _, err := mover.Plan(req); err == nil {
		t.Fatal("Plan succeeded despite one affinity group naming two different destinations")
	}
}

// The preview: bytes in, bytes out, and the resulting free space per root
// touched, across two moves in opposite directions between the same roots.
func TestPlan_ComputesRootDeltas(t *testing.T) {
	req := mover.Request{
		Reason: mover.ReasonRebalance,
		Files: []mover.FileToMove{
			{MediaObjectID: 1, SourceRootFolderID: 1, SizeBytes: 300, AffinityKey: "a", DestRootFolderID: 2},
			{MediaObjectID: 2, SourceRootFolderID: 2, SizeBytes: 100, AffinityKey: "b", DestRootFolderID: 1},
		},
		Roots: map[int64]placement.RootCandidate{
			1: onlineWritable(1, "fs-a", 1000),
			2: onlineWritable(2, "fs-b", 1000),
		},
	}
	plan, err := mover.Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Moves) != 2 {
		t.Fatalf("got %d moves, want 2", len(plan.Moves))
	}
	deltaFor := func(id int64) mover.RootDelta {
		for _, d := range plan.RootDeltas {
			if d.RootFolderID == id {
				return d
			}
		}
		t.Fatalf("no delta for root %d in %+v", id, plan.RootDeltas)
		return mover.RootDelta{}
	}
	// Bytes leaving a root free up space there; bytes arriving consume it.
	root1 := deltaFor(1)
	if root1.BytesOut != 300 || root1.BytesIn != 100 || root1.ResultingFreeBytes != 1200 {
		t.Errorf("root 1 delta = %+v, want out=300 in=100 free=1000+300-100=1200", root1)
	}
	root2 := deltaFor(2)
	if root2.BytesIn != 300 || root2.BytesOut != 100 || root2.ResultingFreeBytes != 800 {
		t.Errorf("root 2 delta = %+v, want in=300 out=100 free=1000+100-300=800", root2)
	}
}

func TestPlan_RefusesAnUnknownDestinationRoot(t *testing.T) {
	req := mover.Request{
		Reason: mover.ReasonRename,
		Files: []mover.FileToMove{
			{MediaObjectID: 1, SourceRootFolderID: 1, SizeBytes: 100, AffinityKey: "a", DestRootFolderID: 99},
		},
		Roots: map[int64]placement.RootCandidate{1: onlineWritable(1, "fs-a", 1000)},
	}
	plan, err := mover.Plan(req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Moves) != 0 || len(plan.Refused) != 1 {
		t.Fatalf("plan = %+v, want refused: destination root not in Roots", plan)
	}
}
