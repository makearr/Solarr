package library_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

// tvRoles are the roles a TV library's custom filter offers.
var tvRoles = []string{"actor", "guest_star", "creator", "showrunner", "director", "writer", "executive_producer", "producer", "composer"}

// libraryWithCredits stores the credited series into category 4.
func libraryWithCredits(t *testing.T) (library.People, library.Organisations, int64) {
	t.Helper()
	opened, _ := newLibrary(t)
	result, err := library.NewSeries(opened).Upsert(t.Context(), seriesAt, creditedSeries())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewDesired(opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 4, ProfileID: 1, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	return library.NewPeople(opened), library.NewOrganisations(opened), result.WorkID
}

func names(entries []library.PersonEntry) []string {
	var out []string
	for _, entry := range entries {
		out = append(out, entry.Name)
	}
	return out
}

func TestTVPeopleListTheLibrarysCreditedPeopleByRoleAndName(t *testing.T) {
	people, _, _ := libraryWithCredits(t)
	list := func(filter library.PeopleFilter, after library.PeopleCursor, size int) []library.PersonEntry {
		t.Helper()
		filter.Category, filter.Visible = 4, []int64{4, 5}
		rows, err := people.List(t.Context(), filter, after, size)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	all := list(library.PeopleFilter{}, library.PeopleCursor{}, 50)
	if got := names(all); !slices.Equal(got, []string{"A Person", "B Person", "G Star"}) {
		t.Errorf("people = %v, want the actor, the creator and the guest star by name", got)
	}
	if all[0].Character != "A Role" || all[0].Works != 1 || all[0].ImageURL != "https://example.test/a.jpg" || !slices.Equal(all[0].Roles, []string{"actor"}) {
		t.Errorf("first = %+v", all[0])
	}
	if got := names(list(library.PeopleFilter{Roles: []string{"actor"}}, library.PeopleCursor{}, 50)); !slices.Equal(got, []string{"A Person"}) {
		t.Errorf("actors = %v", got)
	}
	if got := names(list(library.PeopleFilter{NameContains: "star"}, library.PeopleCursor{}, 50)); !slices.Equal(got, []string{"G Star"}) {
		t.Errorf("contains star = %v", got)
	}
	if got := names(list(library.PeopleFilter{}, library.PeopleCursor{Name: all[0].SortName, ID: all[0].ID, Set: true}, 1)); !slices.Equal(got, []string{"B Person"}) {
		t.Errorf("after the first = %v", got)
	}
	if other, err := people.List(t.Context(), library.PeopleFilter{Category: 5, Visible: []int64{4, 5}}, library.PeopleCursor{}, 50); err != nil || len(other) != 0 {
		t.Errorf("another library sees %v, err = %v", names(other), err)
	}
	if got := names(list(library.PeopleFilter{Desc: true}, library.PeopleCursor{}, 50)); !slices.Equal(got, []string{"G Star", "B Person", "A Person"}) {
		t.Errorf("name descending = %v", got)
	}
}

func TestTVPeopleFilterByConditions(t *testing.T) {
	people, _, _ := libraryWithCredits(t)
	where := func(raw string) []string {
		t.Helper()
		conditions, err := listfilter.Parse(raw, library.PersonFields(tvRoles))
		if err != nil {
			t.Fatal(err)
		}
		rows, err := people.List(t.Context(), library.PeopleFilter{Category: 4, Visible: []int64{4}, Where: conditions}, library.PeopleCursor{}, 50)
		if err != nil {
			t.Fatal(err)
		}
		return names(rows)
	}
	if got := where(`[{"field":"role","op":"is","value":"actor"}]`); !slices.Equal(got, []string{"A Person"}) {
		t.Errorf("role is actor = %v", got)
	}
	if got := where(`[{"field":"name","op":"contains","value":"star"}]`); !slices.Equal(got, []string{"G Star"}) {
		t.Errorf("name contains star = %v", got)
	}
	if got := where(`[{"field":"works","op":"ge","value":"2"}]`); len(got) != 0 {
		t.Errorf("works at least 2 = %v, want none", got)
	}
	if got := where(`[{"field":"photo","op":"is","value":"true"},{"field":"works","op":"ge","value":"1"}]`); !slices.Equal(got, []string{"A Person"}) {
		t.Errorf("photo and a series = %v, want A Person (the only one with a photo)", got)
	}
}

func TestTVPersonCreditsCarryTheirEpisodesAndAStrangerIsNotFound(t *testing.T) {
	people, _, workID := libraryWithCredits(t)
	stars, err := people.List(t.Context(), library.PeopleFilter{Category: 4, Visible: []int64{4}, Roles: []string{"guest_star"}}, library.PeopleCursor{}, 50)
	if err != nil || len(stars) != 1 {
		t.Fatalf("guest stars = %v, %v", names(stars), err)
	}
	credits, err := people.Credits(t.Context(), stars[0].ID, []int64{4})
	if err != nil || len(credits) != 1 || credits[0].ID != workID || credits[0].Role != "guest_star" || credits[0].Character != "A Guest" ||
		!slices.Equal(credits[0].Parts, []string{"s01e01"}) {
		t.Errorf("credits = %+v, %v", credits, err)
	}
	profile, err := people.Get(t.Context(), stars[0].ID, []int64{4})
	if err != nil || profile.IDs["tmdb"] != "21" {
		t.Errorf("profile = %+v, %v", profile, err)
	}
	if _, err := people.Get(t.Context(), stars[0].ID, []int64{5}); !errors.Is(err, library.ErrPersonNotFound) {
		t.Errorf("for a viewer of another library: err = %v, want ErrPersonNotFound", err)
	}
}

func TestTVNetworksCountSeriesAndCarryTheirLogo(t *testing.T) {
	_, organisations, workID := libraryWithCredits(t)
	networks, err := organisations.List(t.Context(), library.OrganisationFilter{Category: 4, Visible: []int64{4}}, library.PeopleCursor{}, 50)
	if err != nil || len(networks) != 2 {
		t.Fatalf("networks = %+v, err = %v", networks, err)
	}
	index := slices.IndexFunc(networks, func(network library.OrganisationEntry) bool { return network.Name == "Other Net" })
	if index < 0 || networks[index].Works != 1 || networks[index].LogoURL != "https://example.test/n.png" {
		t.Fatalf("other = %+v", networks)
	}
	credits, err := organisations.Credits(t.Context(), networks[index].ID, []int64{4})
	if err != nil || len(credits) != 1 || credits[0].ID != workID {
		t.Errorf("series on the network = %+v, %v", credits, err)
	}
}
