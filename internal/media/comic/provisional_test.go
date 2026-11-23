package comic

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/mediaserver"
)

func TestAComicSeriesOnlyTheMediaServerKnowsIsMarkedProvisional(t *testing.T) {
	cases := []struct {
		name  string
		start Series
		want  library.MetadataMark
	}{
		{"only the server answered", Series{Title: "Moon Garden"}, library.MarkProvisional},
		{"a provider answered first", Series{Title: "Moon Garden", Overview: "Provider words.", Sources: []string{ProviderComicVine}}, library.MarkSettled},
	}
	for _, c := range cases {
		series := c.start
		Sources{Local: &fakeLocal{item: mediaserver.Item{Title: "Moon Garden", Overview: "From the server."}}}.askLocal(t.Context(), 4, &series, "local:kavita", Hint{})
		if mark := ToWrite(series, WriteOptions{}).Mark; mark != c.want {
			t.Errorf("%s: mark = %v, want %v (sources %v)", c.name, mark, c.want, series.Sources)
		}
	}
}
