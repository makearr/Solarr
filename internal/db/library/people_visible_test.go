package library_test

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

// peopleNames lists the people a viewer is shown, by name.
func peopleNames(t *testing.T, people library.People, visible *library.Visibility) []string {
	t.Helper()
	rows, err := people.ListPeople(t.Context(), library.PersonFilter{Visible: visible}, library.PersonCursor{}, 50)
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
			if err := people.AddCredit(t.Context(), work.ID, person.ID, "artist", 0); err != nil {
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

	viewer := &library.Visibility{RootFolderIDs: []int64{}, CategoryIDs: []int64{shown}}
	if got, want := peopleNames(t, people, viewer), []string{"In Both", "In The Shown Library", "Only Followed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("people shown to the viewer = %v, want %v", got, want)
	}
	if got := peopleNames(t, people, &library.Visibility{RootFolderIDs: []int64{}, CategoryIDs: []int64{}}); len(got) != 0 {
		t.Errorf("a viewer who may see no library is shown %v, want nobody", got)
	}
	if got := peopleNames(t, people, nil); len(got) != 5 {
		t.Errorf("with no viewer %d people are listed, want all 5", len(got))
	}

	if _, err := people.DetailVisible(t.Context(), onlyHidden, viewer); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("detail of a person known only through a hidden library = %v, want not found", err)
	}
	detail, err := people.DetailVisible(t.Context(), inBoth, viewer)
	if err != nil {
		t.Fatalf("DetailVisible: %v", err)
	}
	var works []int64
	for _, row := range detail.Discography {
		works = append(works, row.ID)
	}
	if want := []int64{bothWorks[0]}; !reflect.DeepEqual(works, want) {
		t.Errorf("works shown on the person's page = %v, want only the visible library's %v", works, want)
	}
	if detail, err := people.DetailVisible(t.Context(), followed.ID, viewer); err != nil || len(detail.Discography) != 0 {
		t.Errorf("a person only followed in a visible library = %+v, %v; want found, with no works", detail.Discography, err)
	}
}
