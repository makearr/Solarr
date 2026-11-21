package library_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

func TestProfileOverride_SetReadClear(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	item := createDesired(t, opened, createWork(t, opened, "music", "Alpha").ID, "SET", 3)

	if _, found, err := desired.ProfileOverride(t.Context(), item.ID); err != nil || found {
		t.Fatalf("fresh item: found=%v err=%v, want no override", found, err)
	}
	profileID := int64(7)
	if err := desired.SetProfileOverride(t.Context(), item.ID, &profileID); err != nil {
		t.Fatalf("SetProfileOverride: %v", err)
	}
	if got, found, err := desired.ProfileOverride(t.Context(), item.ID); err != nil || !found || got != 7 {
		t.Fatalf("after set: got=%d found=%v err=%v, want 7", got, found, err)
	}
	if err := desired.SetProfileOverride(t.Context(), item.ID, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, found, err := desired.ProfileOverride(t.Context(), item.ID); err != nil || found {
		t.Fatalf("after clear: found=%v err=%v, want no override", found, err)
	}
}

func TestTargetOverride_SetReadClear(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	item := createDesired(t, opened, createWork(t, opened, "music", "Alpha").ID, "SET", 3)

	if _, found, err := desired.TargetOverride(t.Context(), item.ID); err != nil || found {
		t.Fatalf("fresh item: found=%v err=%v, want no override", found, err)
	}
	targetID := int64(9)
	if err := desired.SetTargetOverride(t.Context(), item.ID, &targetID); err != nil {
		t.Fatalf("SetTargetOverride: %v", err)
	}
	if got, found, err := desired.TargetOverride(t.Context(), item.ID); err != nil || !found || got != 9 {
		t.Fatalf("after set: got=%d found=%v err=%v, want 9", got, found, err)
	}
	if err := desired.SetTargetOverride(t.Context(), item.ID, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, found, err := desired.TargetOverride(t.Context(), item.ID); err != nil || found {
		t.Fatalf("after clear: found=%v err=%v, want no override", found, err)
	}
}

// A person's tags replace only their own; a name the genre tree knows is that genre, any other a free label.
func TestSetUserTags_ReplacesOnlyTheUsersOwn(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	work := createWork(t, opened, "music", "Alpha")
	if err := people.AssignGenres(t.Context(), work.ID, "music", []library.GenreLink{{Name: "Rock"}}); err != nil {
		t.Fatalf("AssignGenres: %v", err)
	}

	if err := people.SetUserTags(t.Context(), work.ID, "music", []string{"Road trip", "rock"}); err != nil {
		t.Fatalf("SetUserTags: %v", err)
	}
	if err := people.SetUserTags(t.Context(), work.ID, "music", []string{"Road trip", "Sleep", "ROCK"}); err != nil {
		t.Fatalf("SetUserTags again: %v", err)
	}
	if err := people.AssignGenres(t.Context(), work.ID, "music", []library.GenreLink{{Name: "Jazz"}}); err != nil {
		t.Fatalf("a refresh: %v", err)
	}

	user, err := people.UserTags(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("UserTags: %v", err)
	}
	var names []string
	for _, tag := range user {
		names = append(names, tag.Name)
	}
	if want := []string{"Road trip", "Rock", "Sleep"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("user tags = %v, want %v", names, want)
	}
	tree, err := people.TagTree(t.Context(), "*")
	if err != nil {
		t.Fatalf("TagTree: %v", err)
	}
	var every []string
	for _, tag := range tree {
		every = append(every, tag.GenreTree+":"+tag.Name)
	}
	if want := []string{":Road trip", ":Sleep", "music:Jazz", "music:Rock"}; !reflect.DeepEqual(every, want) {
		t.Fatalf("every tag = %v, want %v (one Rock, the genre)", every, want)
	}
}

func TestBlockTag_TakesItOffEveryWorkAndRefusesItAgainUntilUnblocked(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	work := createWork(t, opened, "music", "Aretimok")
	if err := people.SetUserTags(t.Context(), work.ID, "music", []string{"Zorbo", "Keepme"}); err != nil {
		t.Fatalf("SetUserTags: %v", err)
	}
	tags, err := people.UserTags(t.Context(), work.ID)
	if err != nil || len(tags) != 2 {
		t.Fatalf("tags = %v, %v; want both", tags, err)
	}
	var zorbo int64
	for _, tag := range tags {
		if tag.Name == "Zorbo" {
			zorbo = tag.ID
		}
	}

	if err := people.BlockTag(t.Context(), zorbo, 100); err != nil {
		t.Fatalf("BlockTag: %v", err)
	}
	if err := people.SetUserTags(t.Context(), work.ID, "music", []string{"Zorbo", "Keepme"}); err != nil {
		t.Fatalf("SetUserTags after the block: %v", err)
	}
	after, _ := people.UserTags(t.Context(), work.ID)
	if len(after) != 1 || after[0].Name != "Keepme" {
		t.Errorf("tags after the block = %v, want only Keepme", after)
	}
	if _, err := people.CreateTag(t.Context(), "zorbo", "", 0); !errors.Is(err, library.ErrTagBlocked) {
		t.Errorf("CreateTag of a blocked name = %v, want ErrTagBlocked", err)
	}
	blocked, err := people.BlockedTags(t.Context())
	if err != nil || len(blocked) != 1 || blocked[0].Name != "Zorbo" {
		t.Fatalf("blocked = %v, %v", blocked, err)
	}

	if err := people.UnblockTag(t.Context(), "Zorbo", blocked[0].GenreTree); err != nil {
		t.Fatalf("UnblockTag: %v", err)
	}
	if _, err := people.CreateTag(t.Context(), "Zorbo", "", 0); err != nil {
		t.Errorf("CreateTag after unblocking: %v", err)
	}
}

func TestProfileOverrideForWork_CoversEveryItemOfThatWorkOnly(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "Alpha")
	first := createDesired(t, opened, work.ID, "SET", 3)
	other := createDesired(t, opened, createWork(t, opened, "music", "Beta").ID, "SET", 3)

	if err := desired.SetProfileOverrideForWork(t.Context(), work.ID, 7); err != nil {
		t.Fatal(err)
	}
	if got, found, err := desired.ProfileOverride(t.Context(), first.ID); err != nil || !found || got != 7 {
		t.Errorf("the work's item: got=%d found=%v err=%v, want 7", got, found, err)
	}
	if _, found, err := desired.ProfileOverride(t.Context(), other.ID); err != nil || found {
		t.Errorf("another work's item: found=%v err=%v, want none", found, err)
	}
}

func TestOverrides_BothChoicesInOneRead(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	item := createDesired(t, opened, createWork(t, opened, "music", "Alpha").ID, "SET", 3)
	if profile, target, err := desired.Overrides(t.Context(), item.ID); err != nil || profile != 0 || target != 0 {
		t.Fatalf("fresh item: %d %d %v, want none", profile, target, err)
	}
	profileID, targetID := int64(7), int64(9)
	if err := desired.SetProfileOverride(t.Context(), item.ID, &profileID); err != nil {
		t.Fatal(err)
	}
	if err := desired.SetTargetOverride(t.Context(), item.ID, &targetID); err != nil {
		t.Fatal(err)
	}
	if profile, target, err := desired.Overrides(t.Context(), item.ID); err != nil || profile != 7 || target != 9 {
		t.Fatalf("overrides = %d %d %v, want 7 and 9", profile, target, err)
	}
}

func TestPinOffDefaultProfiles_PinsOnlyUnpinnedItemsOfOtherProfiles(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	workID := createWork(t, opened, "music", "Alpha").ID
	onDefault := createDesired(t, opened, workID, "SINGLE", 1) // stored profile 1
	other := createDesiredWithProfile(t, opened, createWork(t, opened, "music", "Beta").ID, 1, 5)
	already := createDesiredWithProfile(t, opened, createWork(t, opened, "music", "Gamma").ID, 1, 6)
	elsewhere := createDesiredWithProfile(t, opened, createWork(t, opened, "music", "Delta").ID, 2, 7)
	alreadyOwn := int64(9)
	if err := desired.SetProfileOverride(t.Context(), already.ID, &alreadyOwn); err != nil {
		t.Fatalf("SetProfileOverride: %v", err)
	}
	pinned, err := desired.PinOffDefaultProfiles(t.Context(), 1, 1)
	if err != nil || pinned != 1 {
		t.Fatalf("pinned = %d, %v; want 1", pinned, err)
	}
	for _, want := range []struct {
		id    int64
		found bool
		got   int64
	}{{onDefault.ID, false, 0}, {other.ID, true, 5}, {already.ID, true, 9}, {elsewhere.ID, false, 0}} {
		got, found, err := desired.ProfileOverride(t.Context(), want.id)
		if err != nil || found != want.found || got != want.got {
			t.Errorf("item %d: override = %d, %v, %v; want %d, %v", want.id, got, found, err, want.got, want.found)
		}
	}
}

func createDesiredWithProfile(t *testing.T, opened *store.Store, workID, categoryID, profileID int64) librarydb.DesiredItem {
	t.Helper()
	var item librarydb.DesiredItem
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateDesiredItem(ctx, librarydb.CreateDesiredItemParams{
			WorkID: workID, CategoryID: categoryID, ProfileID: profileID, Monitored: 1, Dirty: 1,
			SatisfactionKind: "SINGLE", RequiredCount: sql.NullInt64{Int64: 1, Valid: true}, State: "MISSING",
		})
		item = row
		return err
	})
	return item
}
