package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestUsage_CountsItemsByStoredProfileAndByOverride(t *testing.T) {
	opened, _ := newLibrary(t)
	usage := library.NewUsage(opened)
	desired := library.NewDesired(opened)
	item := createDesired(t, opened, createWork(t, opened, "music", "Alpha").ID, "SET", 3)
	override, target := int64(7), int64(9)
	if err := desired.SetProfileOverride(t.Context(), item.ID, &override); err != nil {
		t.Fatal(err)
	}
	if err := desired.SetTargetOverride(t.Context(), item.ID, &target); err != nil {
		t.Fatal(err)
	}
	for profile, want := range map[int64]int64{1: 1, 7: 1, 8: 0} {
		if got, err := usage.ItemsPinnedToProfile(t.Context(), profile); err != nil || got != want {
			t.Errorf("profile %d: %d, %v; want %d", profile, got, err, want)
		}
	}
	if got, err := usage.ItemsPinnedToTarget(t.Context(), 9); err != nil || got != 1 {
		t.Errorf("target 9: %d, %v; want 1", got, err)
	}
	files, recycled, queued, err := usage.RootFolderRows(t.Context(), 5)
	if err != nil || files != 0 || recycled != 0 || queued != 0 {
		t.Errorf("empty root: %d %d %d %v", files, recycled, queued, err)
	}
	if got, err := usage.CategoryFilesOnRoot(t.Context(), 1, 5); err != nil || got != 0 {
		t.Errorf("files of a library on a root: %d, %v", got, err)
	}
}
