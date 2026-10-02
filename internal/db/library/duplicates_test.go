package library_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

func TestDuplicates_GroupsSameSizeAndSampleAcrossFoldersAndLeavesOtherRootsOut(t *testing.T) {
	opened, _ := newLibrary(t)
	files := []struct {
		hash string
		size int64
		root int64
		path string
	}{
		{"same", 100, 1, "A/01.flac"},
		{"same", 100, 1, "Copy of A/01.flac"},
		{"same", 100, 2, "elsewhere/01.flac"}, // another library: not counted for root 1
		{"other", 100, 1, "B/01.flac"},        // same size, different content
		{"big", 900, 1, "C/01.flac"},
		{"big", 900, 1, "D/01.flac"},
	}
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, file := range files {
			id, err := q.CreateMediaObject(ctx, librarydb.CreateMediaObjectParams{SizeBytes: file.size, QuickHash: file.hash, FirstSeenAt: 1})
			if err != nil {
				return err
			}
			if _, err := q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
				MediaObjectID: id, RootFolderID: file.root, RelPath: file.path, LinkKind: "primary", IsPrimary: 1, CreatedAt: 1,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	groups, err := library.NewDuplicates(opened).Groups(t.Context(), []int64{1}, 50)
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	var got [][2]any
	for _, group := range groups {
		var paths []string
		for _, copy := range group.Copies {
			for _, path := range copy.Paths {
				paths = append(paths, path.RelPath)
			}
		}
		got = append(got, [2]any{group.Reclaimable(), paths})
	}
	want := [][2]any{{int64(900), []string{"C/01.flac", "D/01.flac"}}, {int64(100), []string{"A/01.flac", "Copy of A/01.flac"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %v, want %v (largest reclaimable first)", got, want)
	}
}
