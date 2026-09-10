package library_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

func createPlaceholderEdition(t *testing.T, opened *librarydb.Queries, identity library.Identity, releaseGroupID, releaseID string) (workID, editionID int64) {
	t.Helper()
	_, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "musicbrainz:release-group", ExternalID: releaseGroupID, Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"},
		Edition: &library.EditionLink{
			Provider: "musicbrainz:release", ExternalID: releaseID, Confidence: 1.0,
			Edition: library.EditionIdentity{Kind: "unknown", Label: releaseID},
		},
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	work, err := opened.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: releaseGroupID,
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	edition, err := opened.GetEditionByExternalID(t.Context(), librarydb.GetEditionByExternalIDParams{
		Provider: "musicbrainz:release", ExternalID: releaseID,
	})
	if err != nil {
		t.Fatalf("GetEditionByExternalID: %v", err)
	}
	return work.ID, edition.ID
}

func TestPlaceholderEditions_ListsOnlyKindUnknown(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	createPlaceholderEdition(t, queries, identity, "rg-1", "release-1")

	found, err := identity.PlaceholderEditions(t.Context(), 10)
	if err != nil {
		t.Fatalf("PlaceholderEditions: %v", err)
	}
	if len(found) != 1 || found[0].ExternalID != "release-1" {
		t.Fatalf("PlaceholderEditions = %+v, want exactly the one placeholder", found)
	}

	if err := identity.RefineEdition(t.Context(), firstSeen.Unix(), found[0].EditionID, found[0].WorkID, nil, "",
		library.EditionRefinement{Kind: "official"}, nil); err != nil {
		t.Fatalf("RefineEdition: %v", err)
	}

	// A refined edition's kind is no longer 'unknown', so it must drop out of the
	// set on its own -- the whole point of not needing a separate cursor.
	remaining, err := identity.PlaceholderEditions(t.Context(), 10)
	if err != nil {
		t.Fatalf("PlaceholderEditions after refine: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("PlaceholderEditions after refine = %+v, want none left", remaining)
	}
}

func TestRefineEdition_UpdatesTheEditionRow(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	workID, editionID := createPlaceholderEdition(t, queries, identity, "rg-2", "release-2")

	year := int64(1997)
	runtime := int64(2820000)
	err := identity.RefineEdition(t.Context(), firstSeen.Unix(), editionID, workID, nil, "", library.EditionRefinement{
		Kind: "official", Year: &year, Format: "CD", CatalogueNumber: "CDNODATA02",
		Barcode: "724385502125", RuntimeMs: &runtime,
	}, nil)
	if err != nil {
		t.Fatalf("RefineEdition: %v", err)
	}

	edition, err := queries.GetEdition(t.Context(), editionID)
	if err != nil {
		t.Fatalf("GetEdition: %v", err)
	}
	if edition.Kind != "official" || edition.Format.String != "CD" || edition.CatalogueNumber.String != "CDNODATA02" {
		t.Errorf("edition = %+v, want the refined fields", edition)
	}
	if !edition.Year.Valid || edition.Year.Int64 != 1997 {
		t.Errorf("Year = %+v, want 1997", edition.Year)
	}
	// The label an Identifier chain resolved this edition by (the release MBID)
	// must survive refinement -- it is still what the row is found by.
	if edition.Label != "release-2" {
		t.Errorf("Label = %q, changed by refinement -- it must not be", edition.Label)
	}
}

func TestRefineEdition_WorkYearIsSetOnlyWhenStillNull(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	workID, editionID := createPlaceholderEdition(t, queries, identity, "rg-3", "release-3")

	first := int64(1997)
	if err := identity.RefineEdition(t.Context(), firstSeen.Unix(), editionID, workID, &first, "1997-05-21",
		library.EditionRefinement{Kind: "official"}, nil); err != nil {
		t.Fatalf("RefineEdition (first): %v", err)
	}
	work, err := queries.GetWork(t.Context(), workID)
	if err != nil {
		t.Fatalf("GetWork: %v", err)
	}
	if !work.Year.Valid || work.Year.Int64 != 1997 {
		t.Fatalf("Year after first refine = %+v, want 1997", work.Year)
	}

	// A later refinement (a reissue's edition, say) must not overwrite the year
	// the first one already set, but work_date has no such guard yet -- there
	// is nothing to protect it from (no manual override UI exists), so it
	// tracks whatever the most recent enrichment said.
	second := int64(2017)
	if err := identity.RefineEdition(t.Context(), firstSeen.Unix(), editionID, workID, &second, "2017-06-01",
		library.EditionRefinement{Kind: "official"}, nil); err != nil {
		t.Fatalf("RefineEdition (second): %v", err)
	}
	work, err = queries.GetWork(t.Context(), workID)
	if err != nil {
		t.Fatalf("GetWork: %v", err)
	}
	if work.Year.Int64 != 1997 {
		t.Errorf("Year after second refine = %d, want the original 1997 kept", work.Year.Int64)
	}

	dates, err := queries.ListWorkDates(t.Context(), workID)
	if err != nil {
		t.Fatalf("ListWorkDates: %v", err)
	}
	if len(dates) != 1 || dates[0].Kind != library.WorkDateStreet || dates[0].Date != "2017-06-01" {
		t.Fatalf("work dates = %+v, want one street date overwritten to 2017-06-01", dates)
	}
}

func TestSetWorkDescription_WritesTheDescription(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	workID, _ := createPlaceholderEdition(t, queries, identity, "rg-description-1", "release-description-1")

	if err := identity.SetWorkDescription(t.Context(), firstSeen.Unix(), workID,
		"The Fura Husu of the Teke is..."); err != nil {
		t.Fatalf("SetWorkDescription: %v", err)
	}

	work, err := queries.GetWork(t.Context(), workID)
	if err != nil {
		t.Fatalf("GetWork: %v", err)
	}
	if !work.Description.Valid || work.Description.String != "The Fura Husu of the Teke is..." {
		t.Errorf("Description = %+v, want the description written", work.Description)
	}
}

func TestRefineEdition_CreatesPartsAndLinksTheirExternalID(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	workID, editionID := createPlaceholderEdition(t, queries, identity, "rg-4", "release-4")

	runtime := int64(284400)
	err := identity.RefineEdition(t.Context(), firstSeen.Unix(), editionID, workID, nil, "",
		library.EditionRefinement{Kind: "official"}, []library.PartIdentity{
			{
				MediaRef: "disc1:track1", SortKey: "00010001", Title: "Sesoge", RuntimeMs: &runtime,
				ExternalID: &library.PartExternalID{
					Provider: "musicbrainz:recording", ExternalID: "1509e192-0467-437e-80d9-138f3222fd39", Confidence: 1.0,
				},
			},
		})
	if err != nil {
		t.Fatalf("RefineEdition: %v", err)
	}

	part, err := queries.FindWorkPart(t.Context(), librarydb.FindWorkPartParams{WorkID: workID, MediaRef: "disc1:track1"})
	if err != nil {
		t.Fatalf("FindWorkPart: %v", err)
	}
	if part.Title.String != "Sesoge" || part.SortKey != "00010001" || part.RuntimeMs.Int64 != 284400 {
		t.Errorf("part = %+v, want the refined track fields", part)
	}

	byExternal, err := queries.FindPartsByExternalID(t.Context(), librarydb.FindPartsByExternalIDParams{
		Provider: "musicbrainz:recording", ExternalID: "1509e192-0467-437e-80d9-138f3222fd39",
	})
	if err != nil {
		t.Fatalf("FindPartsByExternalID: %v", err)
	}
	if len(byExternal) != 1 || byExternal[0].ID != part.ID {
		t.Errorf("FindPartsByExternalID = %+v, want exactly the part just created", byExternal)
	}
}

// A part enrichment resolves twice (the same recording refined again, or a second
// medium sharing a track number by mistake) must update in place, never duplicate
// -- resolvePart's find-before-create, mirroring resolveEdition/findOrCreateWork.
func TestRefineEdition_RefiningTheSamePartTwiceDoesNotDuplicateIt(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	workID, editionID := createPlaceholderEdition(t, queries, identity, "rg-5", "release-5")

	part := library.PartIdentity{MediaRef: "disc1:track1", SortKey: "0001", Title: "Sesoge"}
	for range 2 {
		if err := identity.RefineEdition(t.Context(), firstSeen.Unix(), editionID, workID, nil, "",
			library.EditionRefinement{Kind: "official"}, []library.PartIdentity{part}); err != nil {
			t.Fatalf("RefineEdition: %v", err)
		}
	}

	parts, err := queries.ListWorkParts(t.Context(), workID)
	if err != nil {
		t.Fatalf("ListWorkParts: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("ListWorkParts = %+v, want exactly one part after refining it twice", parts)
	}
}
