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

// An album's label is linked once, found again by its id, and a work answered for is not asked again.
func TestAWorksLabelIsLinkedByItsIDAndAskedOnce(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	organisations := library.NewOrganisations(opened)
	first := newWorkForCredits(t, identity, queries, "rg-1")
	second := newWorkForCredits(t, identity, queries, "rg-2")
	createDesiredInCategory(t, opened, first, 2)
	createDesiredInCategory(t, opened, second, 2)

	asked, err := organisations.WorksToAsk(t.Context(), "music", "musicbrainz:release-group", "label", "musicbrainz", 0, 10)
	if err != nil || len(asked) != 2 {
		t.Fatalf("works to ask = %+v, %v; want both", asked, err)
	}
	link := &library.OrganisationLink{Kind: "label", Name: "Mute", Provider: "musicbrainz:label", ExternalID: "label-1"}
	id, created, err := organisations.SetWorkOrganisation(t.Context(), seriesAt, first, "label", "musicbrainz", link)
	if err != nil || !created {
		t.Fatalf("first link = %d, created %v, %v", id, created, err)
	}
	renamed := &library.OrganisationLink{Kind: "label", Name: "Mute Records", Provider: "musicbrainz:label", ExternalID: "label-1"}
	again, created, err := organisations.SetWorkOrganisation(t.Context(), seriesAt, second, "label", "musicbrainz", renamed)
	if err != nil || created || again != id {
		t.Errorf("second link = %d, created %v, %v; want the same label by its id", again, created, err)
	}
	if err := organisations.FillDetails(t.Context(), id, library.OrganisationDetails{Country: "GB", Type: "Original Production", Founded: "1978"}); err != nil {
		t.Fatal(err)
	}
	listed, err := organisations.List(t.Context(), library.OrganisationFilter{Category: 2, Visible: []int64{2}}, library.PeopleCursor{}, 10)
	if err != nil || len(listed) != 1 || listed[0].Works != 2 || listed[0].Country != "GB" || !slices.Equal(listed[0].Roles, []string{"label"}) {
		t.Errorf("labels of library 2 = %+v, %v; want Mute with both albums", listed, err)
	}
	if asked, _ := organisations.WorksToAsk(t.Context(), "music", "musicbrainz:release-group", "label", "musicbrainz", 0, 10); len(asked) != 0 {
		t.Errorf("after both were answered, works to ask = %+v", asked)
	}

	if _, _, err := organisations.SetWorkOrganisation(t.Context(), seriesAt, second, "label", "musicbrainz", nil); err != nil {
		t.Fatal(err)
	}
	listed, _ = organisations.List(t.Context(), library.OrganisationFilter{Category: 2, Visible: []int64{2}}, library.PeopleCursor{}, 10)
	if len(listed) != 1 || listed[0].Works != 1 {
		t.Errorf("after the second album was answered with no label, labels = %+v; want Mute with one", listed)
	}
}
