package comic

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
)

type fakeLocal struct {
	item     mediaserver.Item
	asked    []mediaserver.Question
	kinds    []string
	notFound bool
}

func (f *fakeLocal) Ask(_ context.Context, _ int64, kind, _ string, question mediaserver.Question) (mediaserver.Item, bool, error) {
	f.asked, f.kinds = append(f.asked, question), append(f.kinds, kind)
	return f.item, !f.notFound, nil
}

func (f *fakeLocal) AskByPath(context.Context, int64, string) (mediaserver.Item, bool, error) {
	return mediaserver.Item{}, false, nil
}

func TestAskLocal_FillsOnlyWhatTheSeriesLacksAndKeepsItsOwnIds(t *testing.T) {
	local := &fakeLocal{item: mediaserver.Item{
		Title: "Moon Garden", OriginalTitle: "Tsuki", Overview: "From the server.", Year: 2020, Language: "en", Genres: []string{"Fantasy"},
		Organisations: []string{"Example Press"}, IDs: map[string]string{"comicvine": "999", "anilist": "777"},
		People: []mediaserver.Credit{{Name: "Ann Writer", Kind: "writer"}},
	}}
	series := &Series{Title: "Moon Garden", Overview: "Already known.", IDs: map[string]string{ProviderComicVine: "123"}}
	Sources{Local: local}.askLocal(t.Context(), 4, series, "local:kavita", Hint{})
	want := &Series{
		Title: "Moon Garden", Overview: "Already known.", Publisher: "Example Press", Year: 2020, OriginalLanguage: "en",
		IDs: map[string]string{ProviderComicVine: "123", ProviderAniListManga: "777"}, Aliases: []string{"Tsuki"}, Genres: []string{"Fantasy"},
		Credits: []Credit{{Name: "Ann Writer", Role: "writer"}}, Sources: []string{"local:kavita"},
	}
	if !reflect.DeepEqual(series, want) {
		t.Errorf("series = %+v\nwant %+v", series, want)
	}
	if !reflect.DeepEqual(local.kinds, []string{"kavita"}) || local.asked[0].Title != "Moon Garden" {
		t.Errorf("asked %v %+v", local.kinds, local.asked)
	}
}

func TestAskLocal_AnotherYearIsAnotherSeries(t *testing.T) {
	local := &fakeLocal{item: mediaserver.Item{Title: "Moon Garden", Year: 1999, Overview: "Old."}}
	series := &Series{IDs: map[string]string{}}
	Sources{Local: local}.askLocal(t.Context(), 4, series, "local:kavita", Hint{Title: "Moon Garden", Year: 2020})
	if len(series.Sources) != 0 || series.Overview != "" {
		t.Errorf("series = %+v", series)
	}
}
