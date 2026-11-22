package adult

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
)

type fakeLocal struct{ item mediaserver.Item }

func (f fakeLocal) Ask(context.Context, int64, string, string, mediaserver.Question) (mediaserver.Item, bool, error) {
	return f.item, true, nil
}

func (f fakeLocal) AskByPath(context.Context, int64, string) (mediaserver.Item, bool, error) {
	return mediaserver.Item{}, false, nil
}

func TestAskLocalMapsTheSceneAndKeepsTheIDsAlreadyKnown(t *testing.T) {
	service := Service{Local: fakeLocal{item: mediaserver.Item{Title: "Invented Scene", Overview: "Words.", Date: "2021-05-04", Year: 2021, RuntimeMS: 61500,
		IDs: map[string]string{"stashdb": "other", "fansdb": "fans-1", "code": "X-1"}, Organisations: []string{"Invented Studio"}, Tags: []string{"Tag One"},
		People: []mediaserver.Credit{{Name: "Alex Example", Kind: "actor"}, {Name: "Pat Dir", Kind: "director"}}}}}
	got, ok, err := service.askLocal(t.Context(), 4, "local:stash", IDs{StashDB: "mine"}, Hint{Title: "Invented Scene"})
	want := Scene{
		IDs: IDs{FansDB: "fans-1"}, Title: "Invented Scene", Overview: "Words.", Date: "2021-05-04", Year: 2021, DurationSec: 61, Studio: "Invented Studio",
		Director: "Pat Dir", Code: "X-1", Tags: []string{"Tag One"}, Credits: []Credit{{Name: "Alex Example", Role: RolePerformer}}, Sources: []string{"local:stash"},
	}
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("scene = %+v, %v, %v; want %+v", got, ok, err, want)
	}
	if _, ok, _ := (Service{}).askLocal(t.Context(), 4, "local:stash", IDs{}, Hint{}); ok {
		t.Error("a service with no media servers answered")
	}
}
