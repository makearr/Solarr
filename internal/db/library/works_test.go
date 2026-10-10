package library_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/listfilter"
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
		Person: library.PersonIdentity{Name: "Katupoken"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddLeadCredit(t.Context(), credited.ID, personID, "artist"); err != nil {
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

// The Albums view groups by artist, so every album of one artist must arrive
// together, titles in order within, and a page boundary inside a group must
// not drop or repeat a row -- walked one row per page to force every boundary.
func TestListWorks_SortByArtistGroupsThenOrdersByTitleAcrossPages(t *testing.T) {
	opened, queries := newLibrary(t)
	works := library.NewWorks(opened)

	people := library.NewPeople(opened)
	setArtist := func(workID int64, sortName string) {
		t.Helper()
		personID := resolvePersonNamed(t, people, "artist-"+sortName, sortName)
		if err := queries.SetWorkLeadPerson(t.Context(), librarydb.SetWorkLeadPersonParams{
			LeadPersonID:       sql.NullInt64{Int64: personID, Valid: true},
			LeadPersonSortName: sql.NullString{String: sortName, Valid: true},
			ID:                 workID,
		}); err != nil {
			t.Fatalf("SetWorkLeadPerson: %v", err)
		}
	}
	pipoB := createWork(t, opened, "music", "Kebilegev Bibihi")
	hekaW := createWork(t, opened, "music", "Zanuvode")
	pipoA := createWork(t, opened, "music", "Aretimok")
	hekaA := createWork(t, opened, "music", "Dumucamoko")
	unknown := createWork(t, opened, "music", "Zebra")
	unknownFirst := createWork(t, opened, "music", "Abacus")
	lower := createWork(t, opened, "music", "Tavel")
	setArtist(lower.ID, "bokaru")
	setArtist(pipoB.ID, "P!po")
	setArtist(hekaW.ID, "Heka")
	setArtist(pipoA.ID, "P!po")
	setArtist(hekaA.ID, "Heka")

	filter := library.WorkFilter{Sort: library.WorkSortArtist}
	var seen []int64
	cursor := library.WorkCursor{}
	for range 10 {
		page, err := works.List(t.Context(), filter, cursor, 1)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page) == 0 {
			break
		}
		last := page[0]
		seen = append(seen, last.ID)
		cursor = library.WorkCursor{SortArtist: last.ArtistSortName, SortTitle: last.SortTitle, ID: last.ID}
	}

	// A work with no artist sorts last (R2.0 finding 12), titles in order there too; case is ignored.
	want := []int64{lower.ID, hekaA.ID, hekaW.ID, pipoA.ID, pipoB.ID, unknownFirst.ID, unknown.ID}
	if !slices.Equal(seen, want) {
		t.Errorf("order = %v, want %v", seen, want)
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

// The one thing todo 4.20's Fresh filter must get right: a full day-precision
// date on or after the cutoff matches, a year-only or month-only date never
// does (day precision is the whole point), and a work with no date at all
// is silently excluded rather than erroring.
func TestListWorks_FreshFiltersToDayPreciseRecentReleases(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)

	recent := createWorkWithDate(t, opened, "Recent Release", "2026-09-05")
	createWorkWithDate(t, opened, "Old Release", "1997-05-21")
	createWorkWithDate(t, opened, "Year Only Release", "2026")
	createWork(t, opened, "music", "No Date At All")

	page, err := works.List(t.Context(), library.WorkFilter{FreshSince: "2026-09-01"}, library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page) != 1 || page[0].ID != recent.ID {
		t.Fatalf("Fresh page = %+v, want only %q (%d)", page, "Recent Release", recent.ID)
	}
}

// The Wanted screen's own filter (2026-09-11): monitored and not COMPLETE
// includes a work, an unmonitored one is excluded even with the same state,
// and a monitored-but-COMPLETE one is excluded too -- COMPLETE means nothing
// is missing, so it is not "wanted" regardless of monitored. wanted_state
// comes back set only for the row that actually matched.
func TestListWorks_WantedFiltersToMonitoredIncompleteDesiredItems(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)

	missing := createWork(t, opened, "music", "Missing Album")
	addTrack(t, opened, missing.ID)
	createDesired(t, opened, missing.ID, "SET", 10)

	complete := createWork(t, opened, "music", "Complete Album")
	addTrack(t, opened, complete.ID)
	completeItem := createDesired(t, opened, complete.ID, "SET", 10)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{
			ID: completeItem.ID, State: "COMPLETE", PresentCount: 10,
		})
	})

	unmonitored := createWork(t, opened, "music", "Unmonitored Album")
	addTrack(t, opened, unmonitored.ID)
	unmonitoredItem := createDesired(t, opened, unmonitored.ID, "SET", 10)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetDesiredItemMonitored(ctx, librarydb.SetDesiredItemMonitoredParams{
			ID: unmonitoredItem.ID, Monitored: 0,
		})
	})

	createWork(t, opened, "music", "No Desired Item At All")

	// M1d: an album below its cutoff is Wanted -> Cutoff Unmet, never Missing.
	belowCutoff := createWork(t, opened, "music", "Below Cutoff Album")
	addTrack(t, opened, belowCutoff.ID)
	belowItem := createDesired(t, opened, belowCutoff.ID, "SET", 10)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{
			ID: belowItem.ID, State: "UPGRADABLE", PresentCount: 10,
		})
	})

	page, err := works.List(t.Context(), library.WorkFilter{WantedOnly: true}, library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page) != 1 || page[0].ID != missing.ID {
		t.Fatalf("wanted page = %+v, want only %q (%d)", page, "Missing Album", missing.ID)
	}
	cutoff, err := works.List(t.Context(), library.WorkFilter{CutoffUnmetOnly: true}, library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List cutoff unmet: %v", err)
	}
	if len(cutoff) != 1 || cutoff[0].ID != belowCutoff.ID {
		t.Fatalf("cutoff unmet page = %+v, want only %q", cutoff, "Below Cutoff Album")
	}
	if !page[0].WantedState.Valid || page[0].WantedState.String != "MISSING" {
		t.Errorf("WantedState = %+v, want MISSING", page[0].WantedState)
	}
}

