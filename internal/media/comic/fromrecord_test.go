package comic

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/specprovider"
)

type fakeRecords struct{ found []specprovider.Record }

func (fakeRecords) Namespace() string { return "gallerysite" }
func (f fakeRecords) Search(context.Context, specprovider.Query) ([]specprovider.Record, error) {
	return f.found, nil
}
func (f fakeRecords) Item(_ context.Context, id string) (specprovider.Record, error) {
	for _, record := range f.found {
		if record.IDs["gallerysite"] == id {
			return record, nil
		}
	}
	return specprovider.Record{}, context.Canceled
}

func TestADefinitionRecordFillsASeriesAfterACompiledProviderAndNeverOverwritesIt(t *testing.T) {
	record := specprovider.Record{
		IDs: map[string]string{"gallerysite": "77", "anilist": "9"}, Title: "Other Title", Overview: "A gallery.", Year: 2020, PageCount: 24, Adult: true,
		Genres: []string{"romance"}, Tags: []string{"group:circle"}, People: []specprovider.Person{{Name: "Ann Artist", Role: "art"}},
		Organisations: []specprovider.Organisation{{Name: "Circle Press", Role: "publisher"}},
	}
	sources := Sources{Records: func(key string) RecordProvider {
		if key == "mysite" {
			return fakeRecords{found: []specprovider.Record{record}}
		}
		return nil
	}}
	series := Series{IDs: map[string]string{}, Title: "Held Title"}
	if !sources.askRecords(context.Background(), &series, "mysite", Hint{Title: "Held Title"}) {
		// The held title differs from the record's, so nothing matches by title; ask by id as a refresh does.
		series.IDs["mysite"] = "77"
		if !sources.askRecords(context.Background(), &series, "mysite", Hint{}) {
			t.Fatal("the record by id was not read")
		}
	}
	want := Series{
		Title: "Held Title", Overview: "A gallery.", Publisher: "Circle Press", Format: FormatGallery, Year: 2020, Adult: true,
		IDs:     map[string]string{"mysite": "77", "anilist": "9"},
		Genres:  []string{"romance", "group:circle"},
		Credits: []Credit{{Name: "Ann Artist", Role: "art"}},
		Parts:   []Part{{Kind: PartVolume, Number: "1", Pages: 24}},
		Sources: []string{"mysite"},
	}
	if !reflect.DeepEqual(series, want) {
		t.Fatalf("series = %+v\nwant %+v", series, want)
	}
}

func TestADefinitionSearchStoresItsOwnIDUnderTheDefinitionKey(t *testing.T) {
	sources := Sources{Records: func(string) RecordProvider {
		return fakeRecords{found: []specprovider.Record{{IDs: map[string]string{"gallerysite": "5"}, Title: "Found", Year: 2021}}}
	}}
	got := sources.searchRecords(context.Background(), "mysite", "found")
	want := []Candidate{{Title: "Found", Year: 2021, IDs: map[string]string{"mysite": "5"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %+v want %+v", got, want)
	}
}
