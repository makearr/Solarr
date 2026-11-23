package comic

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"path"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/parsecomic"
	"github.com/makearr/Solarr/internal/metadata/comicvine"
	"github.com/makearr/Solarr/internal/metadata/mangadex"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

const scanRoot = "/library"

type inLibrary struct{}

func (inLibrary) Category(int64) (settings.Category, bool) {
	return settings.Category{ID: 3, Kind: KindComics}, true
}
func (inLibrary) ProfileFor(_ settings.Category, fallback int64) int64 { return fallback }

// namedSeries answers a ComicVine search with the one series when the query names it.
type namedSeries struct{ fakeComicVine }

func (namedSeries) SearchSeries(_ context.Context, query string, _ int) ([]comicvine.Series, error) {
	if !sameTitle(query, "Lantern Wardens") {
		return nil, nil
	}
	return []comicvine.Series{{ID: 4050, Name: "Lantern Wardens", StartYear: 2019, Publisher: "Northlight", IssueCount: 3}}, nil
}

func cbz(t *testing.T, salt, comicInfo string) []byte {
	t.Helper()
	var page bytes.Buffer
	if err := png.Encode(&page, image.NewGray(image.Rect(0, 0, 60, 90))); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, entry := range []struct{ name, body string }{{"001.png", page.String()}, {"002.png", page.String()}, {"ComicInfo.xml", comicInfo}, {"salt.txt", salt}} {
		if entry.body == "" {
			continue
		}
		file, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte(entry.body))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func scanned(t *testing.T, fsys *fsx.Mem, rel, comicInfo string) scan.Observed {
	t.Helper()
	absolute := path.Join(scanRoot, rel)
	if err := fsys.MkdirAll(path.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create(absolute)
	if err != nil {
		t.Fatal(err)
	}
	content := cbz(t, rel, comicInfo)
	_, _ = file.Write(content)
	_ = file.Close()
	return scan.Observed{File: scan.Found{RelPath: rel, SizeBytes: int64(len(content)), ModTime: time.Unix(0, 0)}}
}

func TestAScanPlacesFilesOnIssuesAddsTheSeriesAndKeepsAnIdBeforeAName(t *testing.T) {
	opened := comicStore(t)
	service := serviceOn(opened, Sources{ComicVine: namedSeries{fakeComicVine{issues: lanternIssues}}})
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll(scanRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	categories := inLibrary{}
	consumer := NewConsumer(ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Unidentified: library.NewUnidentified(opened), Service: service, Clock: fake,
		Resolver: Resolver{Service: service, Categories: categories, DefaultProfileID: 1},
	})
	root := settings.RootFolder{ID: 1, Path: scanRoot, Online: true}
	batch := []scan.Observed{
		scanned(t, fsys, "Lantern Wardens (2019)/Lantern Wardens 002 (2019).cbz", ""),
		scanned(t, fsys, "Unknown Folder/Wrong Name 007.cbz", `<ComicInfo><Series>Wrong Name</Series><Number>1</Number><Web>https://comicvine.gamespot.com/wrong-name/4050-4050/</Web></ComicInfo>`),
		scanned(t, fsys, "Nobody Knows/Nobody Knows 001.cbz", ""),
	}
	if err := consumer.Observe(t.Context(), root, 3, batch); err != nil {
		t.Fatal(err)
	}
	held, found := service.WorkByIDs(t.Context(), map[string]string{ProviderComicVine: "4050"})
	if !found {
		t.Fatal("the series was not added")
	}
	parts, _ := service.Desired.AllParts(t.Context(), held)
	got := map[string]bool{}
	for _, part := range parts {
		got[part.MediaRef] = part.Monitored != 0
	}
	// Added unmonitored; each file's issue becomes wanted, and the file whose name is wrong but whose metadata names the series id is placed.
	if !got["issue:2"] || !got["issue:1"] || got["issue:1.5"] {
		t.Fatalf("monitored parts = %v, want issues 1 and 2 only", got)
	}
	queue, err := library.NewUnidentified(opened).Count(t.Context())
	if err != nil || queue != 1 {
		t.Fatalf("unidentified = %d, %v, want the one file no series was found for", queue, err)
	}
}

func TestPartKeysReadRangesIssuesVolumesAndChapters(t *testing.T) {
	cases := []struct {
		name, file string
		manga      bool
		want       []string
	}{
		{"a range", "Lantern Wardens 001-003 (2019).cbz", false, []string{"issue:1", "issue:2", "issue:3"}},
		{"a decimal issue", "Lantern Wardens #1.5 (2019).cbz", false, []string{"issue:1.5"}},
		{"an annual", "Lantern Wardens Annual 2 (2019).cbz", false, []string{"annual:2"}},
		{"a volume range", "Moon Orchard v01-v03.cbz", true, []string{"volume:1", "volume:2", "volume:3"}},
		{"a chapter", "Moon Orchard c012.cbz", true, []string{"chapter:12"}},
	}
	for _, c := range cases {
		var got []string
		for _, key := range PartKeys([]parsecomic.Claim{parsecomic.Parse(c.file)}, c.manga) {
			got = append(got, key.MediaRef)
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %v, want %v", c.name, got, c.want)
			}
		}
	}
}

type twoRuns struct{ fakeComicVine }

func (twoRuns) SearchSeries(context.Context, string, int) ([]comicvine.Series, error) {
	return []comicvine.Series{{ID: 1, Name: "Lantern Wardens", StartYear: 1999}, {ID: 2, Name: "Lantern Wardens", StartYear: 2019}}, nil
}

func TestATitleTwoSeriesShareIsOnlyPlacedWhenTheYearTellsThemApart(t *testing.T) {
	opened := comicStore(t)
	service := serviceOn(opened, Sources{ComicVine: twoRuns{}})
	resolver := Resolver{Service: service, Categories: inLibrary{}, DefaultProfileID: 1}
	if id, candidates := resolver.Resolve(t.Context(), 3, Evidence{Series: "Lantern Wardens"}); id != 0 || len(candidates) != 2 {
		t.Fatalf("no year: id %d with %d candidates, want none placed and both offered", id, len(candidates))
	}
	if id, _ := resolver.Resolve(t.Context(), 3, Evidence{Series: "Lantern Wardens", Year: 2019}); id == 0 {
		t.Fatal("the year 2019 names one of the two runs")
	}
}

func TestAHeldSeriesOfAnotherYearIsNotTheOneANameSays(t *testing.T) {
	opened := comicStore(t)
	service := serviceOn(opened, Sources{ComicVine: fakeComicVine{issues: lanternIssues}})
	added, err := service.Add(t.Context(), AddRequest{IDs: map[string]string{ProviderComicVine: "4050"}, CategoryID: 3, ProfileID: 1})
	if err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{Service: service, Categories: inLibrary{}, DefaultProfileID: 1}
	if id, held := resolver.inLibrary(t.Context(), 3, Evidence{Series: "Lantern Wardens", Year: 2019}); !held || id != added.WorkID {
		t.Fatalf("2019 = %d, %v", id, held)
	}
	if id, held := resolver.inLibrary(t.Context(), 3, Evidence{Series: "Lantern Wardens", Year: 1999}); held {
		t.Fatalf("1999 was placed on series %d", id)
	}
}

func TestAChapterFileLinksToTheVolumeThatHoldsItAndAnUnlistedChapterStandsAlone(t *testing.T) {
	opened := comicStore(t)
	service := serviceOn(opened, Sources{AniList: fakeAniList{}, MangaDex: fakeMangaDex{volumes: []mangadex.Volume{
		{Number: "1", Chapters: []string{"1", "2"}}, {Number: "2", Chapters: []string{"3"}},
	}}})
	service.KindOf = func(int64) string { return KindManga }
	service.ProvidersOf = func(int64) []string { return []string{settings.TVSourceAniList, settings.ComicSourceMangaDex} }
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll(scanRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	consumer := NewConsumer(ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Unidentified: library.NewUnidentified(opened), Service: service, Clock: fake,
		Resolver: Resolver{Service: service, Categories: inLibrary{}, DefaultProfileID: 1},
	})
	batch := []scan.Observed{
		scanned(t, fsys, "Moon Orchard/Moon Orchard c002.cbz", ""),
		scanned(t, fsys, "Moon Orchard/Moon Orchard c099.cbz", ""),
		scanned(t, fsys, "Moon Orchard/Moon Orchard v03.cbz", ""),
	}
	if err := consumer.Observe(t.Context(), settings.RootFolder{ID: 1, Path: scanRoot, Online: true}, 3, batch); err != nil {
		t.Fatal(err)
	}
	held, found := service.WorkByIDs(t.Context(), map[string]string{ProviderAniListManga: "5"})
	if !found {
		t.Fatal("the manga was not added")
	}
	parts, _ := service.Desired.AllParts(t.Context(), held)
	got := map[string]bool{}
	for _, part := range parts {
		got[part.MediaRef] = part.Monitored != 0
	}
	want := map[string]bool{"volume:1": true, "volume:2": false, "volume:3": true, "chapter:99": true}
	if len(got) != len(want) {
		t.Fatalf("parts = %v, want %v", got, want)
	}
	for ref, monitored := range want {
		if got[ref] != monitored {
			t.Fatalf("parts = %v, want %v", got, want)
		}
	}
	if waiting, _ := library.NewUnidentified(opened).Count(t.Context()); waiting != 0 {
		t.Fatalf("%d files waiting, want all placed", waiting)
	}
}

func TestAMangaWhoseVolumeCountNoProviderStatesGainsTheVolumesItsFilesName(t *testing.T) {
	opened := comicStore(t)
	service := serviceOn(opened, Sources{AniList: fakeAniList{bare: true}})
	service.KindOf = func(int64) string { return KindManga }
	service.ProvidersOf = func(int64) []string { return []string{settings.TVSourceAniList} }
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll(scanRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	consumer := NewConsumer(ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Unidentified: library.NewUnidentified(opened), Service: service, Clock: fake,
		Resolver: Resolver{Service: service, Categories: inLibrary{}, DefaultProfileID: 1},
	})
	batch := []scan.Observed{scanned(t, fsys, "Moon Orchard/Moon Orchard v01.cbz", ""), scanned(t, fsys, "Moon Orchard/Moon Orchard c050.cbz", "")}
	if err := consumer.Observe(t.Context(), settings.RootFolder{ID: 1, Path: scanRoot, Online: true}, 3, batch); err != nil {
		t.Fatal(err)
	}
	held, _ := service.WorkByIDs(t.Context(), map[string]string{ProviderAniListManga: "5"})
	parts, _ := service.Desired.AllParts(t.Context(), held)
	var refs []string
	for _, part := range parts {
		refs = append(refs, part.MediaRef)
	}
	if len(refs) != 2 || refs[0] != "volume:1" || refs[1] != "chapter:50" {
		t.Fatalf("parts = %v", refs)
	}
	if waiting, _ := library.NewUnidentified(opened).Count(t.Context()); waiting != 0 {
		t.Fatalf("%d files waiting", waiting)
	}
}

func TestAScannedVolumeAlsoSatisfiesTheItemWantingThatVolumeOnItsOwn(t *testing.T) {
	opened := comicStore(t)
	service := serviceOn(opened, Sources{AniList: fakeAniList{bare: true}})
	service.KindOf = func(int64) string { return KindManga }
	service.ProvidersOf = func(int64) []string { return []string{settings.TVSourceAniList} }
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll(scanRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	consumer := NewConsumer(ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Unidentified: library.NewUnidentified(opened), Service: service, Clock: fake,
		Resolver: Resolver{Service: service, Categories: inLibrary{}, DefaultProfileID: 1},
	})
	root := settings.RootFolder{ID: 1, Path: scanRoot, Online: true}
	if err := consumer.Observe(t.Context(), root, 3, []scan.Observed{scanned(t, fsys, "Moon Orchard/Moon Orchard v01.cbz", "")}); err != nil {
		t.Fatal(err)
	}
	held, _ := service.WorkByIDs(t.Context(), map[string]string{ProviderAniListManga: "5"})
	if _, err := service.Desired.EnsureTrackItem(t.Context(), library.TrackRequest{WorkID: held, MediaRef: "volume:1", CategoryID: 3, ProfileID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := consumer.Observe(t.Context(), root, 3, []scan.Observed{scanned(t, fsys, "Moon Orchard/Moon Orchard Vol 01.cbz", "")}); err != nil {
		t.Fatal(err)
	}
	items, err := librarydb.New(opened.Read()).ListDesiredItemsForWork(t.Context(), held)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, item := range items {
		got = append(got, fmt.Sprintf("%s/%s/%d/%s", item.MediaRef, item.SatisfactionKind, item.PresentCount, item.State))
	}
	if len(got) != 2 || got[0] != "volume/SET/1/COMPLETE" || got[1] != "volume:1/SINGLE/1/COMPLETE" {
		t.Fatalf("items = %v", got)
	}
}

func TestAScanDoesNotAddBackASeriesAPersonRemoved(t *testing.T) {
	opened := comicStore(t)
	service := serviceOn(opened, Sources{ComicVine: namedSeries{fakeComicVine{issues: lanternIssues}}})
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll(scanRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := service.Exclusions.Exclude(t.Context(), fake.Now(), MediaType, "Lantern Wardens", []library.ExternalIDWrite{{Provider: ProviderComicVine, ExternalID: "4050"}}); err != nil {
		t.Fatal(err)
	}
	consumer := NewConsumer(ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Unidentified: library.NewUnidentified(opened), Service: service, Clock: fake,
		Resolver: Resolver{Service: service, Categories: inLibrary{}, DefaultProfileID: 1},
	})
	root := settings.RootFolder{ID: 1, Path: scanRoot, Online: true}
	batch := []scan.Observed{scanned(t, fsys, "Lantern Wardens (2019)/Lantern Wardens 002 (2019).cbz", "")}
	if err := consumer.Observe(t.Context(), root, 3, batch); err != nil {
		t.Fatal(err)
	}
	if _, found := service.WorkByIDs(t.Context(), map[string]string{ProviderComicVine: "4050"}); found {
		t.Fatal("a removed series was added back by the scan")
	}
}