// Wanted sorts by last searched: never searched first, then oldest, across pages.
func TestListWorks_SortBySearchedPutsNeverSearchedFirst(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	desired := library.NewDesired(opened)
	for _, title := range []string{"Searched Late", "Never Searched", "Searched Early"} {
		work := createWork(t, opened, "music", title)
		addTrack(t, opened, work.ID)
		item := createDesired(t, opened, work.ID, "SET", 10)
		switch title {
		case "Searched Late":
			mustRecordSearched(t, desired, item.ID, 2000)
		case "Searched Early":
			mustRecordSearched(t, desired, item.ID, 1000)
			mustRecordSearched(t, desired, item.ID, 1500)
		}
	}

	var got []string
	var after library.WorkCursor
	for {
		page, err := works.List(t.Context(), library.WorkFilter{WantedOnly: true, Sort: library.WorkSortSearched}, after, 1)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page) == 0 {
			break
		}
		got = append(got, fmt.Sprintf("%s@%d", page[0].CanonicalTitle, page[0].LastSearchedAt))
		after = library.WorkCursor{SortSearched: page[0].LastSearchedAt, ID: page[0].ID}
	}
	want := []string{"Never Searched@0", "Searched Early@1500", "Searched Late@2000"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func mustRecordSearched(t *testing.T, desired library.Desired, itemID, at int64) {
	t.Helper()
	if err := desired.RecordSearched(t.Context(), itemID, at); err != nil {
		t.Fatalf("RecordSearched: %v", err)
	}
}

func createWorkWithDate(t *testing.T, opened *store.Store, title, date string) librarydb.Work {
	t.Helper()
	created := createWorkWithYear(t, opened, title, 0)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetWorkDate(ctx, librarydb.SetWorkDateParams{
			WorkID: created.ID, Kind: library.WorkDateStreet, Region: "", Date: date, Source: "musicbrainz",
		})
	})
	return created
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

