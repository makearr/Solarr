package library_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

func TestResolveWorks_RecordsTheRootFolderLink(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)

	_, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 1.0,
		Work:         library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"},
		RootFolderID: 7,
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

	roots, err := identity.RootFoldersForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("RootFoldersForWork: %v", err)
	}
	if len(roots) != 1 || roots[0] != 7 {
		t.Errorf("RootFoldersForWork = %v, want [7]", roots)
	}
}

// A second scan of the same root must not error or duplicate the link (the ON
// CONFLICT DO NOTHING upsert) -- the common case, since a resumed or repeated
// sweep resolves the same work from the same root every time.
func TestResolveWorks_RootFolderLinkIsIdempotent(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)

	link := library.WorkLink{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 1.0,
		Work:         library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"},
		RootFolderID: 7,
	}
	if _, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{link}); err != nil {
		t.Fatalf("ResolveWorks (first): %v", err)
	}
	if _, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{link}); err != nil {
		t.Fatalf("ResolveWorks (second): %v", err)
	}

	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	roots, err := identity.RootFoldersForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("RootFoldersForWork: %v", err)
	}
	if len(roots) != 1 {
		t.Errorf("RootFoldersForWork = %v, want exactly one entry after resolving twice", roots)
	}
}

// A work found under two root folders (a discography split across drives, say)
// must report both -- the filter needs every root a work was actually seen under.
func TestResolveWorks_AWorkCanBeLinkedToSeveralRootFolders(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)

	base := library.WorkLink{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"},
	}
	first, second := base, base
	first.RootFolderID = 7
	second.RootFolderID = 9
	if _, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{first, second}); err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	roots, err := identity.RootFoldersForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("RootFoldersForWork: %v", err)
	}
	if len(roots) != 2 || roots[0] != 7 || roots[1] != 9 {
		t.Errorf("RootFoldersForWork = %v, want [7 9]", roots)
	}
}
