package library_test

import (
	"fmt"
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

func TestUsage_RepointMovesEveryItemOfAProfileAndATarget(t *testing.T) {
	opened, _ := newLibrary(t)
	usage := library.NewUsage(opened)
	desired := library.NewDesired(opened)
	var items []int64
	for i := range 450 { // more than one chunk
		item := createDesiredWithProfile(t, opened, createWork(t, opened, "music", fmt.Sprintf("Work %d", i)).ID, 1, 5)
		items = append(items, item.ID)
	}
	own, target := int64(5), int64(9)
	if err := desired.SetProfileOverride(t.Context(), items[0], &own); err != nil {
		t.Fatal(err)
	}
	if err := desired.SetTargetOverride(t.Context(), items[0], &target); err != nil {
		t.Fatal(err)
	}
	if err := usage.RepointProfile(t.Context(), 5, 6); err != nil {
		t.Fatalf("RepointProfile: %v", err)
	}
	if err := usage.RepointTarget(t.Context(), 9, 10); err != nil {
		t.Fatalf("RepointTarget: %v", err)
	}
	if got, err := usage.ItemsPinnedToProfile(t.Context(), 5); err != nil || got != 0 {
		t.Errorf("profile 5 still named %d times, %v", got, err)
	}
	if got, err := usage.ItemsPinnedToProfile(t.Context(), 6); err != nil || got != 451 {
		t.Errorf("profile 6 named %d times, %v; want 451", got, err)
	}
	if got, err := usage.ItemsPinnedToTarget(t.Context(), 10); err != nil || got != 1 {
		t.Errorf("target 10 named %d times, %v; want 1", got, err)
	}
}
