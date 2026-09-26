package library_test

import (
	"context"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

// refineWithReleaseMBID mirrors createPlaceholderEdition (enrich_test.go) but
// links the edition external id under music.ProviderRelease's real string,
// "musicbrainz:release" -- ListWorksNeedingArtwork filters on this exact value,
// not the bare "musicbrainz" its own doc comment first (wrongly) assumed.
func refineWithReleaseMBID(t *testing.T, identity library.Identity, releaseGroupID, releaseMBID string) int64 {
	t.Helper()
	_, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "musicbrainz:release-group", ExternalID: releaseGroupID, Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"},
		Edition: &library.EditionLink{
			Provider: "musicbrainz:release", ExternalID: releaseMBID, Confidence: 1.0,
			Edition: library.EditionIdentity{Kind: "unknown", Label: releaseMBID},
		},
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	found, err := identity.PlaceholderEditions(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("PlaceholderEditions: %v", err)
	}
	for _, edition := range found {
		if edition.ExternalID != releaseMBID {
			continue
		}
		if err := identity.RefineEdition(t.Context(), firstSeen.Unix(), edition.EditionID, edition.WorkID,
			nil, "", "", library.EditionRefinement{Kind: "official"}, nil); err != nil {
			t.Fatalf("RefineEdition: %v", err)
		}
		return edition.WorkID
	}
	t.Fatalf("no placeholder edition found for %s", releaseMBID)
	return 0
}

func TestNeedingArtwork_ListsARefinedEditionsReleaseMBID(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	artworks := library.NewArtworks(opened)

	workID := refineWithReleaseMBID(t, identity, "rg-artwork-1", "release-artwork-1")

	found, err := artworks.NeedingArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("NeedingArtwork: %v", err)
	}
	if len(found) != 1 || found[0].WorkID != workID || found[0].MBID != "release-artwork-1" {
		t.Fatalf("NeedingArtwork = %+v, want exactly {workID=%d, release-artwork-1}", found, workID)
	}
}

func TestNeedingArtwork_ExcludesAPlaceholderEdition(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	artworks := library.NewArtworks(opened)

	_, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "musicbrainz:release-group", ExternalID: "rg-artwork-2", Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "Zor A", SortTitle: "zor a"},
		Edition: &library.EditionLink{
			Provider: "musicbrainz:release", ExternalID: "release-artwork-2", Confidence: 1.0,
			Edition: library.EditionIdentity{Kind: "unknown", Label: "release-artwork-2"},
		},
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	// Still kind='unknown' -- never refined -- so it must not be a candidate yet.
	found, err := artworks.NeedingArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("NeedingArtwork: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("NeedingArtwork = %+v, want none: the only edition is still a placeholder", found)
	}
}

func TestGetForWork_NotFoundWhenNoCoverIsRecorded(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	artworks := library.NewArtworks(opened)
	workID := refineWithReleaseMBID(t, identity, "rg-artwork-4", "release-artwork-4")

	got, err := artworks.GetForWork(t.Context(), workID)
	if err != nil {
		t.Fatalf("GetForWork: %v", err)
	}
	if got.Found {
		t.Errorf("GetForWork = %+v, want Found=false before ArtworkJob has recorded anything", got)
	}
}

func TestGetForWork_AnswersTheRecordedCacheKey(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	artworks := library.NewArtworks(opened)
	workID := refineWithReleaseMBID(t, identity, "rg-artwork-5", "release-artwork-5")

	if err := artworks.Record(t.Context(), workID, "https://example.test/cover.jpg",
		"work-artwork-5-cover", 1000, 1000, firstSeen.Unix()); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := artworks.GetForWork(t.Context(), workID)
	if err != nil {
		t.Fatalf("GetForWork: %v", err)
	}
	if !got.Found || got.CacheKey != "work-artwork-5-cover" {
		t.Errorf("GetForWork = %+v, want the recorded cache key", got)
	}
}

func TestRecord_RemovesTheWorkFromNeedingArtwork(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	artworks := library.NewArtworks(opened)
	workID := refineWithReleaseMBID(t, identity, "rg-artwork-3", "release-artwork-3")

	if err := artworks.Record(t.Context(), workID, "https://example.test/cover.jpg",
		"work-artwork-3-cover", 1000, 1000, firstSeen.Unix()); err != nil {
		t.Fatalf("Record: %v", err)
	}

	found, err := artworks.NeedingArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("NeedingArtwork: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("NeedingArtwork after Record = %+v, want none left, the same no-cursor shape PlaceholderEditions uses", found)
	}
}

