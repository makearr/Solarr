package placement_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/moving/placement"
)

func baseCandidate(id int64) placement.RootCandidate {
	return placement.RootCandidate{
		RootFolderID: id, Online: true, Writable: true, AcceptsNew: true,
		FreeBytes: 100_000, TotalBytes: 200_000, MinFreeBytes: 1_000,
	}
}

func TestResolve_MostFreeSpacePicksTheLargestCandidate(t *testing.T) {
	small := baseCandidate(1)
	small.FreeBytes = 10_000
	large := baseCandidate(2)
	large.FreeBytes = 90_000

	chosen, decision, err := placement.Resolve(placement.Request{
		SizeBytes: 100, Strategy: placement.StrategyMostFreeSpace,
	}, []placement.RootCandidate{small, large})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decision.Outcome != placement.OutcomePlaced {
		t.Fatalf("Outcome = %v, want PLACED", decision.Outcome)
	}
	if chosen.RootFolderID != 2 {
		t.Errorf("chosen = %d, want 2 (the larger free space)", chosen.RootFolderID)
	}
}

func TestResolve_MostFreePercentPrefersProportionNotAbsoluteSize(t *testing.T) {
	bigButFullish := placement.RootCandidate{
		RootFolderID: 1, Online: true, Writable: true, AcceptsNew: true,
		FreeBytes: 1_000_000, TotalBytes: 20_000_000, MinFreeBytes: 0, // 5% free
	}
	smallButRoomy := placement.RootCandidate{
		RootFolderID: 2, Online: true, Writable: true, AcceptsNew: true,
		FreeBytes: 900_000, TotalBytes: 1_000_000, MinFreeBytes: 0, // 90% free
	}

	chosen, _, err := placement.Resolve(placement.Request{
		SizeBytes: 100, Strategy: placement.StrategyMostFreePercent,
	}, []placement.RootCandidate{bigButFullish, smallButRoomy})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if chosen.RootFolderID != 2 {
		t.Errorf("chosen = %d, want 2 (the higher free percentage, despite less absolute free space)", chosen.RootFolderID)
	}
}

func TestResolve_FillOrderPicksLowestOrdWithRoom(t *testing.T) {
	first := baseCandidate(1)
	first.Ord = 0
	second := baseCandidate(2)
	second.Ord = 1

	chosen, _, err := placement.Resolve(placement.Request{
		SizeBytes: 100, Strategy: placement.StrategyFillOrder,
	}, []placement.RootCandidate{second, first})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if chosen.RootFolderID != 1 {
		t.Errorf("chosen = %d, want 1 (lowest ord)", chosen.RootFolderID)
	}
}

func TestResolve_RoundRobinPicksFewestPlacementsSoFar(t *testing.T) {
	busy := baseCandidate(1)
	busy.PlacementCount = 40
	idle := baseCandidate(2)
	idle.PlacementCount = 2

	chosen, _, err := placement.Resolve(placement.Request{
		SizeBytes: 100, Strategy: placement.StrategyRoundRobin,
	}, []placement.RootCandidate{busy, idle})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if chosen.RootFolderID != 2 {
		t.Errorf("chosen = %d, want 2 (fewest placements so far)", chosen.RootFolderID)
	}
}

func TestResolve_WeightedIsProportionalToWeight(t *testing.T) {
	light := baseCandidate(1)
	light.Weight = 1
	heavy := baseCandidate(2)
	heavy.Weight = 4
	candidates := []placement.RootCandidate{light, heavy}

	// weight 1 vs weight 4 out of a total of 5: a random draw of 0.1 (i.e.
	// 0.5 out of 5) falls inside light's [0,1) slice.
	chosen, _, err := placement.Resolve(placement.Request{
		SizeBytes: 100, Strategy: placement.StrategyWeighted, Random: func() float64 { return 0.1 },
	}, candidates)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if chosen.RootFolderID != 1 {
		t.Errorf("chosen = %d, want 1 (draw 0.1*5=0.5 falls in weight-1's [0,1) slice)", chosen.RootFolderID)
	}

	// A draw of 0.5 (2.5 out of 5) falls past light's slice, into heavy's.
	chosen, _, err = placement.Resolve(placement.Request{
		SizeBytes: 100, Strategy: placement.StrategyWeighted, Random: func() float64 { return 0.5 },
	}, candidates)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if chosen.RootFolderID != 2 {
		t.Errorf("chosen = %d, want 2 (draw 0.5*5=2.5 falls in weight-4's [1,5) slice)", chosen.RootFolderID)
	}
}

func TestResolve_ManualUsesTheUsersOwnChoice(t *testing.T) {
	first := baseCandidate(1)
	second := baseCandidate(2)
	second.FreeBytes = 999_999 // would win most_free_space, but manual overrides it
	chosenID := int64(1)

	chosen, _, err := placement.Resolve(placement.Request{
		SizeBytes: 100, Strategy: placement.StrategyManual, Manual: &chosenID,
	}, []placement.RootCandidate{first, second})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if chosen.RootFolderID != 1 {
		t.Errorf("chosen = %d, want 1 (the user's own manual pick)", chosen.RootFolderID)
	}
}

func TestResolve_ManualWithNoChoiceFallsBackToMostFreeSpace(t *testing.T) {
	small := baseCandidate(1)
	small.FreeBytes = 10_000
	large := baseCandidate(2)
	large.FreeBytes = 90_000

	chosen, _, err := placement.Resolve(placement.Request{
		SizeBytes: 100, Strategy: placement.StrategyManual, Manual: nil,
	}, []placement.RootCandidate{small, large})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if chosen.RootFolderID != 2 {
		t.Errorf("chosen = %d, want 2 (automatic addition falls back to most_free_space)", chosen.RootFolderID)
	}
}

