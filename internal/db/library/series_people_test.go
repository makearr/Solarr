package library_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

// libraryWithCredits stores the credited series into category 4 and a second, uncredited-by-name series into category 5.
func libraryWithCredits(t *testing.T) (library.SeriesReader, int64) {
	t.Helper()
	opened, _ := newLibrary(t)
	result, err := library.NewSeries(opened).Upsert(t.Context(), seriesAt, creditedSeries())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewDesired(opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 4, ProfileID: 1, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	return library.NewSeriesReader(opened), result.WorkID
}

func TestPeopleListsTheLibrarysCreditedPeopleByRoleAndName(t *testing.T) {
	reader, _ := libraryWithCredits(t)
	all, err := reader.People(t.Context(), library.TVPersonFilter{CategoryID: 4}, library.PersonCursor{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(all))
	for i, person := range all {
		names[i] = person.Name
	}
	if len(all) != 3 || names[0] != "A Person" || names[1] != "B Person" || names[2] != "G Star" {
		t.Errorf("people = %v, want the actor, the creator and the guest star by name", names)
	}
	if all[0].Character != "A Role" || all[0].SeriesCount != 1 || all[0].ImageURL != "https://example.test/a.jpg" || all[0].Roles[0] != "actor" {
		t.Errorf("first = %+v", all[0])
	}

	cast, err := reader.People(t.Context(), library.TVPersonFilter{CategoryID: 4, Roles: []string{"actor"}}, library.PersonCursor{}, 50)
	if err != nil || len(cast) != 1 || cast[0].Name != "A Person" {
		t.Errorf("actors = %+v, err = %v", cast, err)
	}
	prefixed, err := reader.People(t.Context(), library.TVPersonFilter{CategoryID: 4, NamePrefix: "g"}, library.PersonCursor{}, 50)
	if err != nil || len(prefixed) != 1 || prefixed[0].Name != "G Star" {
		t.Errorf("prefix g = %+v, err = %v", prefixed, err)
	}
	next, err := reader.People(t.Context(), library.TVPersonFilter{CategoryID: 4}, library.PersonCursor{SortName: all[0].SortName, ID: all[0].ID}, 1)
	if err != nil || len(next) != 1 || next[0].Name != "B Person" {
		t.Errorf("after the first = %+v, err = %v", next, err)
	}
	other, err := reader.People(t.Context(), library.TVPersonFilter{CategoryID: 5}, library.PersonCursor{}, 50)
	if err != nil || len(other) != 0 {
		t.Errorf("another library sees %+v, err = %v", other, err)
	}
}

func TestPersonShowsCreditsWithTheirEpisodesAndRefusesAStranger(t *testing.T) {
	reader, workID := libraryWithCredits(t)
	all, _ := reader.People(t.Context(), library.TVPersonFilter{CategoryID: 4, Roles: []string{"guest_star"}}, library.PersonCursor{}, 50)
	detail, err := reader.Person(t.Context(), 4, all[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Credits) != 1 || detail.Credits[0].WorkID != workID || detail.Credits[0].Role != "guest_star" || detail.Credits[0].Character != "A Guest" ||
		len(detail.Credits[0].Episodes) != 1 || detail.Credits[0].Episodes[0] != "s01e01" || detail.IDs["tmdb"] != "21" {
		t.Errorf("detail = %+v", detail)
	}
	if _, err := reader.Person(t.Context(), 5, all[0].ID); !errors.Is(err, library.ErrPersonNotFound) {
		t.Errorf("another library: err = %v, want ErrPersonNotFound", err)
	}
}

func TestNetworksCountSeriesAndListThem(t *testing.T) {
	reader, workID := libraryWithCredits(t)
	networks, err := reader.Networks(t.Context(), 4)
	if err != nil || len(networks) != 2 {
		t.Fatalf("networks = %+v, err = %v", networks, err)
	}
	var other library.TVNetworkRow
	for _, network := range networks {
		if network.Name == "Other Net" {
			other = network
		}
	}
	if other.SeriesCount != 1 || other.LogoURL != "https://example.test/n.png" {
		t.Errorf("other = %+v", other)
	}
	network, ids, err := reader.NetworkSeries(t.Context(), 4, other.ID)
	if err != nil || network.Name != "Other Net" || len(ids) != 1 || ids[0] != workID {
		t.Errorf("network = %+v, ids = %v, err = %v", network, ids, err)
	}
	if _, _, err := reader.NetworkSeries(t.Context(), 5, other.ID); !errors.Is(err, library.ErrNetworkNotFound) {
		t.Errorf("another library: err = %v", err)
	}
}
