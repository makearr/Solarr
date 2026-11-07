package library_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestPlanShowFilmLinks_ANamedCollectionAndATitledFilmAreTheShowsMovies(t *testing.T) {
	collections := []library.NamedCollection{
		{ID: 500, Name: "Sex and the City Collection"}, {ID: 501, Name: "The Mummy Collection"}, {ID: 502, Name: "Battlestar Galactica Collection"},
	}
	loose := []library.NamedWork{{WorkID: 7, Title: "Veronica Mars: The Movie"}, {WorkID: 8, Title: "Mars Attacks!"}, {WorkID: 9, Title: "Firefly"}}
	series := []library.NamedWork{
		{WorkID: 1, Title: "Sex and the City"}, {WorkID: 2, Title: "Veronica Mars"}, {WorkID: 3, Title: "Firefly"},
		{WorkID: 4, Title: "Battlestar Galactica"}, {WorkID: 5, Title: "Battlestar Galactica"},
	}
	got := library.PlanShowFilmLinks(collections, loose, series)
	want := []library.PlannedShowLink{
		{FilmCollectionID: 500, FilmName: "Sex and the City Collection", SeriesWorkID: 1, SeriesTitle: "Sex and the City"},
		{FilmWorkID: 7, FilmName: "Veronica Mars", SeriesWorkID: 2, SeriesTitle: "Veronica Mars"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("planned %+v\nwant %+v\n(the Mummy has no show, Battlestar has two, Firefly the film is the show's own title)", got, want)
	}
}
