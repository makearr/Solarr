package library_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

func TestResolveWorks_UnknownExternalIDCreatesAWork(t *testing.T) {
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
	if work.MediaType != "music" || work.CanonicalTitle != "KI Pecezinu" || work.SortTitle != "ki pecezinu" {
		t.Errorf("work = %+v, want music/KI Pecezinu/ki pecezinu", work)
	}
}

// A second link naming the same (provider, external id) must resolve to the work the
// first one just created, not a duplicate -- this is the ordinary case whenever an
// album's tracks share one release-group MBID.
func TestResolveWorks_RepeatedExternalIDReusesTheSameWork(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	link := library.WorkLink{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"},
	}

	if _, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{link, link}); err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	assertWorkCount(t, opened, 1)
}

// Calling ResolveWorks again for a link an earlier batch already resolved must not
// create a second work -- the whole point of resolving by external id.
func TestResolveWorks_AlreadyResolvedExternalIDIsIdempotent(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	link := library.WorkLink{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"},
	}

	if _, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{link}); err != nil {
		t.Fatalf("ResolveWorks (first): %v", err)
	}
	if _, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{link}); err != nil {
		t.Fatalf("ResolveWorks (second): %v", err)
	}
	assertWorkCount(t, opened, 1)
}

func TestResolveWorks_DifferentExternalIDsCreateDifferentWorks(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)

	_, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{
		{Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 1.0,
			Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"}},
		{Provider: "musicbrainz:release-group", ExternalID: "rg-2", Confidence: 1.0,
			Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "Zor A", SortTitle: "zor a"}},
	})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	assertWorkCount(t, opened, 2)
}

func TestResolveWorks_EmptyBatchIsANoop(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)

	if _, err := identity.ResolveWorks(t.Context(), firstSeen, nil); err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	assertWorkCount(t, opened, 0)
}

// assertWorkCount reads through the existing list surface, at a page size safely
// above anything these tests create.
func assertWorkCount(t *testing.T, opened *store.Store, want int) {
	t.Helper()
	works, err := library.NewWorks(opened).List(t.Context(), library.WorkFilter{}, library.WorkCursor{}, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(works) != want {
		t.Errorf("work count = %d, want %d", len(works), want)
	}
}