func TestWorks_AlternateTitles_ExcludesPrimaryAndIncludesEverythingElse(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Discovery")
	works := library.NewWorks(opened)

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, title := range []struct{ title, kind string }{
			{"Discovery", "primary"},
			{"Découverte", "translit"},
			{"Discoverie", "alias"},
		} {
			if _, err := q.AddWorkTitle(ctx, librarydb.AddWorkTitleParams{
				WorkID: work.ID, Title: title.title, Normalized: title.title, Kind: title.kind,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	aliases, err := works.AlternateTitles(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("AlternateTitles: %v", err)
	}
	if len(aliases) != 2 {
		t.Fatalf("aliases = %v, want 2 (primary excluded)", aliases)
	}
	found := map[string]bool{}
	for _, alias := range aliases {
		found[alias] = true
	}
	if !found["Découverte"] || !found["Discoverie"] {
		t.Errorf("aliases = %v, want Découverte and Discoverie", aliases)
	}
	if found["Discovery"] {
		t.Errorf("aliases = %v, want the primary title excluded", aliases)
	}
}

func TestWorks_AlternateTitles_EmptyForAWorkWithNoTitlesRecorded(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Discovery")
	works := library.NewWorks(opened)

	aliases, err := works.AlternateTitles(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("AlternateTitles: %v", err)
	}
	if len(aliases) != 0 {
		t.Errorf("aliases = %v, want none -- nothing populates work_title yet", aliases)
	}
}

// The descending twin of the test above. The cursor WHERE is a row value, so
// it only agrees with the ORDER BY while the id tie-break runs in the same
// direction as the sort. A mixed direction still *looks* right -- it seeks,
// and a first page is correct -- but silently drops rows inside a tie group,
// which is exactly what a plan assertion cannot see.
func TestListWorks_DescendingPaginationWalksEveryRowExactlyOnce(t *testing.T) {
	const total = 250
	const pageSize = 20

	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	for range total {
		createWork(t, opened, "music", "Greatest Hits")
	}

	seen := make(map[int64]int, total)
	cursor := library.WorkCursor{}
	filter := library.WorkFilter{Direction: library.SortDescending}
	for page := 0; ; page++ {
		if page > total {
			t.Fatal("pagination did not terminate; the cursor is not advancing")
		}
		rows, err := works.List(t.Context(), filter, cursor, pageSize)
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
		t.Errorf("walked %d distinct works descending, want %d -- %d skipped",
			len(seen), total, total-len(seen))
	}
	for id, times := range seen {
		if times != 1 {
			t.Errorf("work %d returned %d times", id, times)
		}
	}
}

// A track wanted on its own before its album was monitored made a complete album read MISSING:
// the list took the oldest item. The album's own SET is its state (2026-09-18 finding).
func TestListWorks_StateIsTheAlbumsNotAnEarlierTracks(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "complete album")
	createDesired(t, opened, work.ID, "SINGLE", 1)
	album := createDesired(t, opened, work.ID, "SET", 12)
	err := opened.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE desired_item SET state = 'COMPLETE', present_count = 12 WHERE id = ?", album.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	page, err := library.NewWorks(opened).List(t.Context(), library.WorkFilter{}, library.WorkCursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || !page[0].WantedState.Valid || page[0].WantedState.String != "COMPLETE" {
		t.Errorf("works = %+v, want the album's COMPLETE", page)
	}
}

// Import revert deletes only the works a session created, so the flag must be exact: an existing
// work is matched, a new one created once, and the same key again in one batch reuses it.
func TestResolveWorksDetailed_CreatedOnlyForTheFirstLinkOfANewWork(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	link := func(id string) library.WorkLink {
		return library.WorkLink{Provider: "test", ExternalID: id, Confidence: 1,
			Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: id, SortTitle: id}}
	}
	if _, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{link("old")}); err != nil {
		t.Fatal(err)
	}

	resolutions, err := identity.ResolveWorksDetailed(t.Context(), firstSeen,
		[]library.WorkLink{link("new"), link("old"), link("new"), link("other")})
	if err != nil {
		t.Fatal(err)
	}
	var created []bool
	for _, r := range resolutions {
		created = append(created, r.WorkCreated)
	}
	if want := []bool{true, false, false, true}; !slices.Equal(created, want) {
		t.Errorf("created = %v, want %v", created, want)
	}
	if resolutions[0].WorkID != resolutions[2].WorkID || resolutions[0].WorkID == resolutions[3].WorkID {
		t.Errorf("ids = %+v: a repeated key must share its work and different keys must not", resolutions)
	}
	for _, r := range resolutions {
		work, err := library.NewWorks(opened).Detail(t.Context(), r.WorkID)
		if err != nil {
			t.Fatalf("work %d was not written: %v", r.WorkID, err)
		}
		if len(work.ExternalIDs) != 1 {
			t.Errorf("work %d has external ids %+v, want its own one", r.WorkID, work.ExternalIDs)
		}
	}
}

