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

func TestSetUserTags_ReplacesOnlyTheUsersOwn(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	work := createWork(t, opened, "music", "Alpha")
	if err := people.AssignGenres(t.Context(), work.ID, []library.GenreLink{{Name: "Rock"}}); err != nil {
		t.Fatalf("AssignGenres: %v", err)
	}

	if err := people.SetUserTags(t.Context(), work.ID, []string{"Road trip", "rock"}); err != nil {
		t.Fatalf("SetUserTags: %v", err)
	}
	if err := people.SetUserTags(t.Context(), work.ID, []string{"Road trip", "Sleep"}); err != nil {
		t.Fatalf("SetUserTags again: %v", err)
	}

	user, err := people.UserTags(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("UserTags: %v", err)
	}
	var names []string
	for _, tag := range user {
		names = append(names, tag.Name)
	}
	if want := []string{"Road trip", "Sleep"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("user tags = %v, want %v", names, want)
	}
	all, err := people.ListTags(t.Context())
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	var every []string
	for _, tag := range all {
		every = append(every, tag.Name)
	}
	if want := []string{"Road trip", "Rock", "Sleep"}; !reflect.DeepEqual(every, want) {
		t.Fatalf("every tag = %v, want %v", every, want)
	}
}
