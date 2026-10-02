package library_test

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
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

func TestUserTags_RenameDeleteAndTheProvidersAreLeftAlone(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	work := createWork(t, opened, "music", "Alpha")
	if err := people.AssignGenres(t.Context(), work.ID, []library.GenreLink{{Name: "Rock"}}); err != nil {
		t.Fatalf("AssignGenres: %v", err)
	}
	if err := people.SetUserTags(t.Context(), work.ID, []string{"Road trip", "Sleep"}); err != nil {
		t.Fatalf("SetUserTags: %v", err)
	}
	own, err := people.ListUserTags(t.Context())
	if err != nil || len(own) != 2 || own[0].Name != "Road trip" || own[0].Works != 1 {
		t.Fatalf("own tags = %+v err %v, want Road trip and Sleep, one album each, no genre", own, err)
	}

	if err := people.RenameUserTag(t.Context(), own[1].ID, "road   trip"); !errors.Is(err, library.ErrTagExists) {
		t.Fatalf("rename onto a taken name: %v, want ErrTagExists", err)
	}
	if err := people.RenameUserTag(t.Context(), own[1].ID, "Nap"); err != nil {
		t.Fatalf("RenameUserTag: %v", err)
	}
	all, _ := people.ListTags(t.Context())
	var names []string
	for _, tag := range all {
		names = append(names, tag.Name)
	}
	if want := []string{"Nap", "Road trip", "Rock"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("tags after rename = %v, want %v", names, want)
	}
	genres, _ := librarydb.New(opened.Read()).ListTagNamesForSubjectBySource(t.Context(),
		librarydb.ListTagNamesForSubjectBySourceParams{SubjectType: "work", SubjectID: work.ID, Source: "musicbrainz"})
	if err := people.RenameUserTag(t.Context(), genres[0].TagID, "Metal"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("renaming a provider's genre: %v, want ErrNoRows", err)
	}

	if err := people.DeleteUserTag(t.Context(), own[0].ID); err != nil {
		t.Fatalf("DeleteUserTag: %v", err)
	}
	if remaining, _ := people.UserTags(t.Context(), work.ID); len(remaining) != 1 || remaining[0].Name != "Nap" {
		t.Fatalf("after deleting Road trip the album carries %+v, want Nap only", remaining)
	}
	if err := people.DeleteUserTag(t.Context(), genres[0].TagID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleting a provider's genre: %v, want ErrNoRows", err)
	}
}
