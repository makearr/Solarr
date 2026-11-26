package tv_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/tv"
)

// A merged series previews with its facts, ratings, cast beside series-wide crew, and its episodes grouped by season.
func TestPreviewOf_GroupsEpisodesAndNamesCastAndCrew(t *testing.T) {
	series := tv.Series{
		Title: "Invented Show", Network: "Invented Network", Year: 2020, RuntimeMin: 45, Status: tv.Status("continuing"),
		Ratings: []tv.ProviderRating{{Provider: "tmdb", Score: 8.1, Votes: 90}},
		Credits: []tv.Credit{
			{Name: "Ann Actor", Role: tv.RoleActor, Character: "Lead"},
			{Name: "Guest Star", Role: tv.RoleGuestStar},
			{Name: "Ep Director", Role: tv.RoleDirector, Season: 1, Episode: 2},
			{Name: "Cy Creator", Role: tv.RoleCreator},
		},
		Episodes: []tv.Episode{
			{Season: 2, Number: 1, Title: "Third"},
			{Season: 1, Number: 2, Title: "Second", AirDate: "2020-01-08", RuntimeMin: 44},
			{Season: 1, Number: 1, Title: "First"},
		},
	}
	got := tv.PreviewOf(series)
	want := media.WorkPreview{
		Title: "Invented Show", Subtitle: "Invented Network", Status: "continuing", Year: 2020,
		Facts:   []media.PreviewFact{{Key: media.FactNetwork, Value: "Invented Network"}, {Key: media.FactRuntimeMin, Value: "45"}},
		Ratings: []media.PreviewRating{{Provider: "tmdb", Score: 8.1, Votes: 90}},
		People: []media.PreviewPerson{
			{Name: "Ann Actor", Role: tv.RoleActor, Character: "Lead"},
			{Name: "Cy Creator", Role: tv.RoleCreator},
		},
		Groups: []media.PreviewGroup{
			{Number: 1, Parts: []media.PreviewPart{{Number: "1", Title: "First"}, {Number: "2", Title: "Second", Date: "2020-01-08", DurationSec: 2640}}},
			{Number: 2, Parts: []media.PreviewPart{{Number: "1", Title: "Third"}}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("preview = %+v, want %+v", got, want)
	}
}