// An album whose tracks are not known yet may be complete on disk: it is not wanted and never
// searched until enrichment gives it a tracklist (answered 2026-09-28).
func TestWanted_AnAlbumWithoutATracklistIsWaitingNotMissing(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	waiting := createWork(t, opened, "music", "Waiting Album")
	createDesired(t, opened, waiting.ID, "SET", 10)
	missing := createWork(t, opened, "music", "Missing Album")
	addTrack(t, opened, missing.ID)
	createDesired(t, opened, missing.ID, "SET", 10)

	page, err := works.List(t.Context(), library.WorkFilter{WantedOnly: true}, library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page) != 1 || page[0].ID != missing.ID {
		t.Errorf("wanted page = %+v, want only the album with a tracklist", page)
	}
	items, err := library.NewDesired(opened).IncompleteItems(t.Context(), 0, 0, 0, 100)
	if err != nil {
		t.Fatalf("IncompleteItems: %v", err)
	}
	if len(items) != 1 || items[0].WorkID != missing.ID {
		t.Errorf("searched items = %+v, want only the album with a tracklist", items)
	}
}

// The dashboard's Recently added: newest file first, pages by the arrival cursor, works without files left out.
func TestListWorks_SortByImportedOrdersByTheNewestFile(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	physical := library.NewPhysical(opened)
	arrivals := map[string][]int64{"Two Files": {100, 300}, "One File": {200}, "Only Wanted": nil}
	for title, times := range arrivals {
		work := createWork(t, opened, "music", title)
		for index, at := range times {
			object := registerFileForArtwork(t, physical, 1, fmt.Sprintf("%s/%d.flac", title, index), fmt.Sprintf("%s%d", title, index))
			if err := opened.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
				return librarydb.New(tx).LinkWorkFile(ctx, librarydb.LinkWorkFileParams{WorkID: work.ID, MediaObjectID: object, FirstSeenAt: at})
			}); err != nil {
				t.Fatalf("LinkWorkFile: %v", err)
			}
		}
	}

	var got []string
	var after library.WorkCursor
	for {
		page, err := works.List(t.Context(), library.WorkFilter{Sort: library.WorkSortImported, Direction: library.SortDescending}, after, 1)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page) == 0 {
			break
		}
		got = append(got, fmt.Sprintf("%s@%d", page[0].CanonicalTitle, page[0].ArrivedAt))
		after = library.WorkCursor{SortImported: page[0].ArrivedAt, ID: page[0].ID}
	}
	want := []string{"Two Files@300", "One File@200"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// A poster marks an album shown by its artist's image: it has a primary artist and no cover of its own.
func TestListWorks_ArtistCoverMarksAnAlbumWithoutItsOwnCover(t *testing.T) {
	opened, queries := newLibrary(t)
	works := library.NewWorks(opened)
	personID := resolvePersonNamed(t, library.NewPeople(opened), "artist-1", "Katupoken")
	credited := func(title string) librarydb.Work {
		work := createWork(t, opened, "music", title)
		if err := queries.SetWorkLeadPerson(t.Context(), librarydb.SetWorkLeadPersonParams{
			LeadPersonID: sql.NullInt64{Int64: personID, Valid: true}, LeadPersonSortName: sql.NullString{String: "Katupoken", Valid: true},
			ID: work.ID,
		}); err != nil {
			t.Fatalf("SetWorkLeadPerson: %v", err)
		}
		return work
	}
	covered := credited("Aretimok")
	credited("Bafu")
	createWork(t, opened, "music", "Cesoge")
	if err := queries.UpsertArtwork(t.Context(), librarydb.UpsertArtworkParams{
		SubjectType: "work", SubjectID: covered.ID, Kind: "cover", CacheKey: "k",
	}); err != nil {
		t.Fatalf("UpsertArtwork: %v", err)
	}

	page, err := works.List(t.Context(), library.WorkFilter{}, library.WorkCursor{}, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]bool{}
	for _, row := range page {
		got[row.CanonicalTitle] = row.ArtistCover
	}
	want := map[string]bool{"Aretimok": false, "Bafu": true, "Cesoge": false}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("artist cover = %v, want %v", got, want)
	}
}

