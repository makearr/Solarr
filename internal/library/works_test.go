package library_test

import (
	"context"
	"testing"

	"github.com/makearr/MediaNexus/internal/library"
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
