package book

import (
	"context"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/bookbrainz"
	"github.com/makearr/Solarr/internal/metadata/fantlab"
	"github.com/makearr/Solarr/internal/metadata/hardcover"
	"github.com/makearr/Solarr/internal/metadata/internetarchive"
	"github.com/makearr/Solarr/internal/metadata/itunes"
)

func TestTheNewProvidersAnswerABookEachWithItsOwnId(t *testing.T) {
	apple := FromITunes(itunes.Audiobook{
		CollectionID: 9, Name: "Awaken Online: Catharsis (Unabridged)", Author: "Travis Bagwell", ReleaseDate: "2016-08-26T07:00:00Z",
		Artwork100: "https://is1-ssl.mzstatic.com/x/100x100bb.jpg",
	})
	if apple.IDs.ITunes != "9" || apple.Title != "Awaken Online: Catharsis" || apple.Year != 2016 || apple.Images[0].URL != "https://is1-ssl.mzstatic.com/x/1500x1500bb.jpg" {
		t.Errorf("iTunes book = %+v", apple)
	}
	russian := FromFantLab(fantlab.Work{ID: 4325, Name: "Дюна", Original: "Dune", Year: 1965, Authors: []fantlab.Author{{Name: "Фрэнк Герберт", Original: "Frank Herbert"}}})
	if russian.Title != "Dune" || russian.Aliases[0] != "Дюна" || russian.Authors()[0] != "Frank Herbert" || russian.IDs.FantLab != "4325" {
		t.Errorf("FantLab book = %+v", russian)
	}
	readers := FromHardcover(hardcover.Book{ID: "77", Title: "Dune", Rating: 4.2, Authors: []string{"Frank Herbert"}, ISBNs: []string{"9780441013593"}})
	if readers.IDs.Hardcover != "77" || readers.IDs.ISBN != "9780441013593" || readers.Ratings[0].Score != 8.4 {
		t.Errorf("Hardcover book = %+v", readers)
	}
	if !sameTitle("Dune (Unabridged)", "Dune") || sameTitle("Dune Messiah", "Children") {
		t.Error("a title with a tag after it is the same title, another book is not")
	}
}

type fakeITunes struct{}

func (fakeITunes) Search(context.Context, string, string, int) ([]itunes.Audiobook, error) {
	return []itunes.Audiobook{{CollectionID: 5, Name: "The Lantern Keeper (Unabridged)", Author: "Mara Quell"}}, nil
}
func (fakeITunes) Lookup(context.Context, int64) (itunes.Audiobook, error) {
	return itunes.Audiobook{}, itunes.ErrNotFound
}

func TestFetchFor_ITunesIsAskedByTheTitleAndAuthorAnotherSourceLearned(t *testing.T) {
	service := Service{Format: Audiobook, Audible: fakeAudible{}, ITunes: fakeITunes{}, PriorityFor: func(int64) []string { return []string{"audible", "itunes"} }}
	book, err := service.FetchFor(t.Context(), 7, IDs{ASIN: "B000000001"}, Hint{})
	if err != nil {
		t.Fatal(err)
	}
	if book.IDs.ITunes != "5" {
		t.Errorf("ids = %+v, want iTunes's collection found by the reading's title and author", book.IDs)
	}
}

func TestSameTitle_ASubtitleContinuesATitleButAnotherWordDoesNot(t *testing.T) {
	cases := []struct {
		found, wanted string
		want          bool
	}{
		{"Dune Messiah", "Dune", false},
		{"Dune: Deluxe Edition", "Dune", true},
		{"Dune", "Dune", true},
		{"Dune", "Dune: Deluxe Edition", true},
		{"Harry Potter and the Chamber of Secrets", "Harry Potter", false},
		{"Dune - A Novel", "dune", true},
	}
	for _, c := range cases {
		if got := sameTitle(c.found, c.wanted); got != c.want {
			t.Errorf("sameTitle(%q, %q) = %v, want %v", c.found, c.wanted, got, c.want)
		}
	}
}

type neighbourFirstITunes struct{ fakeITunes }