// The Radarr-style orders (M1g 1.4) each walk every album exactly once, one row per page, in the order asked.
func TestListWorks_ProfileAndTagSortsWalkEveryAlbumInOrder(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	titles := []string{"Alpha", "Bravo", "Charlie", "Delta"}
	profiles := []int64{3, 1, 3, 2}
	tags := []string{"jazz", "blues", "jazz", ""}
	ids := make([]int64, len(titles))
	for index, title := range titles {
		work := createWork(t, opened, "music", title)
		ids[index] = work.ID
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			if _, err := q.CreateDesiredItem(ctx, librarydb.CreateDesiredItemParams{
				WorkID: work.ID, CategoryID: 1, ProfileID: profiles[index], Monitored: 1, Dirty: 1,
				SatisfactionKind: "SET", State: "MISSING",
			}); err != nil {
				return err
			}
			if tags[index] == "" {
				return nil
			}
			tag, err := q.CreateTag(ctx, librarydb.CreateTagParams{Name: tags[index], Slug: tags[index] + title, Source: "user"})
			if err != nil {
				return err
			}
			return q.AssignTag(ctx, librarydb.AssignTagParams{TagID: tag.ID, SubjectType: "work", SubjectID: work.ID, Source: "user"})
		})
	}
	walk := func(sort library.WorkSort, direction library.SortDirection) []int64 {
		var seen []int64
		cursor := library.WorkCursor{}
		for {
			page, err := works.List(t.Context(), library.WorkFilter{Sort: sort, Direction: direction}, cursor, 1)
			if err != nil {
				t.Fatalf("List %q: %v", sort, err)
			}
			if len(page) == 0 {
				return seen
			}
			seen = append(seen, page[0].ID)
			cursor = library.WorkCursor{SortNumber: page[0].SortNumber, SortText: page[0].SortText, ID: page[0].ID}
		}
	}
	for _, check := range []struct {
		sort      library.WorkSort
		direction library.SortDirection
		want      []int64
	}{
		{library.WorkSortProfile, library.SortAscending, []int64{ids[1], ids[3], ids[0], ids[2]}},
		{library.WorkSortProfile, library.SortDescending, []int64{ids[2], ids[0], ids[3], ids[1]}},
		{library.WorkSortTag, library.SortAscending, []int64{ids[3], ids[1], ids[0], ids[2]}},
		{library.WorkSortStatus, library.SortAscending, ids},
	} {
		if got := walk(check.sort, check.direction); !slices.Equal(got, check.want) {
			t.Errorf("sort %q %q = %v, want %v", check.sort, check.direction, got, check.want)
		}
	}
}

// A custom filter narrows the Albums list by what the conditions name, and every field the list offers is valid SQL.
func TestWorksList_CustomFilterConditions(t *testing.T) {
	opened, _ := newLibrary(t)
	createWorkWithYear(t, opened, "Alpha", 1999)
	createWorkWithYear(t, opened, "Beta", 2005)
	createWorkWithYear(t, opened, "Gamma", 2012)
	works := library.NewWorks(opened)
	titles := func(raw string) []string {
		t.Helper()
		conditions, err := listfilter.Parse(raw, library.WorkFields)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := works.List(t.Context(), library.WorkFilter{Where: conditions}, library.WorkCursor{}, 50)
		if err != nil {
			t.Fatalf("List %s: %v", raw, err)
		}
		var out []string
		for _, row := range rows {
			out = append(out, row.CanonicalTitle)
		}
		return out
	}
	if got := titles(`[{"field":"year","op":"gt","value":"2000"},{"field":"title","op":"not_contains","value":"gam"}]`); len(got) != 1 || got[0] != "Beta" {
		t.Errorf("year > 2000 and title not containing gam = %v, want Beta", got)
	}
	if got := titles(`[{"field":"title","op":"starts_with","value":"al"}]`); len(got) != 1 || got[0] != "Alpha" {
		t.Errorf("starts with al = %v", got)
	}
	for field, spec := range library.WorkFields {
		value := map[listfilter.Kind]string{listfilter.Text: "x", listfilter.Number: "1", listfilter.Bool: "true", listfilter.Enum: "MISSING"}[spec.Kind]
		op := map[listfilter.Kind]string{listfilter.Text: "contains", listfilter.Number: "ge", listfilter.Bool: "is", listfilter.Enum: "is"}[spec.Kind]
		titles(`[{"field":"` + field + `","op":"` + op + `","value":"` + value + `"}]`)
		if spec.Kind == listfilter.Text {
			titles(`[{"field":"` + field + `","op":"not_contains","value":"x"}]`)
		}
	}
}
