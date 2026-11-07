package library_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
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
