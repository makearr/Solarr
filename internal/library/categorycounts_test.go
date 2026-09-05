package library_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/library"
)

func TestCategoryCounters_CountsWorksAndFilesForTheScopedRoots(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	physical := library.NewPhysical(opened)
	counters := library.NewCategoryCounters(opened, clock.NewFakeAtEpoch())

	// Two works under root 1 (category 1's), one under root 2 (category 2's).
	workA, workB, workOther := library.WorkLink{
		Provider: "test", ExternalID: "a", Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "A", SortTitle: "a"}, RootFolderID: 1,
	}, library.WorkLink{
		Provider: "test", ExternalID: "b", Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "B", SortTitle: "b"}, RootFolderID: 1,
	}, library.WorkLink{
		Provider: "test", ExternalID: "c", Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "C", SortTitle: "c"}, RootFolderID: 2,
	}
	if _, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{workA, workB, workOther}); err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	// Three files under root 1, one under root 2.
	if _, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "a/1.flac", SizeBytes: 1, QuickHash: "h1", FirstSeenAt: firstSeen},
		{RelPath: "a/2.flac", SizeBytes: 1, QuickHash: "h2", FirstSeenAt: firstSeen},
		{RelPath: "b/1.flac", SizeBytes: 1, QuickHash: "h3", FirstSeenAt: firstSeen},
	}); err != nil {
		t.Fatalf("RegisterBatch root 1: %v", err)
	}
	if _, err := physical.RegisterBatch(t.Context(), 2, []library.PhysicalFile{
		{RelPath: "c/1.flac", SizeBytes: 1, QuickHash: "h4", FirstSeenAt: firstSeen},
	}); err != nil {
		t.Fatalf("RegisterBatch root 2: %v", err)
	}

	corrections, err := counters.Reconcile(t.Context(), []library.CategoryScope{
		{CategoryID: 1, RootFolderIDs: []int64{1}},
		{CategoryID: 2, RootFolderIDs: []int64{2}},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(corrections) != 4 {
		t.Fatalf("corrections = %+v, want 4 (works+files for each of 2 categories)", corrections)
	}

	if v, ok := correctionFor(corrections, "works"); !ok || v.Now != 2 || v.Scope != "category:1" {
		t.Errorf("category:1 works = %+v, ok=%v, want Now=2", v, ok)
	}
}

// A category spanning several root folders sums across all of them.
func TestCategoryCounters_ACategorySpanningSeveralRootsSumsThem(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	counters := library.NewCategoryCounters(opened, clock.NewFakeAtEpoch())

	links := []library.WorkLink{
		{Provider: "test", ExternalID: "a", Confidence: 1,
			Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "A", SortTitle: "a"}, RootFolderID: 1},
		{Provider: "test", ExternalID: "b", Confidence: 1,
			Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "B", SortTitle: "b"}, RootFolderID: 2},
	}
	if _, err := identity.ResolveWorks(t.Context(), firstSeen, links); err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	corrections, err := counters.Reconcile(t.Context(), []library.CategoryScope{
		{CategoryID: 1, RootFolderIDs: []int64{1, 2}},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if v, ok := correctionFor(corrections, "works"); !ok || v.Now != 2 {
		t.Errorf("works = %+v, ok=%v, want Now=2 (one from each root)", v, ok)
	}
}

// Reconciling twice with nothing changed must report no corrections the second
// time -- otherwise every run looks like drift even when there was none.
func TestCategoryCounters_UnchangedCountsProduceNoCorrections(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	counters := library.NewCategoryCounters(opened, clock.NewFakeAtEpoch())

	if _, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "test", ExternalID: "a", Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "A", SortTitle: "a"}, RootFolderID: 1,
	}}); err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	scopes := []library.CategoryScope{{CategoryID: 1, RootFolderIDs: []int64{1}}}
	if _, err := counters.Reconcile(t.Context(), scopes); err != nil {
		t.Fatalf("Reconcile (first): %v", err)
	}
	corrections, err := counters.Reconcile(t.Context(), scopes)
	if err != nil {
		t.Fatalf("Reconcile (second): %v", err)
	}
	if len(corrections) != 0 {
		t.Errorf("corrections = %+v, want none: nothing changed between the two runs", corrections)
	}
}

// The confirmed bug private/next.md named: a subscription-discovered work has a
// desired_item.category_id but no work_root_folder row, so a naive per-root-folder
// count undercounts a category's sidebar "works" total the moment a subscription
// creates one. The union must also not double-count a work that has rows on both
// sides (scanned AND has a desired_item in the same category).
func TestCategoryCounters_CountsSubscriptionDiscoveredWorksWithNoRootFolder(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	desired := library.NewDesired(opened)
	counters := library.NewCategoryCounters(opened, clock.NewFakeAtEpoch())

	scannedAndDesired, subscriptionOnly := library.WorkLink{
		Provider: "test", ExternalID: "a", Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "A", SortTitle: "a"}, RootFolderID: 1,
	}, library.WorkLink{
		Provider: "test", ExternalID: "b", Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "B", SortTitle: "b"},
	}
	workIDs, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{scannedAndDesired, subscriptionOnly})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	for _, workID := range workIDs {
		if _, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
			WorkID: workID, CategoryID: 1, ProfileID: 1, Monitored: true,
		}); err != nil {
			t.Fatalf("EnsureAlbumItem: %v", err)
		}
	}

	corrections, err := counters.Reconcile(t.Context(), []library.CategoryScope{
		{CategoryID: 1, RootFolderIDs: []int64{1}},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if v, ok := correctionFor(corrections, "works"); !ok || v.Now != 2 {
		t.Errorf("works = %+v, ok=%v, want Now=2 (one on both sides, one desired_item-only)", v, ok)
	}
}

// A category with no root folders (none configured yet, or all detached) must
// reconcile to zero rather than erroring or being skipped.
func TestCategoryCounters_ACategoryWithNoRootFoldersReconcilesToZero(t *testing.T) {
	opened, _ := newLibrary(t)
	counters := library.NewCategoryCounters(opened, clock.NewFakeAtEpoch())

	corrections, err := counters.Reconcile(t.Context(), []library.CategoryScope{
		{CategoryID: 1, RootFolderIDs: nil},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if v, ok := correctionFor(corrections, "works"); !ok || v.Now != 0 {
		t.Errorf("works = %+v, ok=%v, want Now=0", v, ok)
	}
}
