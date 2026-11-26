package adult

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
)

func TestPreviewOf_SceneRoundsDurationAndNamesStudioNetworkAndPerformers(t *testing.T) {
	got := PreviewOf(Scene{
		Title: "Invented Scene", Overview: "Plot.", Date: "2020-02-03", Year: 2020, DurationSec: 1829, Studio: "Made Up Studio", Network: "Made Up Network",
		Tags:    []string{"tag-a"},
		Images:  []Image{{Kind: "poster", URL: "http://x/p.jpg"}},
		Credits: []Credit{{Name: "Pat Performer", Role: "performer", Image: "http://x/pp.jpg"}},
	})
	want := media.WorkPreview{
		Title: "Invented Scene", Subtitle: "Made Up Studio", Overview: "Plot.", PosterURL: "http://x/p.jpg", Year: 2020, Genres: []string{"tag-a"},
		Facts: []media.PreviewFact{
			{Key: media.FactStudio, Value: "Made Up Studio"}, {Key: media.FactNetwork, Value: "Made Up Network"},
			{Key: media.FactReleased, Value: "2020-02-03"}, {Key: media.FactRuntimeMin, Value: "30"},
		},
		People: []media.PreviewPerson{{Name: "Pat Performer", Role: "performer", ImageURL: "http://x/pp.jpg"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("preview = %+v\nwant %+v", got, want)
	}
}
