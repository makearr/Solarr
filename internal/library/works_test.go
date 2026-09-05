package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

func TestListWorks_FiltersNarrowToWhatWasAsked(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)

	createWork(t, opened, "music", "amber rivers")
	createWork(t, opened, "music", "broken signals")
	createWork(t, opened, "tv", "amber gardens")

	page, err := works.List(t.Context(), library.WorkFilter{MediaType: "music"},
		library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("got %d works, want the 2 music ones", len(page))
	}
	for _, work := range page {
		if work.MediaType != "music" {
			t.Errorf("work %d is %s, which the filter excluded", work.ID, work.MediaType)
		}
	}
}

// An unset filter must not narrow anything, or the list silently hides rows a user did
// not ask to hide.
func TestListWorks_AnUnsetFilterExcludesNothing(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)

	createWork(t, opened, "music", "amber rivers")
	createWork(t, opened, "tv", "broken signals")

	page, err := works.List(t.Context(), library.WorkFilter{}, library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page) != 2 {
		t.Errorf("got %d works, want both: an empty filter is not a filter", len(page))
	}
}

func TestListWorks_FiltersCombineWithAnd(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)

	wanted := createWork(t, opened, "music", "amber rivers")
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.UpdateWorkTimestamp(ctx, librarydb.UpdateWorkTimestampParams{ID: wanted.ID, UpdatedAt: 2})
	})
	createWork(t, opened, "tv", "amber gardens")
	createWork(t, opened, "music", "zenith machines")

	page, err := works.List(t.Context(),
		library.WorkFilter{MediaType: "music", TitlePrefix: "a"}, library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page) != 1 || page[0].ID != wanted.ID {
		t.Errorf("got %d works, want only %d: the two filters must both apply", len(page), wanted.ID)
	}
}

func linkRootFolder(t *testing.T, opened *store.Store, workID, rootFolderID int64) {
	t.Helper()
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.LinkWorkRootFolder(ctx, librarydb.LinkWorkRootFolderParams{
			WorkID: workID, RootFolderID: rootFolderID, FirstSeenAt: firstSeen.Unix(),
		})
	})
}

func TestListWorks_RootFolderFilterNarrowsToLinkedWorks(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)

	inRoot1 := createWork(t, opened, "music", "amber rivers")
	inRoot2 := createWork(t, opened, "music", "broken signals")
	linkRootFolder(t, opened, inRoot1.ID, 1)
	linkRootFolder(t, opened, inRoot2.ID, 2)

	page, err := works.List(t.Context(), library.WorkFilter{RootFolderIDs: []int64{1}}, library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page) != 1 || page[0].ID != inRoot1.ID {
		t.Errorf("got %+v, want only the work linked to root folder 1", page)
	}
}

// A nil filter must be unfiltered, but a non-nil empty slice -- "the caller has no
// visible root folders at all" -- must match nothing. The two are easy to conflate
// since len() cannot distinguish them.
func TestListWorks_EmptyNonNilRootFolderFilterMatchesNothing(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)

	work := createWork(t, opened, "music", "amber rivers")
	linkRootFolder(t, opened, work.ID, 1)

	nilFiltered, err := works.List(t.Context(), library.WorkFilter{RootFolderIDs: nil}, library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List (nil): %v", err)
	}
	if len(nilFiltered) != 1 {
		t.Errorf("nil RootFolderIDs excluded a row, want unfiltered: got %+v", nilFiltered)
	}

	emptyFiltered, err := works.List(t.Context(), library.WorkFilter{RootFolderIDs: []int64{}}, library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List (empty): %v", err)
	}
	if len(emptyFiltered) != 0 {
		t.Errorf("empty non-nil RootFolderIDs = %+v, want nothing visible to mean nothing returned", emptyFiltered)
	}
}

// The confirmed bug private/next.md named: a subscription-discovered work has a
// desired_item.category_id but no work_root_folder row at all (nothing ever calls
// LinkWorkRootFolder for it), so it must still surface when the caller's visible
// categories are passed alongside their visible root folders -- neither alone is
// enough, since a work can have either or both.
func TestListWorks_CategoryFilterFindsASubscriptionDiscoveredWorkWithNoRootFolder(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	desired := library.NewDesired(opened)

	scanned := createWork(t, opened, "music", "amber rivers")
	linkRootFolder(t, opened, scanned.ID, 1)

	subscribed := createWork(t, opened, "music", "broken signals")
	if _, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: subscribed.ID, CategoryID: 9, ProfileID: 1, Monitored: true,
	}); err != nil {
		t.Fatalf("EnsureAlbumItem: %v", err)
	}

	page, err := works.List(t.Context(),
		library.WorkFilter{RootFolderIDs: []int64{1}, CategoryIDs: []int64{9}}, library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("got %d works, want both the scanned and the subscription-discovered one: %+v", len(page), page)
	}

	// The subscription-discovered work must NOT appear if its category is not
	// in the caller's visible set -- the fix must not leak everything with any
	// desired_item row, only what the caller's own categories admit.
	restricted, err := works.List(t.Context(),
		library.WorkFilter{RootFolderIDs: []int64{1}, CategoryIDs: []int64{}}, library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List (restricted): %v", err)
	}
	if len(restricted) != 1 || restricted[0].ID != scanned.ID {
		t.Errorf("got %+v, want only the scanned work once the subscription's category is not visible", restricted)
	}
}

