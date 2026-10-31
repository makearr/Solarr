package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

func TestAPersonEditedByHandKeepsItsNameAndAttributesThroughARefresh(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	id, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "stashdb:performer", ExternalID: "p-1", Person: library.PersonIdentity{Name: "Ada Vale", Country: "GB"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := people.SetExtras(t.Context(), id, library.PersonExtras{Attributes: library.PersonAttributes{HeightCm: 170, HairColor: "Blonde", Status: library.PersonActive}}); err != nil {
		t.Fatal(err)
	}
	edit := library.PersonEdit{Name: "Ada Vane", Gender: "Trans woman", Attributes: library.PersonAttributes{HeightCm: 175, WeightKg: 60}}
	if err := people.Edit(t.Context(), id, edit); err != nil {
		t.Fatal(err)
	}
	if err := people.BackfillDetails(t.Context(), id, library.PersonIdentity{Name: "Ada Vale"}); err != nil {
		t.Fatal(err)
	}
	profile, err := people.Get(t.Context(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := library.PersonAttributes{HeightCm: 175, WeightKg: 60, HairColor: "Blonde", Status: library.PersonActive}
	if profile.Name != "Ada Vane" || profile.Gender != "Trans woman" || profile.Attributes != want || !contains(profile.Aliases, "Ada Vale") {
		t.Errorf("profile = %q %q %+v aliases %v; want the hand-set name and gender, hand over provider attributes, the old name as an alias", profile.Name, profile.Gender, profile.Attributes, profile.Aliases)
	}

	for _, condition := range []string{
		`[{"field":"height","op":"gt","value":"172"},{"field":"hair_colour","op":"is","value":"blonde"},{"field":"status","op":"is","value":"active"},{"field":"gender","op":"is","value":"trans woman"},{"field":"nationality","op":"is","value":"gb"}]`,
	} {
		conditions, err := listfilter.Parse(condition, library.PersonFields(nil))
		if err != nil {
			t.Fatal(err)
		}
		listed, err := people.List(t.Context(), library.PeopleFilter{Where: conditions}, library.PeopleCursor{}, 10)
		if err != nil || len(listed) != 1 || listed[0].ID != id {
			t.Errorf("filter %s = %v, %v; want the person", condition, listed, err)
		}
	}
	conditions, _ := listfilter.Parse(`[{"field":"status","op":"is","value":"deceased"}]`, library.PersonFields(nil))
	if listed, err := people.List(t.Context(), library.PeopleFilter{Where: conditions}, library.PeopleCursor{}, 10); err != nil || len(listed) != 0 {
		t.Errorf("deceased filter = %v, %v; want nobody", listed, err)
	}
}

func TestMergePeopleMovesTheLoserIntoTheWinnerAsAnAlias(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	winner, _ := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{Provider: "stashdb:performer", ExternalID: "w", Person: library.PersonIdentity{Name: "Ada Vale"}})
	loser, _ := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{Provider: "theporndb:performer", ExternalID: "l", Person: library.PersonIdentity{Name: "A. Vale"}})
	if err := people.MergePeople(t.Context(), loser, loser); err == nil {
		t.Error("merging a person into themselves succeeded")
	}
	if err := people.MergePeople(t.Context(), loser, winner); err != nil {
		t.Fatal(err)
	}
	profile, err := people.Get(t.Context(), winner, nil)
	if err != nil || !contains(profile.Aliases, "A. Vale") || len(profile.IDs) != 2 {
		t.Errorf("winner = %+v, %v; want the loser's name as an alias and both provider ids", profile, err)
	}
	if _, err := people.Get(t.Context(), loser, nil); err == nil {
		t.Error("the loser is still there")
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
