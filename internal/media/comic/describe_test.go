package comic

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
)

func TestPreviewOf_SeriesHasItsPartsInOneGroup(t *testing.T) {
	got := PreviewOf(Series{
		Title: "Invented Saga", Publisher: "Made Up Comics", Overview: "Heroes.", Status: "Ongoing", Cover: "http://x/c.jpg", Year: 2010, OriginalLanguage: "en",
		Genres:  []string{"Adventure"},
		Credits: []Credit{{Name: "Wren Writer", Role: "writer"}},
		Parts:   []Part{{Number: "1", Title: "Beginnings", CoverDate: "2010-03-01"}, {Number: "1.5", CoverDate: "2010-04-01"}},
	})
	want := media.WorkPreview{
		Title: "Invented Saga", Subtitle: "Made Up Comics", Overview: "Heroes.", PosterURL: "http://x/c.jpg", Status: "Ongoing", Year: 2010, Genres: []string{"Adventure"},
		Facts:  []media.PreviewFact{{Key: media.FactPublisher, Value: "Made Up Comics"}, {Key: media.FactLanguage, Value: "en"}},
		People: []media.PreviewPerson{{Name: "Wren Writer", Role: "writer"}},
		Groups: []media.PreviewGroup{{Number: 1, Parts: []media.PreviewPart{{Number: "1", Title: "Beginnings", Date: "2010-03-01"}, {Number: "1.5", Date: "2010-04-01"}}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("preview = %+v\nwant %+v", got, want)
	}
}

func TestPreviewOf_SeriesWithoutPartsHasNoGroups(t *testing.T) {
	if got := PreviewOf(Series{Title: "Bare"}); got.Groups != nil {
		t.Errorf("groups = %+v, want none", got.Groups)
	}
}