func TestPeopleNeedingArtwork_ListsACreditedArtistWithNoImageYet(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	artworks := library.NewArtworks(opened)

	personID := resolvePersonNamed(t, people, "artist-mbid-1", "Heka", "Heka")

	found, err := artworks.PeopleNeedingArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("PeopleNeedingArtwork: %v", err)
	}
	if len(found) != 1 || found[0].PersonID != personID || found[0].ArtistMBID != "artist-mbid-1" {
		t.Fatalf("PeopleNeedingArtwork = %+v, want exactly {personID=%d, artist-mbid-1}", found, personID)
	}
}

func TestPeopleNeedingArtwork_ExcludesAPersonWhoseImageWasFetched(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	artworks := library.NewArtworks(opened)

	personID := resolvePersonNamed(t, people, "artist-mbid-2", "Heka", "Heka")
	if err := artworks.RecordForPerson(
		t.Context(), personID, "https://example.invalid/heka.jpg", "person-1-cover", 500, 500, 1,
	); err != nil {
		t.Fatalf("RecordForPerson: %v", err)
	}

	found, err := artworks.PeopleNeedingArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("PeopleNeedingArtwork: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("PeopleNeedingArtwork = %+v, want none once an image is recorded", found)
	}
}

// The starvation guard: an artist with no Wikidata image must drop out of the
// sweep, or the same lowest-id people come back every run and the rest are
// never reached.
func TestPeopleNeedingArtwork_ExcludesAPersonRecordedAsHavingNoImage(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	artworks := library.NewArtworks(opened)

	personID := resolvePersonNamed(t, people, "artist-mbid-3", "Obscure Act", "Obscure Act")
	if err := artworks.RecordPersonArtworkAbsent(t.Context(), personID, 1); err != nil {
		t.Fatalf("RecordPersonArtworkAbsent: %v", err)
	}

	found, err := artworks.PeopleNeedingArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("PeopleNeedingArtwork: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("PeopleNeedingArtwork = %+v, want none once absence is recorded", found)
	}
}

// The absence marker must not become a servable image: an empty cache_key
// names no file, so GetForPerson has to answer "nothing here", not a row.
func TestGetForPerson_TreatsARecordedAbsenceAsNoImage(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	artworks := library.NewArtworks(opened)

	personID := resolvePersonNamed(t, people, "artist-mbid-4", "Obscure Act", "Obscure Act")
	if err := artworks.RecordPersonArtworkAbsent(t.Context(), personID, 1); err != nil {
		t.Fatalf("RecordPersonArtworkAbsent: %v", err)
	}

	got, err := artworks.GetForPerson(t.Context(), personID)
	if err != nil {
		t.Fatalf("GetForPerson: %v", err)
	}
	if got != (library.PersonArtwork{}) {
		t.Fatalf("GetForPerson = %+v, want the zero value for a recorded absence", got)
	}
}

func TestGetForPerson_AnswersTheRecordedCacheKey(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	artworks := library.NewArtworks(opened)

	personID := resolvePersonNamed(t, people, "artist-mbid-5", "Heka", "Heka")
	if err := artworks.RecordForPerson(
		t.Context(), personID, "https://example.invalid/heka.jpg", "person-9-cover", 500, 500, 1,
	); err != nil {
		t.Fatalf("RecordForPerson: %v", err)
	}

	got, err := artworks.GetForPerson(t.Context(), personID)
	if err != nil {
		t.Fatalf("GetForPerson: %v", err)
	}
	if got != (library.PersonArtwork{CacheKey: "person-9-cover", Found: true}) {
		t.Fatalf("GetForPerson = %+v, want the recorded key", got)
	}
}

// The sweep walks by id within a run, so people it could not finish do not
// come back at the top of every page and hide everyone after them.
func TestPeopleNeedingArtwork_StartsAfterTheGivenPerson(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	artworks := library.NewArtworks(opened)

	first := resolvePersonNamed(t, people, "artist-mbid-a", "First", "First")
	second := resolvePersonNamed(t, people, "artist-mbid-b", "Second", "Second")

	found, err := artworks.PeopleNeedingArtwork(t.Context(), first, 10)
	if err != nil {
		t.Fatalf("PeopleNeedingArtwork: %v", err)
	}
	if len(found) != 1 || found[0].PersonID != second {
		t.Fatalf("PeopleNeedingArtwork after %d = %+v, want only person %d", first, found, second)
	}
}

