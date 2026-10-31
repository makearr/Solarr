package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

// Merging a library files its wanted items under the target, leaves a work both hold once, and clears what was the
// source's rollups.
func TestCategoryMerge_MovesTheWantedItemsAndKeepsAWorkBothHoldOnce(t *testing.T) {
	opened, _ := newLibrary(t)
	only := createWork(t, opened, "audiobook", "Only In The Source")
	both := createWork(t, opened, "audiobook", "In Both")
	stay := createWork(t, opened, "audiobook", "Already In The Target")
	createDesiredInCategory(t, opened, only.ID, 7)
	createDesiredInCategory(t, opened, both.ID, 7)
	createDesiredInCategory(t, opened, both.ID, 6)
	createDesiredInCategory(t, opened, stay.ID, 6)

	moved, err := library.NewCategoryMerges(opened).Merge(t.Context(), 7, 6)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 2 {
		t.Errorf("moved = %d, want the two wanted items the source held", moved)
	}
	counts := map[int64]int{}
	rows, err := opened.Read().QueryContext(t.Context(), "SELECT category_id, COUNT(*) FROM desired_item GROUP BY category_id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var category int64
		var count int
		if err := rows.Scan(&category, &count); err != nil {
			t.Fatal(err)
		}
		counts[category] = count
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if counts[7] != 0 || counts[6] != 3 {
		t.Errorf("wanted items per library = %v, want all three works in library 6 and none in 7", counts)
	}
	if _, err := library.NewCategoryMerges(opened).Merge(t.Context(), 6, 6); err == nil {
		t.Error("a library was merged into itself")
	}
}