func (neighbourFirstITunes) Search(context.Context, string, string, int) ([]itunes.Audiobook, error) {
	return []itunes.Audiobook{
		{CollectionID: 1, Name: "Dune Messiah", Author: "Frank Herbert"},
		{CollectionID: 2, Name: "Dune: Deluxe Edition", Author: "Frank Herbert"},
		{CollectionID: 3, Name: "Dune", Author: "Frank Herbert"},
	}, nil
}

func TestAskITunes_TakesTheExactTitleNotTheNeighbourListedFirst(t *testing.T) {
	service := Service{Format: Audiobook, ITunes: neighbourFirstITunes{}}
	got, ok := service.askITunes(context.Background(), IDs{}, Hint{Title: "Dune"}, func(string, error, error) bool { return true })
	if !ok || got.IDs.ITunes != "3" {
		t.Errorf("got %+v ok=%v", got.IDs, ok)
	}
}

type fakeArchive struct{}

func (fakeArchive) Item(_ context.Context, identifier string) (internetarchive.Item, error) {
	if identifier != "examplefox00" {
		return internetarchive.Item{}, internetarchive.ErrNotFound
	}
	return internetarchive.Item{Title: "Example Fox (item)", Description: "Item by Open Library's ocaid."}, nil
}

func (fakeArchive) ByISBN(context.Context, string) (internetarchive.Item, error) {
	return internetarchive.Item{Title: "Example Fox", Description: "A fox digs under three farms to feed his family, and the farmers dig after him.",
		Subjects: []string{"Foxes -- Fiction"}, Language: "eng", Cover: "https://archive.org/services/img/example-fox"}, nil
}

func TestFetchFor_TheArchiveAnswersByISBNWithDescriptionGenresAndCover(t *testing.T) {
	service := Service{Format: Ebook, Archive: fakeArchive{}, PriorityFor: func(int64) []string { return []string{"internetarchive"} }}
	book, err := service.FetchFor(t.Context(), 7, IDs{ISBN: "9780000000001"}, Hint{Title: "Example Fox"})
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Example Fox" || !strings.HasPrefix(book.Overview, "A fox digs") || len(book.Images) == 0 || book.Images[0].Source != "internetarchive" {
		t.Errorf("book = %+v", book)
	}
}

func TestFetchFor_TheArchiveItemOpenLibraryNamesIsAskedBeforeTheISBN(t *testing.T) {
	service := Service{Format: Ebook, Archive: fakeArchive{}, PriorityFor: func(int64) []string { return []string{"internetarchive"} }}
	book, err := service.FetchFor(t.Context(), 7, IDs{ISBN: "9780000000001", Archive: "examplefox00"}, Hint{Title: "Example Fox"})
	if err != nil || book.Title != "Example Fox (item)" {
		t.Errorf("book = %+v err=%v", book, err)
	}
}

type fakeBookBrainz struct{}

func (fakeBookBrainz) Search(context.Context, string, int) ([]bookbrainz.Work, error) {
	return []bookbrainz.Work{
		{BBID: "w-fr", Name: "Moon Garden", Language: "fr", Authors: []string{"Otto Other"}},
		{BBID: "w-en", Name: "Moon Garden", Language: "en", Annotation: "A gardener finds the moon.", Authors: []string{"Ann Writer"}},
	}, nil
}

func TestAskBookBrainz_TakesTheWorkOfTheAuthorAskedForAndNeedsOne(t *testing.T) {
	service := Service{Format: Ebook, BookBrainz: fakeBookBrainz{}}
	ask := func(ids IDs, hint Hint) (Book, bool) {
		return service.askBookBrainz(t.Context(), ids, hint, func(string, error, error) bool { return true })
	}
	got, ok := ask(IDs{}, Hint{Title: "Moon Garden", Author: "Ann Writer"})
	if !ok || got.IDs.BookBrainz != "w-en" || got.Overview != "A gardener finds the moon." || got.Authors()[0] != "Ann Writer" {
		t.Errorf("by author: %+v ok=%v", got, ok)
	}
	if _, ok := ask(IDs{}, Hint{Title: "Moon Garden"}); ok {
		t.Error("a title alone chose a work")
	}
	if got, ok := ask(IDs{BookBrainz: "w-fr"}, Hint{Title: "Moon Garden"}); !ok || got.IDs.BookBrainz != "w-fr" {
		t.Errorf("stored id: %+v ok=%v", got.IDs, ok)
	}
}
