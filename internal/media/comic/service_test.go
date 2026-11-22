package comic

import (
	"context"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/metadata/comicvine"
	"github.com/makearr/Solarr/internal/metadata/mangadex"
	"github.com/makearr/Solarr/internal/metadata/metron"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

type fakeComicVine struct {
	issues []comicvine.Issue
}

func (fakeComicVine) Configured() bool { return true }
func (fakeComicVine) SearchSeries(context.Context, string, int) ([]comicvine.Series, error) {
	return []comicvine.Series{{ID: 4050, Name: "Lantern Wardens", StartYear: 2019, Publisher: "Northlight", IssueCount: 3}}, nil
}
func (fakeComicVine) Series(context.Context, int) (comicvine.Series, error) {
	return comicvine.Series{ID: 4050, Name: "Lantern Wardens", StartYear: 2019, Publisher: "Northlight", IssueCount: 3, Description: "Wardens of the lantern road."}, nil
}
func (f fakeComicVine) Issues(context.Context, int, int) ([]comicvine.Issue, bool, error) {
	return f.issues, false, nil
}

type fakeMetron struct{}

func (fakeMetron) Configured() bool { return true }
func (fakeMetron) SearchSeries(context.Context, string, int) ([]metron.Series, error) {
	return []metron.Series{{ID: 77, Name: "Lantern Wardens", YearBegan: 2019, ComicVineID: 4050}}, nil
}
func (fakeMetron) Series(context.Context, int) (metron.Series, error) {
	return metron.Series{ID: 77, Name: "Lantern Wardens", YearBegan: 2019, Type: "Limited Series", Publisher: "Northlight", ComicVineID: 4050}, nil
}
func (fakeMetron) Issues(context.Context, int, int) ([]metron.Issue, bool, error) {
	return []metron.Issue{{ID: 900, Number: "2", Pages: 32, Rating: "Teen"}}, false, nil
}

var lanternIssues = []comicvine.Issue{
	{ID: 1, Number: "1", CoverDate: "2019-03-00", StoreDate: "2019-02-06"},
	{ID: 2, Number: "1.5", CoverDate: "2019-04-00"},
	{ID: 3, Number: "2", CoverDate: "2019-05-00"},
	{ID: 4, Number: "Annual 1", CoverDate: "2019-12-00"},
}

func comicStore(t *testing.T) *store.Store {
	t.Helper()
	directory := t.TempDir()
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, directory, owner); err != nil {
		t.Fatal(err)
	}
	return opened
}

func comicService(t *testing.T, sources Sources) Service { return serviceOn(comicStore(t), sources) }

func serviceOn(opened *store.Store, sources Sources) Service {
	return Service{
		Sources: sources, Series: library.NewSeries(opened), Reader: library.NewSeriesReader(opened), Desired: library.NewDesired(opened),
		Clock: clock.NewFakeAtEpoch(), KindOf: func(int64) string { return KindComics },
		ProvidersOf: func(int64) []string { return []string{settings.ComicSourceComicVine, settings.ComicSourceMetron} },
	}
}

