package book

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
)

func TestPreviewOf_BookCarriesTitleAuthorFactsRatingsAndPeople(t *testing.T) {
	got := PreviewOf(Book{
		Title: "Invented Tale", Overview: "A story.", Year: 1999, Published: "2001-05-01", Language: "en", Publisher: "Made Up Press", RuntimeMin: 600,
		Genres:  []string{"Fantasy"},
		Images:  []Image{{Kind: "backdrop", URL: "http://x/b.jpg"}, {Kind: "poster", URL: "http://x/p.jpg"}},
		Ratings: []Rating{{Provider: "openlibrary", Score: 8.5, Votes: 40}},
		Credits: []Credit{{Name: "Ann Author", Role: RoleAuthor, Image: "http://x/a.jpg"}, {Name: "Nia Narrator", Role: "narrator"}},
		Series:  &Series{Name: "Invented Cycle", Position: "2"},
	})
	want := media.WorkPreview{
		Title: "Invented Tale", Subtitle: "Ann Author", Overview: "A story.", PosterURL: "http://x/p.jpg", Year: 1999, Genres: []string{"Fantasy"},
		Facts: []media.PreviewFact{
			{Key: media.FactPublisher, Value: "Made Up Press"}, {Key: media.FactLanguage, Value: "en"}, {Key: media.FactReleased, Value: "2001-05-01"},
			{Key: media.FactRuntimeMin, Value: "600"}, {Key: media.FactCollection, Value: "Invented Cycle"},
		},
		Ratings: []media.PreviewRating{{Provider: "openlibrary", Score: 8.5, Votes: 40}},
		People:  []media.PreviewPerson{{Name: "Ann Author", Role: RoleAuthor, ImageURL: "http://x/a.jpg"}, {Name: "Nia Narrator", Role: "narrator"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("preview = %+v\nwant %+v", got, want)
	}
}
