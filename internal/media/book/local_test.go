package book

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
)

type fakeLocal struct {
	item     mediaserver.Item
	question mediaserver.Question
}

func (f *fakeLocal) Ask(_ context.Context, _ int64, _, _ string, question mediaserver.Question) (mediaserver.Item, bool, error) {
	f.question = question
	return f.item, true, nil
}

func (f *fakeLocal) AskByPath(context.Context, int64, string) (mediaserver.Item, bool, error) {
	return mediaserver.Item{}, false, nil
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