func TestAddingAComicsSeriesStoresItsIssuesWithComicVineNumberingAndMetronsPageCounts(t *testing.T) {
	service := comicService(t, Sources{ComicVine: fakeComicVine{issues: lanternIssues}, Metron: fakeMetron{}})
	result, err := service.Add(t.Context(), AddRequest{IDs: map[string]string{ProviderComicVine: "4050"}, CategoryID: 1, ProfileID: 1, Monitor: MonitorFirst})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Series.IDs[ProviderMetron] != "77" || result.Series.Format != FormatSeries {
		t.Fatalf("add = %+v", result)
	}
	parts, err := service.Desired.AllParts(t.Context(), result.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	type stored struct {
		Ref, Date string
		Monitored bool
	}
	var got []stored
	for _, part := range parts {
		got = append(got, stored{part.MediaRef, part.Date.String, part.Monitored != 0})
	}
	want := []stored{
		{"issue:1", "2019-02-06", true}, {"issue:1.5", "2019-04-01", false}, {"issue:2", "2019-05-01", false}, {"annual:Annual 1", "2019-12-01", false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parts = %+v, want %+v", got, want)
	}
	if again, err := service.Add(t.Context(), AddRequest{IDs: map[string]string{ProviderMetron: "77"}, CategoryID: 1, ProfileID: 1}); err != nil || again.Created || again.WorkID != result.WorkID {
		t.Fatalf("second add = %+v, %v: the bridged id must name the held series", again, err)
	}
}

func TestARefreshAddsTheNewIssueMonitoredWhenTheSeriesIsAndKeepsTheOthers(t *testing.T) {
	cv := &fakeComicVine{issues: lanternIssues[:3]}
	service := comicService(t, Sources{ComicVine: cv})
	result, err := service.Add(t.Context(), AddRequest{IDs: map[string]string{ProviderComicVine: "4050"}, CategoryID: 1, ProfileID: 1, Monitor: MonitorNone})
	if err != nil {
		t.Fatal(err)
	}
	cv.issues = append(cv.issues[:3:3], comicvine.Issue{ID: 7, Number: "3", CoverDate: "2019-06-00"})
	service.ComicVine = cv
	if _, err := service.Refresh(t.Context(), 1, result.WorkID); err != nil {
		t.Fatal(err)
	}
	parts, _ := service.Desired.AllParts(t.Context(), result.WorkID)
	if len(parts) != 4 || parts[3].MediaRef != "issue:3" || parts[3].Monitored != 0 {
		t.Fatalf("parts after refresh = %+v: an unmonitored series' new issue stays unmonitored", parts)
	}
}

func TestAnAdultSeriesIsRefusedByAKindThatIsNotAdult(t *testing.T) {
	service := comicService(t, Sources{AniList: fakeAniList{adult: true}})
	service.KindOf = func(int64) string { return KindManga }
	service.ProvidersOf = func(int64) []string { return []string{settings.TVSourceAniList} }
	if _, err := service.Add(t.Context(), AddRequest{IDs: map[string]string{ProviderAniListManga: "5"}, CategoryID: 1, ProfileID: 1}); err == nil {
		t.Fatal("an adult series was added to a manga library")
	}
	found, err := service.Search(t.Context(), 1, "Moon", 10)
	if err != nil || len(found) != 0 {
		t.Fatalf("search = %+v, %v: an adult result must not be offered to a manga library", found, err)
	}
	service.KindOf = func(int64) string { return KindHentai }
	if found, _ = service.Search(t.Context(), 1, "Moon", 10); len(found) != 1 || found[0].Provider != ProviderAniListManga {
		t.Fatalf("hentai search = %+v", found)
	}
}

type fakeAniList struct{ adult bool }

func (f fakeAniList) Manga(context.Context, int64) (anilist.Manga, error) {
	m := anilist.Manga{ID: 5, Volumes: 3, IsAdult: f.adult, Format: "MANGA", Status: "FINISHED", CountryOfOrigin: "JP"}
	m.Title.English = "Moon Orchard"
	m.StartDate.Year = 2010
	return m, nil
}
func (f fakeAniList) SearchManga(ctx context.Context, _ string, _ int) ([]anilist.Manga, bool, error) {
	m, _ := f.Manga(ctx, 5)
	return []anilist.Manga{m}, false, nil
}

type fakeMangaDex struct{ volumes []mangadex.Volume }

func (fakeMangaDex) SearchManga(context.Context, string, int, []string) ([]mangadex.Manga, bool, error) {
	return []mangadex.Manga{{ID: "dex-1", Title: "Moon Orchard", Links: map[string]string{"al": "5"}}}, false, nil
}
func (fakeMangaDex) Manga(context.Context, string) (mangadex.Manga, error) {
	return mangadex.Manga{ID: "dex-1", Title: "Moon Orchard", OriginalLanguage: "ja", ContentRating: "safe", Links: map[string]string{"al": "5", "mal": "9"}}, nil
}
func (f fakeMangaDex) Aggregate(context.Context, string, string) ([]mangadex.Volume, error) {
	return f.volumes, nil
}

func TestAMangaTakesItsVolumesFromTheAggregateAndLeavesUnnumberedChaptersLoose(t *testing.T) {
	sources := Sources{AniList: fakeAniList{}, MangaDex: fakeMangaDex{volumes: []mangadex.Volume{
		{Number: "1", Chapters: []string{"1", "2"}}, {Number: "2", Chapters: []string{"3"}}, {Number: mangadex.UnnumberedVolume, Chapters: []string{"4"}},
	}}}
	service := comicService(t, sources)
	service.KindOf = func(int64) string { return KindManga }
	order := []string{settings.TVSourceAniList, settings.ComicSourceMangaDex}
	service.ProvidersOf = func(int64) []string { return order }
	series, err := service.Fetch(t.Context(), 1, map[string]string{ProviderAniListManga: "5"}, Hint{})
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, part := range series.Parts {
		refs = append(refs, part.Ref())
	}
	// AniList says three volumes: the third is listed although the aggregate lacks it; the loose chapter sorts last.
	if want := []string{"volume:1", "volume:2", "volume:3", "chapter:4"}; !reflect.DeepEqual(refs, want) {
		t.Fatalf("parts = %v, want %v", refs, want)
	}
	if series.IDs[ProviderMangaDex] != "dex-1" || series.IDs[ProviderMAL] != "9" || series.OriginalLanguage != "ja" || series.Status != "ended" {
		t.Fatalf("series = %+v", series)
	}
	if got := series.Parts[0].Chapters; !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Fatalf("volume 1 chapters = %v", got)
	}

	service.MangaDex = nil
	series, err = service.Fetch(t.Context(), 1, map[string]string{ProviderAniListManga: "5"}, Hint{})
	if err != nil || len(series.Parts) != 3 || series.Parts[2].Ref() != "volume:3" {
		t.Fatalf("without an aggregate: %+v, %v", series.Parts, err)
	}
}

func TestSortKeyOrdersIssuesBeforeAnnualsAndDecimalsBetween(t *testing.T) {
	keys := []string{SortKey(PartIssue, "-1"), SortKey(PartIssue, "0"), SortKey(PartIssue, "1"), SortKey(PartIssue, "1.5"), SortKey(PartIssue, "1AU"), SortKey(PartIssue, "2"), SortKey(PartIssue, "10"), SortKey(PartAnnual, "1"), SortKey(PartChapter, "1")}
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Fatalf("key %d (%s) does not sort before key %d (%s)", i-1, keys[i-1], i, keys[i])
		}
	}
}

func TestMonitorOptionsPickIssuesTheWayTheyAreNamed(t *testing.T) {
	now := time.Date(2020, 1, 10, 0, 0, 0, 0, time.UTC)
	parts := []Part{
		{Kind: PartIssue, Number: "1", Store: "2019-01-01"}, {Kind: PartIssue, Number: "2", Store: "2020-01-09"},
		{Kind: PartIssue, Number: "3", Store: "2020-02-01"}, {Kind: PartIssue, Number: "4"}, {Kind: PartChapter, Number: "9"},
	}
	for option, want := range map[MonitorOption]string{
		MonitorAll: "1234", MonitorFuture: "34", MonitorFirst: "1", MonitorLatest: "4", MonitorNone: "",
	} {
		wanted := Monitored(parts, option, now)
		got := ""
		for _, number := range []string{"1", "2", "3", "4"} {
			if wanted["issue:"+number] {
				got += number
			}
		}
		if got != want || wanted["chapter:9"] {
			t.Errorf("%s monitors %q (chapter %v), want %q and no chapter", option, got, wanted["chapter:9"], want)
		}
	}

}