// A failed read must surface, not pass for a person with no image -- the
// absence marker check once ran before the error check and hid it.
func TestGetForPerson_ReportsAReadFailure(t *testing.T) {
	opened, _ := newLibrary(t)
	artworks := library.NewArtworks(opened)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := artworks.GetForPerson(cancelled, 1); err == nil {
		t.Fatal("GetForPerson with a cancelled context answered no error")
	}
}

// A wanted album with no files has no root folder, so the category wanting it
// is the only thing that can make its cover visible.
func TestGetForWork_ReportsTheCategoriesWantingTheWork(t *testing.T) {
	opened, _ := newLibrary(t)
	artworks := library.NewArtworks(opened)
	work := createWork(t, opened, "music", "Tender Prey")
	if _, err := library.NewDesired(opened).EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: work.ID, CategoryID: 7, ProfileID: 1, Monitored: true,
	}); err != nil {
		t.Fatalf("EnsureAlbumItem: %v", err)
	}

	got, err := artworks.GetForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("GetForWork: %v", err)
	}
	if len(got.RootFolderIDs) != 0 || len(got.CategoryIDs) != 1 || got.CategoryIDs[0] != 7 {
		t.Fatalf("GetForWork = %+v, want no root folders and category 7", got)
	}
}

// registerFileForArtwork puts one file on disk, as far as library.db knows.
func registerFileForArtwork(t *testing.T, physical library.Physical, rootFolderID int64, relPath, quickHash string) int64 {
	t.Helper()
	registered, err := physical.RegisterBatch(t.Context(), rootFolderID, []library.PhysicalFile{
		{RelPath: relPath, SizeBytes: 1, QuickHash: quickHash, FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	return registered[0].MediaObjectID
}

func TestNeedingEmbeddedArtwork_NamesTheFileTheWorkWasResolvedFrom(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	artworks := library.NewArtworks(opened)
	mediaObjectID := registerFileForArtwork(t, library.NewPhysical(opened), 3, "Heka/Detisisu Binip (1991)/02.m4a", "q1")

	workIDs, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "musicbrainz:release-group", ExternalID: "rg-embedded", Confidence: 1.0,
		Work:         library.WorkIdentity{MediaType: "music", CanonicalTitle: "Detisisu Binip", SortTitle: "detisisu binip"},
		RootFolderID: 3, MediaObjectID: mediaObjectID,
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	found, err := artworks.NeedingEmbeddedArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("NeedingEmbeddedArtwork: %v", err)
	}
	want := library.EmbeddedArtworkCandidate{WorkID: workIDs[0], RootFolderID: 3, RelPath: "Heka/Detisisu Binip (1991)/02.m4a"}
	if len(found) != 1 || found[0] != want {
		t.Fatalf("NeedingEmbeddedArtwork = %+v, want [%+v]", found, want)
	}
}

// The detailed resolution is import's path; it must record the file too.
func TestNeedingEmbeddedArtwork_CoversWorksResolvedByImport(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	artworks := library.NewArtworks(opened)
	mediaObjectID := registerFileForArtwork(t, library.NewPhysical(opened), 1, "P!po/Kebilegev Bibihi (2017)/02.m4a", "q2")

	if _, err := identity.ResolveWorksDetailed(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "musicbrainz:release-group", ExternalID: "rg-imported", Confidence: 1.0,
		Work:         library.WorkIdentity{MediaType: "music", CanonicalTitle: "Kebilegev Bibihi", SortTitle: "kebilegev bibihi"},
		RootFolderID: 1, MediaObjectID: mediaObjectID,
	}}); err != nil {
		t.Fatalf("ResolveWorksDetailed: %v", err)
	}

	found, err := artworks.NeedingEmbeddedArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("NeedingEmbeddedArtwork: %v", err)
	}
	if len(found) != 1 || found[0].RelPath != "P!po/Kebilegev Bibihi (2017)/02.m4a" {
		t.Fatalf("NeedingEmbeddedArtwork = %+v, want the imported file", found)
	}
}

