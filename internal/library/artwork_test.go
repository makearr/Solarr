package library_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/library"
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
	found, err := identity.PlaceholderEditions(t.Context(), 10)
	if err != nil {
		t.Fatalf("PlaceholderEditions: %v", err)
	}
	for _, edition := range found {
		if edition.ExternalID != releaseMBID {
			continue
		}
		if err := identity.RefineEdition(t.Context(), firstSeen.Unix(), edition.EditionID, edition.WorkID,
			nil, library.EditionRefinement{Kind: "official"}, nil); err != nil {
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

	found, err := artworks.NeedingArtwork(t.Context(), 10)
	if err != nil {
		t.Fatalf("NeedingArtwork: %v", err)
	}
	if len(found) != 1 || found[0].WorkID != workID || found[0].ReleaseMBID != "release-artwork-1" {
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
	found, err := artworks.NeedingArtwork(t.Context(), 10)
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

	found, err := artworks.NeedingArtwork(t.Context(), 10)
	if err != nil {
		t.Fatalf("NeedingArtwork: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("NeedingArtwork after Record = %+v, want none left, the same no-cursor shape PlaceholderEditions uses", found)
	}
}
