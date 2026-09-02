package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// markPlanned is what the planner does when it has dealt with an item, and is the
// only thing that leaves a row clean for MarkEverythingDirty to find.
func markPlanned(t *testing.T, opened *store.Store, item librarydb.DesiredItem) {
	t.Helper()
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.MarkDesiredItemPlanned(ctx, librarydb.MarkDesiredItemPlannedParams{
			LastPlannedAt: sql.NullInt64{Int64: 1, Valid: true},
			ID:            item.ID, Generation: item.Generation,
		})
	})
}

func dirtyCount(t *testing.T, queries *librarydb.Queries) int {
	t.Helper()
	items, err := queries.ListDirtyDesiredItems(t.Context(), librarydb.ListDirtyDesiredItemsParams{
		AfterID: 0, PageSize: 1000,
	})
	if err != nil {
		t.Fatalf("ListDirtyDesiredItems: %v", err)
	}
	return len(items)
}

func TestMarkEverythingDirty_ReturnsOnlyWhatWasStillClean(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Rupe of Fofu")
	clean := createDesired(t, opened, work.ID, "SINGLE", 1)
	stillDirty := createDesired(t, opened, work.ID, "SINGLE", 1)
	markPlanned(t, opened, clean)

	if got := dirtyCount(t, queries); got != 1 {
		t.Fatalf("dirty before = %d, want 1 (only %d)", got, stillDirty.ID)
	}

	moved, err := library.NewRestored(opened).MarkEverythingDirty(t.Context())
	if err != nil {
		t.Fatalf("MarkEverythingDirty: %v", err)
	}
	// One row moved, but the planner now sees both -- an item already dirty was
	// never going to be missed.
	if moved != 1 {
		t.Errorf("moved = %d, want 1", moved)
	}
	if got := dirtyCount(t, queries); got != 2 {
		t.Errorf("dirty after = %d, want 2", got)
	}
}

// A restored library is bigger than one batch, and the loop has to survive that
// rather than stopping at 200.
func TestMarkEverythingDirty_CrossesTheBatchBoundary(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Rupe of Fofu")
	const total = 205
	for range total {
		markPlanned(t, opened, createDesired(t, opened, work.ID, "SINGLE", 1))
	}
	if got := dirtyCount(t, queries); got != 0 {
		t.Fatalf("dirty before = %d, want 0", got)
	}

	moved, err := library.NewRestored(opened).MarkEverythingDirty(t.Context())
	if err != nil {
		t.Fatalf("MarkEverythingDirty: %v", err)
	}
	if moved != total {
		t.Errorf("moved = %d, want %d", moved, total)
	}
	if got := dirtyCount(t, queries); got != total {
		t.Errorf("dirty after = %d, want %d", got, total)
	}
}

func TestMarkEverythingDirty_OnAnEmptyLibraryDoesNothing(t *testing.T) {
	opened, _ := newLibrary(t)
	moved, err := library.NewRestored(opened).MarkEverythingDirty(t.Context())
	if err != nil {
		t.Fatalf("MarkEverythingDirty: %v", err)
	}
	if moved != 0 {
		t.Errorf("moved = %d on an empty library, want 0", moved)
	}
}

func TestCategoryReferences_CountsRowsPerCategory(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Rupe of Fofu")
	createDesiredInCategory(t, opened, work.ID, 1)
	createDesiredInCategory(t, opened, work.ID, 1)
	createDesiredInCategory(t, opened, work.ID, 7)

	references, err := library.NewRestored(opened).CategoryReferences(t.Context())
	if err != nil {
		t.Fatalf("CategoryReferences: %v", err)
	}
	want := []library.ReferenceCount{{ID: 1, Rows: 2}, {ID: 7, Rows: 1}}
	if len(references) != len(want) {
		t.Fatalf("references = %+v, want %+v", references, want)
	}
	for index, reference := range references {
		if reference != want[index] {
			t.Errorf("references[%d] = %+v, want %+v", index, reference, want[index])
		}
	}
}

func TestRootFolderReferences_CountsPathsPerRoot(t *testing.T) {
	opened, _ := newLibrary(t)
	object := createObject(t, opened, "hash-restore", 1024)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, location := range []struct {
			root int64
			path string
		}{{3, "a/one.flac"}, {3, "a/two.flac"}, {9, "b/three.flac"}} {
			if _, err := q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
				MediaObjectID: object.ID, RootFolderID: location.root,
				RelPath: location.path, LinkKind: "primary", CreatedAt: 1,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	references, err := library.NewRestored(opened).RootFolderReferences(t.Context())
	if err != nil {
		t.Fatalf("RootFolderReferences: %v", err)
	}
	want := []library.ReferenceCount{{ID: 3, Rows: 2}, {ID: 9, Rows: 1}}
	if len(references) != len(want) {
		t.Fatalf("references = %+v, want %+v", references, want)
	}
	for index, reference := range references {
		if reference != want[index] {
			t.Errorf("references[%d] = %+v, want %+v", index, reference, want[index])
		}
	}
}
