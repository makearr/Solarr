package book

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/fantlab"
	"github.com/makearr/Solarr/internal/metadata/hardcover"
	"github.com/makearr/Solarr/internal/metadata/itunes"
)

func TestTheNewProvidersAnswerABookEachWithItsOwnId(t *testing.T) {
	apple := FromITunes(itunes.Audiobook{
		CollectionID: 9, Name: "Awaken Online: Catharsis (Unabridged)", Author: "Travis Bagwell", ReleaseDate: "2016-08-26T07:00:00Z",
		Artwork100: "https://is1-ssl.mzstatic.com/x/100x100bb.jpg",
	})
	if apple.IDs.ITunes != "9" || apple.Title != "Awaken Online: Catharsis" || apple.Year != 2016 || apple.Images[0].URL != "https://is1-ssl.mzstatic.com/x/600x600bb.jpg" {
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
