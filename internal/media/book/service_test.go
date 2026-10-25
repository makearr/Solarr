package book

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/metadata/audible"
	"github.com/makearr/Solarr/internal/metadata/googlebooks"
	"github.com/makearr/Solarr/internal/metadata/openlibrary"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

type fakeOpenLibrary struct{ asked []string }

func (f *fakeOpenLibrary) Search(_ context.Context, title, author string, _ int) ([]openlibrary.Doc, error) {
	f.asked = append(f.asked, "search:"+title+"|"+author)
	return []openlibrary.Doc{{Key: "/works/OL1001W", Title: "The Lantern Keeper", AuthorNames: []string{"Mara Quell"}}}, nil
}

func (f *fakeOpenLibrary) Doc(_ context.Context, workID string) (openlibrary.Doc, error) {
	return openlibrary.Doc{Key: "/works/" + workID, Title: "The Lantern Keeper", AuthorNames: []string{"Mara Quell"}, AuthorKeys: []string{"OL2001A"},
		FirstPublishYear: 1987, Publishers: []string{"Northlight"}, Pages: 312, Languages: []string{"eng"}, ISBN: []string{"0000000000", "9780000000002"}}, nil
}

func (f *fakeOpenLibrary) Work(_ context.Context, workID string) (openlibrary.Work, error) {
	f.asked = append(f.asked, "work:"+workID)
	if workID != "OL1001W" {
		return openlibrary.Work{}, openlibrary.ErrNotFound
	}
	return openlibrary.Work{Key: "/works/OL1001W", Title: "The Lantern Keeper", Description: "Open Library's own words.",
		Subjects: []string{"Fantasy fiction", "Accessible book", "Fiction / Fantasy / Epic"}, Covers: []int64{42}}, nil
}

func (f *fakeOpenLibrary) ByISBN(_ context.Context, isbn string) (openlibrary.Edition, error) {
	f.asked = append(f.asked, "isbn:"+isbn)
	return openlibrary.Edition{}, openlibrary.ErrNotFound
}

type fakeGoogle struct{}

func (fakeGoogle) Volume(context.Context, string) (googlebooks.Volume, error) {
	var volume googlebooks.Volume
	volume.ID = "abcDEF123"
	volume.VolumeInfo.Title, volume.VolumeInfo.Description, volume.VolumeInfo.PageCount = "The Lantern Keeper", "Google's words.", 320
	volume.VolumeInfo.Categories = []string{"Fiction"}
	return volume, nil
}
func (fakeGoogle) Search(context.Context, string, string, int) ([]googlebooks.Volume, error) {
	return nil, nil
}
func (fakeGoogle) ByISBN(ctx context.Context, _ string) (googlebooks.Volume, error) {
	return fakeGoogle{}.Volume(ctx, "")
}

type fakeWikidata struct{}

func (fakeWikidata) BookIDs(context.Context, string) (wikidata.WorkIDs, bool, error) {
	return wikidata.WorkIDs{QID: "Q500", OpenLibrary: "OL1001W", GoogleBooks: "abcDEF123", Goodreads: "77"}, true, nil
}

func (fakeWikidata) FactsOf(_ context.Context, _ wikidata.FactKind, ids []string) (map[string]wikidata.WorkFacts, error) {
	return map[string]wikidata.WorkFacts{ids[0]: {QID: "Q500", OriginalTitle: "Der Laternenhüter", Summary: "Wikipedia's lead.", Date: "1986-11-02",
		Makers: []string{"Mara Quell"}, Series: &wikidata.SeriesPlace{QID: "Q600", Name: "The Lantern Cycle", Position: "1"}}}, nil
}

type fakeAudible struct{}

func (fakeAudible) Search(context.Context, string, string, int) ([]audible.Product, error) {
	return nil, nil
}
func (fakeAudible) Book(_ context.Context, asin string) (audible.Book, error) {
	return audible.Book{ASIN: asin, Title: "The Lantern Keeper", Authors: []audible.Person{{Name: "Mara Quell"}}, Narrators: []audible.Person{{Name: "Tobin Reyes"}},
		RuntimeLengthMin: 612, ReleaseDate: "2015-03-02T00:00:00.000Z", SeriesPrimary: &audible.SeriesRef{Name: "The Lantern Cycle", Position: "Book 1"}}, nil
}

func TestFetchFor_WikidataLeadsWhatItHoldsAndNeverTheTitle(t *testing.T) {
	openLibrary := &fakeOpenLibrary{}
	service := Service{Format: Ebook, OpenLibrary: openLibrary, GoogleBooks: fakeGoogle{}, Wikidata: fakeWikidata{}}
	book, err := service.FetchFor(t.Context(), 0, IDs{OpenLibrary: "OL1001W"}, Hint{})
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "The Lantern Keeper" || book.Overview != "Wikipedia's lead." || book.Year != 1986 || book.Pages != 312 {
		t.Errorf("book = %+v", book)
	}
	if !reflect.DeepEqual(book.IDs, IDs{OpenLibrary: "OL1001W", GoogleBooks: "abcDEF123", ISBN: "9780000000002", Goodreads: "77"}) {
		t.Errorf("ids = %+v", book.IDs)
	}
	if book.Series == nil || *book.Series != (Series{Key: "Q600", Name: "The Lantern Cycle", Position: "1"}) {
		t.Errorf("series = %+v", book.Series)
	}
	if !slices.Contains(book.Aliases, "Der Laternenhüter") {
		t.Errorf("the original title should be another name: %v", book.Aliases)
	}
	if !reflect.DeepEqual(book.Genres, []string{"Fantasy fiction", "Epic"}) {
		t.Errorf("genres = %v", book.Genres)
	}
	if book.Credits[0].Image == "" {
		t.Errorf("the author's photo should come from Open Library: %+v", book.Credits)
	}
}

