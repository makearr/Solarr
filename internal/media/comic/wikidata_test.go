package comic

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

type fakeWikidata struct {
	asked [][]string
	kinds []wikidata.FactKind
	facts map[string]wikidata.WorkFacts
}

func (f *fakeWikidata) FactsOf(_ context.Context, kind wikidata.FactKind, ids []string) (map[string]wikidata.WorkFacts, error) {
	f.kinds, f.asked = append(f.kinds, kind), append(f.asked, ids)
	return f.facts, nil
}

func TestWikidataFillsAComicSeriesFoundByItsComicVineVolumeAndKeepsWhatIsHeld(t *testing.T) {
	source := &fakeWikidata{facts: map[string]wikidata.WorkFacts{"4050-7": {
		Date: "1963-03-01", Summary: "A team of wardens.", Genres: []string{"Superhero"}, Languages: []string{"en"}, Makers: []string{"Ada Quill"}, OriginalTitle: "Lantern Wardens",
	}}}
	series := &Series{Title: "Lantern Wardens", Overview: "Held overview", IDs: map[string]string{ProviderComicVine: "7"}}
	Sources{Wikidata: source}.askWikidata(t.Context(), series)
	if !reflect.DeepEqual(source.asked, [][]string{{"4050-7"}}) || source.kinds[0] != wikidata.FactComic {
		t.Errorf("asked %v as %v, want the Comic Vine volume id", source.asked, source.kinds)
	}
	want := &Series{
		Title: "Lantern Wardens", Overview: "Held overview", Year: 1963, Genres: []string{"Superhero"}, OriginalLanguage: "en",
		Credits: []Credit{{Name: "Ada Quill", Role: "creator"}}, Sources: []string{settings.SourceWikidata}, IDs: map[string]string{ProviderComicVine: "7"},
	}
	if !reflect.DeepEqual(series, want) {
		t.Errorf("series = %+v, want %+v", series, want)
	}
}

func TestWikidataIsNotAskedForASeriesWithoutAnIDItKnowsThemBy(t *testing.T) {
	source := &fakeWikidata{}
	Sources{Wikidata: source}.askWikidata(t.Context(), &Series{IDs: map[string]string{ProviderGCD: "7"}})
	Sources{Wikidata: source}.askWikidata(t.Context(), &Series{Manga: true, IDs: map[string]string{ProviderComicVine: "7"}})
	if len(source.asked) != 0 {
		t.Errorf("asked = %v, want nothing", source.asked)
	}
}
