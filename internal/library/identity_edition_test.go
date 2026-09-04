package library_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

func TestResolveWorks_UnknownEditionExternalIDCreatesAnEdition(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)

	_, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"},
		Edition: &library.EditionLink{
			Provider: "musicbrainz:release", ExternalID: "release-1", Confidence: 1.0,
			Edition: library.EditionIdentity{Kind: "unknown", Label: "release-1"},
		},
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}

	edition, err := queries.GetEditionByExternalID(t.Context(), librarydb.GetEditionByExternalIDParams{
		Provider: "musicbrainz:release", ExternalID: "release-1",
	})
	if err != nil {
		t.Fatalf("GetEditionByExternalID: %v", err)
	}
	if edition.WorkID != work.ID {
		t.Errorf("edition.WorkID = %d, want %d (the work this link resolved to)", edition.WorkID, work.ID)
	}
	if edition.Kind != "unknown" || edition.Label != "release-1" {
		t.Errorf("edition = %+v, want kind=unknown label=release-1", edition)
	}
}

// Two tracks of the same release share one edition, exactly as they share one work.
func TestResolveWorks_RepeatedEditionExternalIDReusesTheSameEdition(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	link := library.WorkLink{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"},
		Edition: &library.EditionLink{
			Provider: "musicbrainz:release", ExternalID: "release-1", Confidence: 1.0,
			Edition: library.EditionIdentity{Kind: "unknown", Label: "release-1"},
		},
	}

	if _, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{link, link}); err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	editions, err := queries.ListEditions(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListEditions: %v", err)
	}
	if len(editions) != 1 {
		t.Errorf("editions = %d, want 1", len(editions))
	}
}

// A WorkLink naming no edition must not create one -- an unreleased-MBID file stays
// work-only, the same as before this feature existed.
func TestResolveWorks_NoEditionMeansNoEditionRow(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)

	_, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"},
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	editions, err := queries.ListEditions(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListEditions: %v", err)
	}
	if len(editions) != 0 {
		t.Errorf("editions = %d, want 0", len(editions))
	}
}
