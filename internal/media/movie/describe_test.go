package movie_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/movie"
)

// A merged film previews with the first release date it has, a capped cast and the writer and director.
func TestPreviewOf_FactsCastAndCrew(t *testing.T) {
	film := movie.Movie{
		Title: "Invented Film", Studio: "Invented Pictures", Year: 2019, RuntimeMin: 101, Digital: "2019-06-01", InCinemas: "2019-03-01",
		Ratings: []movie.Rating{{Provider: "tmdb", Score: 7.2, Votes: 10}},
		Credits: []movie.Credit{
			{Name: "Ann Actor", Role: movie.RoleActor, Character: "Lead"},
			{Name: "Di Rector", Role: movie.RoleDirector},
			{Name: "Cam Era", Role: movie.RoleCinematographer},
		},
	}
	want := media.WorkPreview{
		Title: "Invented Film", Subtitle: "Invented Pictures", Year: 2019,
		Facts: []media.PreviewFact{
			{Key: media.FactStudio, Value: "Invented Pictures"}, {Key: media.FactRuntimeMin, Value: "101"}, {Key: media.FactReleased, Value: "2019-06-01"},
		},
		Ratings: []media.PreviewRating{{Provider: "tmdb", Score: 7.2, Votes: 10}},
		People:  []media.PreviewPerson{{Name: "Ann Actor", Role: movie.RoleActor, Character: "Lead"}, {Name: "Di Rector", Role: movie.RoleDirector}},
	}
	if got := movie.PreviewOf(film); !reflect.DeepEqual(got, want) {
		t.Fatalf("preview = %+v, want %+v", got, want)
	}
}
