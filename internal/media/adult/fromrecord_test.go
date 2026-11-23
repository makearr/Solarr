package adult

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/specprovider"
)

type fakeRecords struct{ record specprovider.Record }

func (f fakeRecords) ByID(_ context.Context, namespace, id string) (specprovider.Record, error) {
	if namespace == "stashdb" && id == "s-1" {
		return f.record, nil
	}
	return specprovider.Record{}, errors.New("not found")
}

func (f fakeRecords) Search(_ context.Context, query specprovider.Query) ([]specprovider.Record, error) {
	if query.Title == "Invented Scene" {
		return []specprovider.Record{f.record}, nil
	}
	return nil, nil
}

func TestAskRecordMapsADefinitionsSceneByIDAndByTitle(t *testing.T) {
	record := specprovider.Record{
		Title: "Invented Scene", Date: "2021-05-04", Tags: []string{"Tag One"}, RuntimeSeconds: 60,
		Organisations: []specprovider.Organisation{{Name: "Invented Studio"}},
		People:        []specprovider.Person{{Name: "Alex Example"}, {Name: "Pat Dir", Role: "director"}},
		Images:        []specprovider.Image{{URL: "https://img.example/a.jpg"}},
	}
	service := Service{Records: func(key string) RecordProvider {
		if key == "def:mine" {
			return fakeRecords{record: record}
		}
		return nil
	}}
	byID, ok, err := service.askRecord(t.Context(), "def:mine", IDs{StashDB: "s-1"}, Hint{})
	if err != nil || !ok {
		t.Fatalf("by id: %v %v", ok, err)
	}
	want := Scene{
		Title: "Invented Scene", Date: "2021-05-04", Year: 2021, DurationSec: 60, Studio: "Invented Studio", Director: "Pat Dir", Tags: []string{"Tag One"},
		Images:  []Image{{Kind: "poster", URL: "https://img.example/a.jpg", Source: "def:mine"}},
		Credits: []Credit{{Provider: ProviderPerformerName, ExternalID: NameKey("Alex Example"), Name: "Alex Example", Role: RolePerformer}, {Provider: ProviderPerformerName, ExternalID: NameKey("Pat Dir"), Name: "Pat Dir", Role: RoleDirector, Order: 1}},
		Sources: []string{"def:mine"},
	}
	if !reflect.DeepEqual(byID, want) {
		t.Errorf("scene = %+v, want %+v", byID, want)
	}
	byTitle, ok, _ := service.askRecord(t.Context(), "def:mine", IDs{}, Hint{Title: "Invented Scene"})
	if !ok || byTitle.Title != "Invented Scene" {
		t.Errorf("by title: %v %+v", ok, byTitle)
	}
	if _, ok, _ := service.askRecord(t.Context(), "def:mine", IDs{}, Hint{Title: "Other"}); ok {
		t.Error("an unrelated title matched")
	}
}
