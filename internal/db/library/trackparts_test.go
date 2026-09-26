package library_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

// setUpAlbumWithParts is a small helper: resolves a work with a root folder
// (on-disk visibility), a primary artist credit, and the given tracks.
func setUpAlbumWithParts(
	t *testing.T, identity library.Identity, people library.People,
	externalID, title, sortTitle, artistExternalID, artistName string, rootFolderID int64, trackTitles ...string,
) int64 {
	t.Helper()
	workIDs, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "test", ExternalID: externalID, Confidence: 1,
		Work:         library.WorkIdentity{MediaType: "music", CanonicalTitle: title, SortTitle: sortTitle},
		RootFolderID: rootFolderID,
		Edition: &library.EditionLink{
			Provider: "test", ExternalID: externalID + "-edition", Confidence: 1,
			Edition: library.EditionIdentity{Kind: "unknown", Label: externalID + "-edition"},
		},
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	workID := workIDs[0]

	placeholders, err := identity.PlaceholderEditions(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("PlaceholderEditions: %v", err)
	}
	var editionID int64
	for _, placeholder := range placeholders {
		if placeholder.WorkID == workID {
			editionID = placeholder.EditionID
		}
	}
	if editionID == 0 {
		t.Fatalf("no placeholder edition found for work %d", workID)
	}

	parts := make([]library.PartIdentity, len(trackTitles))
	for index, trackTitle := range trackTitles {
		parts[index] = library.PartIdentity{
			MediaRef: fmt.Sprintf("disc1:track%d", index+1),
			SortKey:  fmt.Sprintf("%04d", index+1),
			Title:    trackTitle,
		}
	}
	if err := identity.RefineEdition(t.Context(), firstSeen.Unix(), editionID, workID, nil, "", "",
		library.EditionRefinement{Kind: "album"}, parts); err != nil {
		t.Fatalf("RefineEdition: %v", err)
	}

	if artistName != "" {
		personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
			Provider: "test", ExternalID: artistExternalID,
			Person: library.PersonIdentity{Name: artistName, SortName: artistName},
		})
		if err != nil {
			t.Fatalf("ResolvePerson: %v", err)
		}
		if err := people.AddCredit(t.Context(), workID, personID, "artist", 0); err != nil {
			t.Fatalf("AddCredit: %v", err)
		}
	}
	return workID
}

// TestTrackParts_ListsTracksGroupedByArtistThenAlbum is ADR-54's Tracks-level
// acceptance case: rows come back grouped Artist -> Album -> Track#.
func TestTrackParts_ListsTracksGroupedByArtistThenAlbum(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	people := library.NewPeople(opened)
	tracks := library.NewTrackParts(opened)

	setUpAlbumWithParts(t, identity, people, "b-album", "B Album", "b album",
		"artist-b", "Bravo", 1, "B1", "B2")
	setUpAlbumWithParts(t, identity, people, "a-album", "A Album", "a album",
		"artist-a", "Zemih", 1, "A1", "A2", "A3")

	rows, _, hasMore, err := tracks.List(t.Context(),
		library.TrackFilter{RootFolderIDs: []int64{1}}, library.TrackAlbumCursor{}, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if hasMore {
		t.Errorf("hasMore = true, want false (only 2 albums, page size 10)")
	}
	if len(rows) != 5 {
		t.Fatalf("rows = %+v, want 5 tracks total", rows)
	}
	wantTitles := []string{"A1", "A2", "A3", "B1", "B2"}
	for index, row := range rows {
		if !row.Title.Valid || row.Title.String != wantTitles[index] {
			t.Errorf("row %d title = %+v, want %q", index, row.Title, wantTitles[index])
		}
	}
	if rows[0].ArtistName.String != "Zemih" || rows[4].ArtistName.String != "Bravo" {
		t.Errorf("artist names = %q / %q, want Zemih then Bravo", rows[0].ArtistName.String, rows[4].ArtistName.String)
	}
}

// Paginating at the album boundary (TrackParts.List's own doc comment) must
// never split one album's tracks across two pages.
func TestTrackParts_PaginatesAtTheAlbumBoundaryNeverSplittingOneAlbum(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	people := library.NewPeople(opened)
	tracks := library.NewTrackParts(opened)

	setUpAlbumWithParts(t, identity, people, "a-album", "A Album", "a album",
		"artist-a", "Zemih", 1, "A1", "A2", "A3")
	setUpAlbumWithParts(t, identity, people, "b-album", "B Album", "b album",
		"artist-b", "Bravo", 1, "B1", "B2")

	firstPage, cursor, hasMore, err := tracks.List(t.Context(),
		library.TrackFilter{RootFolderIDs: []int64{1}}, library.TrackAlbumCursor{}, 1)
	if err != nil {
		t.Fatalf("List (first page): %v", err)
	}
	if !hasMore {
		t.Fatal("hasMore = false, want true: a second album remains")
	}
	if len(firstPage) != 3 {
		t.Fatalf("first page = %+v, want all 3 of Zemih's tracks, none of Bravo's", firstPage)
	}

	secondPage, _, hasMore, err := tracks.List(t.Context(),
		library.TrackFilter{RootFolderIDs: []int64{1}}, cursor, 1)
	if err != nil {
		t.Fatalf("List (second page): %v", err)
	}
	if hasMore {
		t.Error("hasMore = true on the second page, want false")
	}
	if len(secondPage) != 2 {
		t.Fatalf("second page = %+v, want Bravo's 2 tracks", secondPage)
	}
}

// The plan assertion (reaches work_primary_artist_sort, never sorts) lives in
// bench_test.go's TestADR28_TrackAlbumPagePlanReachesTheIndexAndNeverSorts,
// at corpus scale -- a small table lets SQLite get away with a plan that says
// nothing about 500k rows (bench_test.go's own planCases doc comment).

// Descending orders the albums Z to A; each album's tracks keep their own order.
func TestTrackParts_DescendingReversesTheAlbumsNotTheirTracks(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	people := library.NewPeople(opened)
	tracks := library.NewTrackParts(opened)
	setUpAlbumWithParts(t, identity, people, "a-album", "A Album", "a album", "artist-a", "Zemih", 1, "A1", "A2")
	setUpAlbumWithParts(t, identity, people, "b-album", "B Album", "b album", "artist-b", "Bravo", 1, "B1", "B2")

	filter := library.TrackFilter{RootFolderIDs: []int64{1}, Descending: true}
	var got []string
	cursor := library.TrackAlbumCursor{}
	for {
		page, next, hasMore, err := tracks.List(t.Context(), filter, cursor, 1)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, row := range page {
			got = append(got, row.Title.String)
		}
		if !hasMore {
			break
		}
		cursor = next
	}
	if want := []string{"B1", "B2", "A1", "A2"}; !slices.Equal(got, want) {
		t.Errorf("tracks = %q, want %q", got, want)
	}
}
