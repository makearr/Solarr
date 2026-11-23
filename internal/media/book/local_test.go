package book

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
)

type fakeLocal struct {
	item     mediaserver.Item
	pathItem mediaserver.Item
	question mediaserver.Question
}

func (f *fakeLocal) Ask(_ context.Context, _ int64, _, _ string, question mediaserver.Question) (mediaserver.Item, bool, error) {
	f.question = question
	return f.item, true, nil
}

func (f *fakeLocal) AskByPath(context.Context, int64, string) (mediaserver.Item, bool, error) {
	return f.pathItem, f.pathItem.Title != "", nil
}

func TestIDsByPathNamesABookByWhatTheServerHoldsOfItsFolder(t *testing.T) {
	local := &fakeLocal{pathItem: mediaserver.Item{Title: "The Lantern Keeper", IDs: map[string]string{"asin": "B000TEST01", "isbn": "9780000000002"}}}
	isbn, asin := Service{Format: Ebook, Local: local}.IDsByPath(t.Context(), 7, "/library/books/Mara Quell/The Lantern Keeper")
	if isbn != "9780000000002" || asin != "B000TEST01" {
		t.Errorf("ids = %q %q", isbn, asin)
	}
	if isbn, asin = (Service{Format: Ebook, Local: &fakeLocal{}}).IDsByPath(t.Context(), 7, "/x"); isbn != "" || asin != "" {
		t.Errorf("no match answered %q %q", isbn, asin)
	}
	if isbn, asin = (Service{Format: Ebook}).IDsByPath(t.Context(), 7, "/x"); isbn != "" || asin != "" {
		t.Errorf("no server answered %q %q", isbn, asin)
	}
}

func TestFetchForTakesAMediaServersBookWhereNoProviderKnowsItAndAsksByIDAndTitle(t *testing.T) {
	local := &fakeLocal{item: mediaserver.Item{Title: "The Lantern Keeper", Overview: "Home words.", Year: 2015, Genres: []string{"Fantasy"},
		IDs:    map[string]string{"asin": "B000TEST01", "isbn": "9780000000002"},
		People: []mediaserver.Credit{{Name: "Mara Quell", Kind: "author"}, {Name: "Jo Reed", Kind: "narrator"}}, Parent: &mediaserver.ItemRef{Title: "Lanterns #2"}}}
	service := Service{Format: Ebook, Local: local, PriorityFor: func(int64) []string { return []string{"local:audiobookshelf"} }}
	got, err := service.FetchFor(t.Context(), 7, IDs{ISBN: "9780000000001"}, Hint{Title: "The Lantern Keeper", Author: "Mara Quell"})
	if err != nil {
		t.Fatal(err)
	}
	want := Book{
		IDs: IDs{ISBN: "9780000000001", ASIN: "B000TEST01"}, Title: "The Lantern Keeper", Overview: "Home words.", Year: 2015, Genres: []string{"Fantasy"},
		Credits: []Credit{{Name: "Mara Quell", Role: RoleAuthor}, {Name: "Jo Reed", Role: RoleNarrator, Order: 1}},
		Series:  &Series{Key: "Lanterns", Name: "Lanterns", Position: "2"}, Sources: []string{"local:audiobookshelf"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("book = %+v, want %+v", got, want)
	}
	if wantQ := (mediaserver.Question{IDs: map[string]string{"isbn": "9780000000001", "asin": ""}, Title: "The Lantern Keeper", Author: "Mara Quell"}); !reflect.DeepEqual(local.question, wantQ) {
		t.Errorf("question = %+v, want %+v", local.question, wantQ)
	}
}