// Sort titles repeat, so a page boundary lands inside a group. Mutation-testing the id
// tie-break away drops 230 of 250 rows silently (notes section 11).
func TestListWorks_PaginationWalksEveryRowExactlyOnce(t *testing.T) {
	const total = 250
	const pageSize = 20

	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	// One sort title for all of them, which is the case that breaks.
	for range total {
		createWork(t, opened, "music", "Greatest Hits")
	}

	seen := make(map[int64]int, total)
	cursor := library.WorkCursor{}
	for page := 0; ; page++ {
		if page > total {
			t.Fatal("pagination did not terminate; the cursor is not advancing")
		}

		rows, err := works.List(t.Context(), library.WorkFilter{}, cursor, pageSize)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			seen[row.ID]++
		}
		last := rows[len(rows)-1]
		cursor = library.WorkCursor{SortTitle: last.SortTitle, ID: last.ID}
	}

	if len(seen) != total {
		t.Errorf("walked %d distinct works, want %d -- %d skipped",
			len(seen), total, total-len(seen))
	}
	for id, times := range seen {
		if times != 1 {
			t.Errorf("work %d returned %d times", id, times)
		}
	}
}

func TestListWorks_ArtistIsThePersonCreditedInTheArtistRole(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	people := library.NewPeople(opened)

	credited := createWork(t, opened, "music", "KI Pecezinu")
	uncredited := createWork(t, opened, "music", "Unknown Pleasures")

	personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "artist-1",
		Person: library.PersonIdentity{Name: "Katupoken", SortName: "Katupoken"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddCredit(t.Context(), credited.ID, personID, "artist", 0); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}
	// A non-artist role must not surface as the list's artist column.
	if err := people.AddCredit(t.Context(), uncredited.ID, personID, "producer", 0); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}

	page, err := works.List(t.Context(), library.WorkFilter{}, library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	byID := make(map[int64]library.WorkListRow, len(page))
	for _, row := range page {
		byID[row.ID] = row
	}
	if !byID[credited.ID].Artist.Valid || byID[credited.ID].Artist.String != "Katupoken" {
		t.Errorf("Artist for the artist-credited work = %+v, want Katupoken", byID[credited.ID].Artist)
	}
	if byID[uncredited.ID].Artist.Valid {
		t.Errorf("Artist for the producer-only work = %+v, want no artist credit", byID[uncredited.ID].Artist)
	}
}

func TestListWorks_SortByYearOrdersAndPaginatesWithTies(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)

	// Two works share a year, so the id tie-break must still walk every row
	// exactly once (same reasoning as the sort_title pagination test above).
	older := createWorkWithYear(t, opened, "Older Album", 1990)
	tiedA := createWorkWithYear(t, opened, "Tied Album A", 2000)
	tiedB := createWorkWithYear(t, opened, "Tied Album B", 2000)
	newer := createWorkWithYear(t, opened, "Newer Album", 2010)

	filter := library.WorkFilter{Sort: library.WorkSortYear}
	var seen []int64
	cursor := library.WorkCursor{}
	for {
		page, err := works.List(t.Context(), filter, cursor, 1)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page) == 0 {
			break
		}
		last := page[0]
		seen = append(seen, last.ID)
		cursor = library.WorkCursor{SortYear: last.Year.Int64, ID: last.ID}
	}

	if len(seen) != 4 {
		t.Fatalf("walked %d works, want 4", len(seen))
	}
	if seen[0] != older.ID || seen[3] != newer.ID {
		t.Errorf("order = %v, want %d first and %d last (ascending by year)", seen, older.ID, newer.ID)
	}
	orderedForward := seen[1] == tiedA.ID && seen[2] == tiedB.ID
	orderedBackward := seen[1] == tiedB.ID && seen[2] == tiedA.ID
	if !orderedForward && !orderedBackward {
		t.Errorf("order = %v, want the two tied-year works in the middle, each exactly once", seen)
	}
}

func TestListWorks_SortDirectionDescendingReversesOrder(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)

	older := createWorkWithYear(t, opened, "Older Album", 1990)
	newer := createWorkWithYear(t, opened, "Newer Album", 2010)

	page, err := works.List(t.Context(),
		library.WorkFilter{Sort: library.WorkSortYear, Direction: library.SortDescending},
		library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page) != 2 || page[0].ID != newer.ID || page[1].ID != older.ID {
		t.Errorf("descending order = %+v, want %d then %d", page, newer.ID, older.ID)
	}
}

func createWorkWithYear(t *testing.T, opened *store.Store, title string, year int64) librarydb.Work {
	t.Helper()
	var created librarydb.Work
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateWork(ctx, librarydb.CreateWorkParams{
			MediaType: "music", CanonicalTitle: title, SortTitle: title,
			Year: sql.NullInt64{Int64: year, Valid: true}, AddedAt: 1, UpdatedAt: 1,
		})
		created = row
		return err
	})
	return created
}
