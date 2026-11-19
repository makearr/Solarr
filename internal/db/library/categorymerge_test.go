package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
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

// DB-12: the wanted item the target already holds takes the source's file and finished download instead of losing them.
func TestCategoryMerge_AFoldedDuplicateKeepsItsFileAndDownload(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "audiobook", "In Both")
	source := createDesiredInCategory(t, opened, work.ID, 7)
	target := createDesiredInCategory(t, opened, work.ID, 6)
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "In Both/book.m4b", SizeBytes: 10, QuickHash: "h", FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO desired_item_file (desired_item_id, media_object_id, role, media_ref, added_at) VALUES (?, ?, 'primary', '', 1)`,
			source.ID, registered[0].MediaObjectID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	acquisition, err := library.NewAcquisitions(opened).Create(t.Context(), library.NewAcquisition{
		DesiredItemID: &source.ID, CandidateGUID: "g", CandidateTitle: "t", DownloaderID: 1, Protocol: "usenet", State: "IMPORTED", CreatedAt: 1, UpdatedAt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	merges := library.NewCategoryMerges(opened)
	for run := 0; run < 2; run++ {
		if _, err := merges.Merge(t.Context(), 7, 6); err != nil {
			t.Fatalf("merge %d: %v", run+1, err)
		}
	}
	var files, items, dirty int64
	var ownerOfDownload sql.NullInt64
	row := opened.Read().QueryRowContext(t.Context(), `SELECT
 (SELECT COUNT(*) FROM desired_item_file WHERE desired_item_id = ?1),
 (SELECT COUNT(*) FROM desired_item WHERE work_id = ?2),
 (SELECT dirty FROM desired_item WHERE id = ?1),
 (SELECT desired_item_id FROM acquisition WHERE id = ?3)`, target.ID, work.ID, acquisition.ID)
	if err := row.Scan(&files, &items, &dirty, &ownerOfDownload); err != nil {
		t.Fatal(err)
	}
	if files != 1 || items != 1 || dirty != 1 || !ownerOfDownload.Valid || ownerOfDownload.Int64 != target.ID {
		t.Errorf("files %d, items %d, dirty %d, download owner %v; want the target's item to hold the file and download", files, items, dirty, ownerOfDownload)
	}
}
