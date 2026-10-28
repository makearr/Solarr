package book

import (
	"slices"
	"testing"
)

func TestAnAuthorsOtherBooksAreTheirsAndNotHeld(t *testing.T) {
	works := AuthorWorks{Format: Ebook, Source: &fakeOpenLibrary{}}
	got, err := works.otherBooks(t.Context(), "Mara Quell", map[string]bool{foldTitle("The Lantern Keeper"): true})
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{}
	for _, one := range got {
		titles = append(titles, one.Title)
	}
	if want := []string{"The Glass Orchard"}; !slices.Equal(titles, want) {
		t.Errorf("other books = %v, want %v", titles, want)
	}
	if got[0].Kind != "book" || got[0].ExternalID != "OL1002W" || got[0].PosterURL == "" {
		t.Errorf("entry = %+v, want an Open Library book with its cover", got[0])
	}
}