func TestNeedingEmbeddedArtwork_ExcludesAWorkWithACover(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	artworks := library.NewArtworks(opened)
	mediaObjectID := registerFileForArtwork(t, library.NewPhysical(opened), 1, "a/1.m4a", "q3")

	workIDs, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "musicbrainz:release-group", ExternalID: "rg-covered", Confidence: 1.0,
		Work:         library.WorkIdentity{MediaType: "music", CanonicalTitle: "Covered", SortTitle: "covered"},
		RootFolderID: 1, MediaObjectID: mediaObjectID,
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	if err := artworks.Record(t.Context(), workIDs[0], "", "work-1-cover", 500, 500, 1); err != nil {
		t.Fatalf("Record: %v", err)
	}

	found, err := artworks.NeedingEmbeddedArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("NeedingEmbeddedArtwork: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("NeedingEmbeddedArtwork = %+v, want none once a cover exists", found)
	}
}

func TestWorkFileLocations_AnswersTheRecordedFilesPathsForTheNamedWorks(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	physical := library.NewPhysical(opened)

	resolve := func(releaseGroup, relPath, quickHash string) int64 {
		t.Helper()
		mediaObjectID := registerFileForArtwork(t, physical, 1, relPath, quickHash)
		workIDs, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
			Provider: "musicbrainz:release-group", ExternalID: releaseGroup, Confidence: 1.0,
			Work:         library.WorkIdentity{MediaType: "music", CanonicalTitle: releaseGroup, SortTitle: releaseGroup},
			RootFolderID: 1, MediaObjectID: mediaObjectID,
		}})
		if err != nil {
			t.Fatalf("ResolveWorks: %v", err)
		}
		return workIDs[0]
	}
	heka := resolve("rg-heka", "Heka/Detisisu Binip (1991)/01.m4a", "q-heka")
	resolve("rg-pink", "P!po/Trustfall (2023)/01.m4a", "q-pink")

	got, err := identity.WorkFileLocations(t.Context(), []int64{heka, 9999})
	if err != nil {
		t.Fatalf("WorkFileLocations: %v", err)
	}
	want := []library.WorkFileLocation{{WorkID: heka, RootFolderID: 1, RelPath: "Heka/Detisisu Binip (1991)/01.m4a"}}
	if !slices.Equal(got, want) {
		t.Errorf("WorkFileLocations = %+v, want %+v", got, want)
	}
}

// The test library holds Kebilegev Bibihi in two folders. Every file must be
// remembered, or a rescan reaches only the first folder and the other folder's
// tracks never match (found 2026-09-17).
func TestWorkFileLocations_AnswersEveryFolderAWorkWasResolvedFrom(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	physical := library.NewPhysical(opened)

	first := registerFileForArtwork(t, physical, 1, "P!po/Kebilegev Bibihi (1)/01.m4a", "q-first")
	second := registerFileForArtwork(t, physical, 1, "P!po/Kebilegev Bibihi (2017)/02.m4a", "q-second")
	link := func(mediaObjectID int64) library.WorkLink {
		return library.WorkLink{
			Provider: "musicbrainz:release-group", ExternalID: "rg-kebilegev-bibihi", Confidence: 1.0,
			Work:         library.WorkIdentity{MediaType: "music", CanonicalTitle: "Kebilegev Bibihi", SortTitle: "kebilegev bibihi"},
			RootFolderID: 1, MediaObjectID: mediaObjectID,
		}
	}
	workIDs, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{link(first), link(second)})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	got, err := identity.WorkFileLocations(t.Context(), []int64{workIDs[0]})
	if err != nil {
		t.Fatalf("WorkFileLocations: %v", err)
	}
	want := []library.WorkFileLocation{
		{WorkID: workIDs[0], RootFolderID: 1, RelPath: "P!po/Kebilegev Bibihi (1)/01.m4a"},
		{WorkID: workIDs[0], RootFolderID: 1, RelPath: "P!po/Kebilegev Bibihi (2017)/02.m4a"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("WorkFileLocations = %+v, want both folders %+v", got, want)
	}

	candidates, err := library.NewArtworks(opened).NeedingEmbeddedArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("NeedingEmbeddedArtwork: %v", err)
	}
	if len(candidates) != 1 || candidates[0].RelPath != "P!po/Kebilegev Bibihi (1)/01.m4a" {
		t.Errorf("NeedingEmbeddedArtwork = %+v, want one candidate, the work's first file", candidates)
	}
}
