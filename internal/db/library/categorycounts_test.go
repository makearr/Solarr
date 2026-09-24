package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
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
	if len(corrections) != 10 {
		t.Fatalf("corrections = %+v, want 10 (works+files+files_offline+parts+artists for each of 2 categories)", corrections)
	}

	if v, ok := correctionFor(corrections, "works"); !ok || v.Now != 2 || v.Scope != "category:1" {
		t.Errorf("category:1 works = %+v, ok=%v, want Now=2", v, ok)
	}
}

// TestCategoryCounters_FilesOfflineSumsOnlyTheOfflineSubset is todo 4.18's own
// remaining item: "files" stays the category's true total regardless of
// reachability, "files_offline" is how many of those sit on a root the
// caller named as offline -- a category spanning one online and one offline
// root must report both figures separately, not conflate them.
func TestCategoryCounters_FilesOfflineSumsOnlyTheOfflineSubset(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	counters := library.NewCategoryCounters(opened, clock.NewFakeAtEpoch())

	if _, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "a/1.flac", SizeBytes: 1, QuickHash: "h1", FirstSeenAt: firstSeen},
		{RelPath: "a/2.flac", SizeBytes: 1, QuickHash: "h2", FirstSeenAt: firstSeen},
	}); err != nil {
		t.Fatalf("RegisterBatch root 1 (online): %v", err)
	}
	if _, err := physical.RegisterBatch(t.Context(), 2, []library.PhysicalFile{
		{RelPath: "b/1.flac", SizeBytes: 1, QuickHash: "h3", FirstSeenAt: firstSeen},
	}); err != nil {
		t.Fatalf("RegisterBatch root 2 (offline): %v", err)
	}

	corrections, err := counters.Reconcile(t.Context(), []library.CategoryScope{
		{CategoryID: 1, RootFolderIDs: []int64{1, 2}, OfflineRootFolderIDs: []int64{2}},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if v, ok := correctionFor(corrections, "files"); !ok || v.Now != 3 {
		t.Errorf("files = %+v, ok=%v, want Now=3 (both roots, online and offline)", v, ok)
	}
	if v, ok := correctionFor(corrections, "files_offline"); !ok || v.Now != 1 {
		t.Errorf("files_offline = %+v, ok=%v, want Now=1 (only root 2's own file)", v, ok)
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

// ADR-54's navigation levels need per-category counts for work_part-entity
// levels (music's Tracks) and person-entity levels (music's Artists), not just
// "works"/"files" -- this is the acceptance case for both.
func TestCategoryCounters_CountsPartsAndArtistCredits(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	people := library.NewPeople(opened)
	counters := library.NewCategoryCounters(opened, clock.NewFakeAtEpoch())

	workIDs, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "test", ExternalID: "a", Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "A", SortTitle: "a"}, RootFolderID: 1,
		Edition: &library.EditionLink{
			Provider: "test", ExternalID: "edition-a", Confidence: 1,
			Edition: library.EditionIdentity{Kind: "unknown", Label: "edition-a"},
		},
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	workID := workIDs[0]

	placeholders, err := identity.PlaceholderEditions(t.Context(), 10)
	if err != nil || len(placeholders) != 1 {
		t.Fatalf("PlaceholderEditions: %v, %+v", err, placeholders)
	}
	if err := identity.RefineEdition(t.Context(), firstSeen.Unix(), placeholders[0].EditionID, workID, nil, "", "",
		library.EditionRefinement{Kind: "album"}, []library.PartIdentity{
			{MediaRef: "disc1:track1", SortKey: "0001"},
			{MediaRef: "disc1:track2", SortKey: "0002"},
			{MediaRef: "disc1:track3", SortKey: "0003"},
		}); err != nil {
		t.Fatalf("RefineEdition: %v", err)
	}

	personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "test", ExternalID: "artist-a",
		Person: library.PersonIdentity{Name: "Artist A", SortName: "Artist A"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddCredit(t.Context(), workID, personID, "artist", 0); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}

	corrections, err := counters.Reconcile(t.Context(), []library.CategoryScope{
		{CategoryID: 1, RootFolderIDs: []int64{1}},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if v, ok := correctionFor(corrections, "parts"); !ok || v.Now != 3 {
		t.Errorf("parts = %+v, ok=%v, want Now=3", v, ok)
	}
	if v, ok := correctionFor(corrections, "artists"); !ok || v.Now != 1 {
		t.Errorf("artists = %+v, ok=%v, want Now=1", v, ok)
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

// An artist just added counts in their library before enrichment credits any work to them
// (answered 2026-09-24), and appears on its Artists level -- not in another library's.
func TestArtists_AMonitoredArtistBelongsToTheirLibraryAtOnce(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "heka",
		Person: library.PersonIdentity{Name: "Heka", SortName: "Heka"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := people.Subscribe(t.Context(), firstSeen, personID, 1, library.SubscriptionRequest{
		MediaTypes: []string{"music"}, CategoryID: 1,
	}); err != nil {
		t.Fatal(err)
	}

	corrections, err := library.NewCategoryCounters(opened, clock.NewFakeAtEpoch()).Reconcile(t.Context(),
		[]library.CategoryScope{{CategoryID: 1}, {CategoryID: 2}})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, c := range corrections {
		if c.Metric == "artists" {
			counts[c.Scope] = c.Now
		}
	}
	if counts["category:1"] != 1 || counts["category:2"] != 0 {
		t.Errorf("artist counts = %v, want 1 in library 1 and 0 in library 2", counts)
	}

	for categoryID, want := range map[int64]int{1: 1, 2: 0} {
		page, err := people.ListPeople(t.Context(),
			library.PersonFilter{Role: "artist", CategoryIDs: []int64{categoryID}}, library.PersonCursor{}, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != want {
			t.Errorf("library %d lists %d artists, want %d", categoryID, len(page), want)
		}
	}
}
