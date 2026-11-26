package music

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

func TestPreviewOf_AlbumGroupsTracksByDiscSkipsPregapAndDoublesTheRating(t *testing.T) {
	got := PreviewOf(
		musicbrainz.ReleaseGroup{
			Title: "Invented Album", PrimaryType: "Album", FirstReleaseDate: "2004-06",
			ArtistCredit: []musicbrainz.ArtistCredit{{Name: "The Invented", ArtistName: "Invented Band"}},
			Genres:       []musicbrainz.Genre{{Name: "Folk", Count: 3}},
			Rating:       musicbrainz.Rating{Value: 4, Votes: 12},
		},
		musicbrainz.Release{
			LabelName: "Made Up Records", Date: "2004-06-07",
			Media: []musicbrainz.Medium{
				{Position: 1, Tracks: []musicbrainz.Track{{Position: 0, Optional: true, Title: "Hidden"}, {Position: 1, Title: "Opener", DurationMs: 185000}}},
				{Position: 2, Title: "Bonus", Tracks: []musicbrainz.Track{{Position: 1, Title: "Extra", DurationMs: 61000}}},
			},
		},
	)
	want := media.WorkPreview{
		Title: "Invented Album", Subtitle: "The Invented", Status: "Album", Year: 2004, Genres: []string{"Folk"},
		People:  []media.PreviewPerson{{Name: "Invented Band", Role: "artist"}},
		Ratings: []media.PreviewRating{{Provider: "musicbrainz", Score: 8, Votes: 12}},
		Facts:   []media.PreviewFact{{Key: media.FactPublisher, Value: "Made Up Records"}, {Key: media.FactReleased, Value: "2004-06-07"}},
		Groups: []media.PreviewGroup{
			{Number: 1, Parts: []media.PreviewPart{{Number: "1", Title: "Opener", DurationSec: 185}}},
			{Number: 2, Title: "Bonus", Parts: []media.PreviewPart{{Number: "1", Title: "Extra", DurationSec: 61}}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("preview = %+v\nwant %+v", got, want)
	}
}
