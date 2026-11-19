package book

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/openlibrary"
)

func TestACreditCarriesTheAuthorsOpenLibraryKeyOnlyWhenTheNamesAndKeysPair(t *testing.T) {
	book := FromOpenLibrary(openlibrary.Work{}, openlibrary.Doc{
		Key: "/works/OL1001W", Title: "The Lantern Keeper", AuthorNames: []string{"Mara Quell", "Ivo Brandt"}, AuthorKeys: []string{"OL2001A", "OL2002A"},
	})
	if got := []string{book.Credits[0].OpenLibrary, book.Credits[1].OpenLibrary}; !slices.Equal(got, []string{"OL2001A", "OL2002A"}) {
		t.Errorf("keys = %v", got)
	}
	unpaired := FromOpenLibrary(openlibrary.Work{}, openlibrary.Doc{
		Key: "/works/OL1001W", Title: "The Lantern Keeper", AuthorNames: []string{"Mara Quell", "Ivo Brandt"}, AuthorKeys: []string{"OL2002A"},
	})
	if unpaired.Credits[0].OpenLibrary != "" || unpaired.Credits[1].OpenLibrary != "" {
		t.Errorf("unpaired keys = %+v, want none: which name a lone key belongs to is a guess", unpaired.Credits)
	}
	merged := mergeCredits([]Credit{{Name: "Mara Quell", Role: RoleAuthor, Image: "https://example.test/a.jpg"}}, book.Credits)
	if merged[0].OpenLibrary != "OL2001A" {
		t.Errorf("merged = %+v, want the second source's key for the same author", merged)
	}
	write := ToWrite(Ebook, book, time.Unix(0, 0))
	want := []string{"openlibrary:author", "OL2001A"}
	if ids := write.Credits[0].ExternalIDs; len(ids) != 1 || !slices.Equal([]string{ids[0].Provider, ids[0].ExternalID}, want) {
		t.Errorf("credit ids = %+v, want %v", ids, want)
	}
}

func TestAKeyedAuthorsBooksAreTheirsAloneAndRead(t *testing.T) {
	// The name search mixes two writers of one name; the key names one of them.
	source := &fakeOpenLibrary{works: []openlibrary.Doc{
		{Key: "/works/OL1101W", Title: "Tidewater", AuthorNames: []string{"M. Quell"}, AuthorKeys: []string{"OL2001A"}, FirstPublishYear: 2003},
	}}
	works := AuthorWorks{Format: Ebook, Source: source}
	got, err := works.otherBooks(t.Context(), "Mara Quell", "OL2001A", map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Tidewater" {
		t.Errorf("keyed books = %+v, want Tidewater alone: the key is exact, the spelling of the name is not", got)
	}
	if !slices.Equal(source.asked, []string{"key:OL2001A@0"}) {
		t.Errorf("asked %v, want the key alone", source.asked)
	}
}

func TestAProlificAuthorIsReadPageByPageWithinTheBound(t *testing.T) {
	source := &fakeOpenLibrary{}
	for index := range 250 {
		source.works = append(source.works, openlibrary.Doc{Key: fmt.Sprintf("/works/OL%dW", 3000+index), Title: fmt.Sprintf("Book %d", index),
			AuthorKeys: []string{"OL2001A"}, FirstPublishYear: 2020 - index/20})
	}
	works := AuthorWorks{Format: Ebook, Source: source}
	all, err := works.writtenBy(t.Context(), "Mara Quell", "OL2001A", authorQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 250 || !slices.Equal(source.asked, []string{"key:OL2001A@0", "key:OL2001A@100", "key:OL2001A@200"}) {
		t.Errorf("read %d books asking %v, want all 250 over three pages", len(all), source.asked)
	}

	source.asked = nil
	held := map[string]bool{}
	for index := range 130 {
		held[foldTitle(fmt.Sprintf("Book %d", index))] = true
	}
	others, err := works.otherBooks(t.Context(), "Mara Quell", "OL2001A", held)
	if err != nil {
		t.Fatal(err)
	}
	if len(others) != maxOtherWorks || others[0].Title != "Book 130" || len(source.asked) != 2 {
		t.Errorf("others = %d from %q asking %v, want %d unheld from Book 130 over two pages", len(others), others[0].Title, source.asked, maxOtherWorks)
	}

	source.asked = nil
	if _, err := works.writtenBy(t.Context(), "Mara Quell", "OL2001A", authorQuery{since: 2019}); err != nil {
		t.Fatal(err)
	}
	if len(source.asked) != 1 {
		t.Errorf("a follow from 2019 asked %v, want one page: the newest come first", source.asked)
	}

	for index := range 400 {
		source.works = append(source.works, openlibrary.Doc{Key: fmt.Sprintf("/works/OL%dW", 4000+index), Title: fmt.Sprintf("Late %d", index)})
	}
	source.asked = nil
	if _, err := works.writtenBy(t.Context(), "Mara Quell", "OL2001A", authorQuery{}); err != nil {
		t.Fatal(err)
	}
	if len(source.asked) != maxAuthorPages {
		t.Errorf("650 works asked %d pages, want the bound %d", len(source.asked), maxAuthorPages)
	}
}
