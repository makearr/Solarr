package library_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

// peopleNames lists the people a viewer is shown, by name.
func peopleNames(t *testing.T, people library.People, visible []int64) []string {
	t.Helper()
	rows, err := people.List(t.Context(), library.PeopleFilter{Visible: visible}, library.PeopleCursor{}, 50)
	if err != nil {
		t.Fatalf("ListPeople: %v", err)
	}
	names := make([]string, len(rows))
	for index, row := range rows {
		names[index] = row.Name
	}
	return names
}

// A person exists for a viewer only through a library that viewer may see: one that credits them or follows them.
// Someone known only through a hidden library is not listed and not found, and a person in both shows only the
// works of the visible one.
func TestPeople_AreSeenOnlyThroughLibrariesTheViewerMaySee(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	const shown, hidden = int64(1), int64(2)

	credit := func(name string, categories ...int64) (personID int64, workIDs []int64) {
		person := createPerson(t, opened, name)
		for _, categoryID := range categories {
			work := createWork(t, opened, "music", name+" work")
			createDesiredInCategory(t, opened, work.ID, categoryID)
			if err := people.AddLeadCredit(t.Context(), work.ID, person.ID, "artist"); err != nil {
				t.Fatalf("AddCredit: %v", err)
			}
			workIDs = append(workIDs, work.ID)
		}
		return person.ID, workIDs
	}
	credit("In The Shown Library", shown)
	onlyHidden, _ := credit("In The Hidden Library", hidden)
	inBoth, bothWorks := credit("In Both", shown, hidden)
	followed := createPerson(t, opened, "Only Followed")
	if _, err := people.Subscribe(t.Context(), time.Unix(1, 0), followed.ID, 7, library.SubscriptionRequest{
		MediaTypes: []string{"music"}, Roles: []string{"artist"}, CategoryID: shown,
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	createPerson(t, opened, "Credited Nowhere")

	viewer := []int64{shown}
	if got, want := peopleNames(t, people, viewer), []string{"In Both", "In The Shown Library", "Only Followed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("people shown to the viewer = %v, want %v", got, want)
	}
	if got := peopleNames(t, people, []int64{}); len(got) != 0 {
		t.Errorf("a viewer who may see no library is shown %v, want nobody", got)
	}
	if got := peopleNames(t, people, nil); len(got) != 5 {
		t.Errorf("with no viewer %d people are listed, want all 5", len(got))
	}
	if got := peopleNames(t, people, []int64{hidden}); !reflect.DeepEqual(got, []string{"In Both", "In The Hidden Library"}) {
		t.Errorf("people of the other library = %v", got)
	}

	if _, err := people.Get(t.Context(), onlyHidden, viewer); !errors.Is(err, library.ErrPersonNotFound) {
		t.Errorf("a person known only through a hidden library = %v, want not found", err)
	}
	profile, err := people.Get(t.Context(), inBoth, viewer)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if want := []library.PersonLibrary{{CategoryID: shown, Role: "artist", Works: 1}}; !reflect.DeepEqual(profile.Libraries, want) {
		t.Errorf("libraries on the person's page = %+v, want only the visible one %+v", profile.Libraries, want)
	}
	credits, err := people.Credits(t.Context(), inBoth, viewer)
	if err != nil {
		t.Fatalf("Credits: %v", err)
	}
	var works []int64
	for _, credit := range credits {
		works = append(works, credit.ID)
	}
	if want := []int64{bothWorks[0]}; !reflect.DeepEqual(works, want) {
		t.Errorf("works shown on the person's page = %v, want only the visible library's %v", works, want)
	}
	if credits, err := people.Credits(t.Context(), followed.ID, viewer); err != nil || len(credits) != 0 {
		t.Errorf("a person only followed in a visible library = %+v, %v; want no works", credits, err)
	}
	if _, err := people.Get(t.Context(), followed.ID, viewer); err != nil {
		t.Errorf("a person only followed in a visible library is found: %v", err)
	}
}
