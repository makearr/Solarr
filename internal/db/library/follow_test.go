package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

// One person can be followed into a music and a TV library at once; each follow is its own, and the person stays
// monitored until the last one goes.
func TestFollows_AreOnePerLibrary(t *testing.T) {
	opened, _ := newLibrary(t)
	subscriptions := library.NewSubscriptions(opened)
	people := library.NewPeople(opened)
	person := createPerson(t, opened, "Plays And Sings")
	subject := library.Subject{Type: "person", ID: person.ID}
	const user = int64(7)

	music, err := subscriptions.Follow(t.Context(), firstSeen, subject, user, library.FollowRequest{
		CategoryID: 1, MediaType: "music", FiltersJSON: `{"include_primary_types":["Album"]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	tv, err := subscriptions.Follow(t.Context(), firstSeen, subject, user, library.FollowRequest{
		CategoryID: 2, MediaType: "tv", Roles: []string{"actor"}, BackCatalogue: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if music.ID == tv.ID || music.CategoryID != 1 || tv.CategoryID != 2 || !tv.MonitorBackCatalogue || tv.Roles[0] != "actor" {
		t.Fatalf("follows = %+v and %+v; want two, one per library", music, tv)
	}
	again, err := subscriptions.Follow(t.Context(), firstSeen, subject, user, library.FollowRequest{CategoryID: 2, MediaType: "tv"})
	if err != nil || again.ID != tv.ID || again.MonitorBackCatalogue {
		t.Errorf("following again = %+v, %v; want the same follow, changed", again, err)
	}
	follows, err := subscriptions.FollowsOf(t.Context(), subject, user)
	if err != nil || len(follows) != 2 {
		t.Fatalf("FollowsOf = %+v, %v; want both", follows, err)
	}

	if err := subscriptions.Unfollow(t.Context(), subject, user, 2); err != nil {
		t.Fatal(err)
	}
	if row, _ := people.Person(t.Context(), person.ID); row.Monitored != 1 {
		t.Error("unfollowed in one library, the person stops being monitored though another follow remains")
	}
	if err := subscriptions.Unfollow(t.Context(), subject, user, 1); err != nil {
		t.Fatal(err)
	}
	if row, _ := people.Person(t.Context(), person.ID); row.Monitored != 0 {
		t.Error("with no follow left the person is still monitored")
	}
}
