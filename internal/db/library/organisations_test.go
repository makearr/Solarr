package library_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

// A network is found through the libraries whose series it airs, and nowhere else.
func TestOrganisations_AreSeenThroughTheLibrariesTheirWorksAreIn(t *testing.T) {
	opened, _ := newLibrary(t)
	result, err := library.NewSeries(opened).Upsert(t.Context(), seriesAt, creditedSeries())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewDesired(opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{
		WorkID: result.WorkID, MediaRef: "s01", CategoryID: 4, ProfileID: 1,
	}); err != nil {
		t.Fatal(err)
	}
	organisations := library.NewOrganisations(opened)

	listed, err := organisations.List(t.Context(), library.OrganisationFilter{Visible: []int64{4}, Category: 4}, library.PeopleCursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range listed {
		if entry.Works != 1 || !slices.Equal(entry.Roles, []string{"network"}) {
			t.Errorf("entry %+v: want one series, as a network", entry)
		}
		names = append(names, entry.Name)
	}
	if want := []string{"Example Net", "Other Net"}; !slices.Equal(names, want) {
		t.Errorf("networks of library 4 = %v, want %v", names, want)
	}
	if hidden, err := organisations.List(t.Context(), library.OrganisationFilter{Visible: []int64{5}}, library.PeopleCursor{}, 10); err != nil || len(hidden) != 0 {
		t.Errorf("networks for a viewer of another library = %+v, %v; want none", hidden, err)
	}

	profile, err := organisations.Get(t.Context(), listed[0].ID, []int64{4})
	if err != nil || profile.Name != "Example Net" || len(profile.Libraries) != 1 || profile.Libraries[0].CategoryID != 4 {
		t.Errorf("Get = %+v, %v; want the network in library 4", profile, err)
	}
	if _, err := organisations.Get(t.Context(), listed[0].ID, []int64{5}); !errors.Is(err, library.ErrOrganisationNotFound) {
		t.Errorf("Get for a viewer of another library = %v, want not found", err)
	}
	credits, err := organisations.Credits(t.Context(), listed[0].ID, []int64{4})
	if err != nil || len(credits) != 1 || credits[0].ID != result.WorkID || credits[0].Role != "network" {
		t.Errorf("Credits = %+v, %v; want the one series", credits, err)
	}
}