func TestFetchFor_TheLibrarysOwnOrderDecides(t *testing.T) {
	service := Service{Format: Ebook, OpenLibrary: &fakeOpenLibrary{}, GoogleBooks: fakeGoogle{}, Wikidata: fakeWikidata{},
		PriorityFor: func(int64) []string { return []string{"googlebooks", "openlibrary"} }}
	book, err := service.FetchFor(t.Context(), 7, IDs{OpenLibrary: "OL1001W"}, Hint{})
	if err != nil {
		t.Fatal(err)
	}
	if book.Overview != "Google's words." || book.Pages != 320 || book.Series != nil {
		t.Errorf("Google first, Wikidata not asked: %+v", book)
	}
}

func TestFetchFor_AnAudiobookFindsItsWorkFromItsReading(t *testing.T) {
	openLibrary := &fakeOpenLibrary{}
	service := Service{Format: Audiobook, OpenLibrary: openLibrary, Audible: fakeAudible{}}
	book, err := service.FetchFor(t.Context(), 0, IDs{ASIN: "B000000001"}, Hint{})
	if err != nil {
		t.Fatal(err)
	}
	if book.IDs.OpenLibrary != "OL1001W" || book.RuntimeMin != 612 || book.Series.Position != "1" || book.Published != "2015-03-02" {
		t.Errorf("book = %+v", book)
	}
	if !slices.Contains(openLibrary.asked, "search:The Lantern Keeper|Mara Quell") {
		t.Errorf("asked %v", openLibrary.asked)
	}
	roles := map[string]string{}
	for _, credit := range book.Credits {
		roles[credit.Role] = credit.Name
	}
	if roles[RoleNarrator] != "Tobin Reyes" || roles[RoleAuthor] != "Mara Quell" {
		t.Errorf("credits = %+v", book.Credits)
	}
}

func TestFormat_AnAudiobookKeepsItsWorkIDsApartFromTheBooks(t *testing.T) {
	ids := IDs{OpenLibrary: "OL1001W", ASIN: "B000000001", ISBN: "9780000000002"}
	providers := func(format Format) []string {
		var out []string
		for _, write := range format.idWrites(ids) {
			out = append(out, write.Provider)
		}
		return out
	}
	if got := providers(Ebook); !reflect.DeepEqual(got, []string{"audible", "openlibrary", "isbn"}) {
		t.Errorf("book providers = %v", got)
	}
	if got := providers(Audiobook); !reflect.DeepEqual(got, []string{"audible", "openlibrary:audiobook", "isbn:audiobook"}) {
		t.Errorf("audiobook providers = %v", got)
	}
	stored := map[string]string{"audible": "B000000001", "openlibrary:audiobook": "OL1001W"}
	if got := Audiobook.idsOf(stored); got != (IDs{OpenLibrary: "OL1001W", ASIN: "B000000001"}) {
		t.Errorf("read back %+v", got)
	}
	if named, ok := Audiobook.NamedIDs("openlibrary:audiobook", "OL1001W"); !ok || named.OpenLibrary != "OL1001W" {
		t.Errorf("named %+v %v", named, ok)
	}
}

func TestReleaseFacts_StandsTheClaimedFormatInForTheFile(t *testing.T) {
	info, _ := parsebook.Parse("Mara Quell - The Lantern Keeper (2015) [M4B] 64k")
	facts := ReleaseFacts(Audiobook, info, indexer.RawItem{Title: "x"}, "newznab")
	if facts.File.Extension != "m4b" || facts.File.AudioCodec != "aac" || facts.File.AudioBitrate != 64000 {
		t.Errorf("facts = %+v", facts.File)
	}
	if Ebook.Claims(info) {
		t.Error("an M4B release is no ebook")
	}
}

func TestNaturalCompare_NumbersChaptersAsAPersonDoes(t *testing.T) {
	names := []string{"Part 10.mp3", "Part 2.mp3", "Part 1.mp3", "intro.mp3"}
	slices.SortFunc(names, NaturalCompare)
	if want := []string{"intro.mp3", "Part 1.mp3", "Part 2.mp3", "Part 10.mp3"}; !reflect.DeepEqual(names, want) {
		t.Errorf("sorted %v, want %v", names, want)
	}
}

func TestPickCandidate_TakesTheExactTitleByTheNamedAuthorOnly(t *testing.T) {
	candidates := []Candidate{
		{IDs: IDs{OpenLibrary: "OL9W"}, Title: "The Lantern Keeper", Author: "Tobin Reyes"},
		{IDs: IDs{OpenLibrary: "OL1001W"}, Title: "The Lantern Keeper", Author: "Mara Quell"},
	}
	pick, ok := pickCandidate(candidates, parsebook.Info{Title: "The Lantern Keeper", Author: "Mara Quell"})
	if !ok || pick.IDs.OpenLibrary != "OL1001W" {
		t.Errorf("pick = %+v, %v", pick, ok)
	}
	if _, ok := pickCandidate(candidates, parsebook.Info{Title: "The Lantern Keeper"}); ok {
		t.Error("two books of one title and no author named is a person's decision")
	}
}
