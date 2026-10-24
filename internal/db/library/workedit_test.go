package library_test

import (
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