func TestResolve_AffinityWinsOverStrategyWhenItQualifies(t *testing.T) {
	affinityRoot := baseCandidate(1)
	affinityRoot.FreeBytes = 10_000 // would lose most_free_space on its own
	other := baseCandidate(2)
	other.FreeBytes = 999_999

	affinityRootID := int64(1)
	chosen, decision, err := placement.Resolve(placement.Request{
		SizeBytes: 100, Strategy: placement.StrategyMostFreeSpace,
		AffinityEnabled: true, AffinityKey: "artist:42", ExistingAffinityRoot: &affinityRootID,
	}, []placement.RootCandidate{affinityRoot, other})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !decision.AffinityUsed {
		t.Error("AffinityUsed = false, want true")
	}
	if chosen.RootFolderID != 1 {
		t.Errorf("chosen = %d, want 1 (affinity wins even though it would lose the strategy)", chosen.RootFolderID)
	}
}

func TestResolve_AffinityIsIgnoredWhenTheRootNoLongerQualifies(t *testing.T) {
	affinityRoot := baseCandidate(1)
	affinityRoot.AcceptsNew = false // draining: affinity must not be forced onto it
	other := baseCandidate(2)

	affinityRootID := int64(1)
	chosen, decision, err := placement.Resolve(placement.Request{
		SizeBytes: 100, Strategy: placement.StrategyMostFreeSpace,
		AffinityEnabled: true, AffinityKey: "artist:42", ExistingAffinityRoot: &affinityRootID,
	}, []placement.RootCandidate{affinityRoot, other})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decision.AffinityUsed {
		t.Error("AffinityUsed = true, want false (the affinity root no longer qualifies)")
	}
	if chosen.RootFolderID != 2 {
		t.Errorf("chosen = %d, want 2 (fell through to the strategy)", chosen.RootFolderID)
	}
}

func TestResolve_TiebreakPrefersMatchingFilesystem(t *testing.T) {
	same := baseCandidate(1)
	same.FreeBytes = 50_000
	same.FSID = "zfs-pool-a"
	other := baseCandidate(2)
	other.FreeBytes = 50_000
	other.FSID = "zfs-pool-b"

	chosen, _, err := placement.Resolve(placement.Request{
		SizeBytes: 100, Strategy: placement.StrategyMostFreeSpace, SourceFSID: "zfs-pool-a",
	}, []placement.RootCandidate{other, same})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if chosen.RootFolderID != 1 {
		t.Errorf("chosen = %d, want 1 (matches the download's own filesystem)", chosen.RootFolderID)
	}
}

func TestResolve_DefersWithAStructuredReasonWhenNothingQualifies(t *testing.T) {
	offline := baseCandidate(1)
	offline.Online = false
	fullDisk := baseCandidate(2)
	fullDisk.FreeBytes = 500
	fullDisk.MinFreeBytes = 1_000_000
	draining := baseCandidate(3)
	draining.AcceptsNew = false

	_, decision, err := placement.Resolve(placement.Request{
		CategoryID: 7, SizeBytes: 100, Strategy: placement.StrategyMostFreeSpace,
	}, []placement.RootCandidate{offline, fullDisk, draining})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decision.Outcome != placement.OutcomeDefer {
		t.Fatalf("Outcome = %v, want DEFER", decision.Outcome)
	}
	if decision.Reason == "" {
		t.Error("Reason is empty, want a structured explanation")
	}
	if len(decision.Rejected) != 3 {
		t.Fatalf("len(Rejected) = %d, want 3 (every candidate named)", len(decision.Rejected))
	}
	byID := map[int64]string{}
	for _, rejected := range decision.Rejected {
		byID[rejected.RootFolderID] = rejected.Reason
	}
	if byID[1] != "offline" {
		t.Errorf("Rejected[1] = %q, want \"offline\"", byID[1])
	}
	if byID[3] != "draining (accepts_new = 0)" {
		t.Errorf("Rejected[3] = %q, want the draining reason", byID[3])
	}
}

func TestResolve_ReadOnlyRootIsNeverAPlacementTarget(t *testing.T) {
	readOnly := baseCandidate(1)
	readOnly.ReadOnly = true
	writable := baseCandidate(2)

	chosen, _, err := placement.Resolve(placement.Request{
		SizeBytes: 100, Strategy: placement.StrategyMostFreeSpace,
	}, []placement.RootCandidate{readOnly, writable})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if chosen.RootFolderID != 2 {
		t.Errorf("chosen = %d, want 2 (read_only=1 root must never be chosen)", chosen.RootFolderID)
	}
}

func TestResolve_MinFreeBytesReserveIsRespected(t *testing.T) {
	tight := baseCandidate(1)
	tight.FreeBytes = 1_000
	tight.MinFreeBytes = 950 // only 50 bytes of headroom after the reserve
	roomy := baseCandidate(2)
	roomy.FreeBytes = 1_000
	roomy.MinFreeBytes = 0

	chosen, _, err := placement.Resolve(placement.Request{
		SizeBytes: 100, Strategy: placement.StrategyMostFreeSpace, // both have equal FreeBytes
	}, []placement.RootCandidate{tight, roomy})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if chosen.RootFolderID != 2 {
		t.Errorf("chosen = %d, want 2 (the tight root's reserve leaves no room for a 100-byte item)", chosen.RootFolderID)
	}
}
