package specprovider_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/fantlab"
	"github.com/makearr/Solarr/internal/metadata/itunes"
	"github.com/makearr/Solarr/internal/metadata/specprovider"
	"github.com/makearr/Solarr/internal/platform/clock"
)

const itunesAnswer = `{"resultCount":1,"results":[{"collectionId":4242,"collectionName":"The Lantern Road (Unabridged)","artistName":"Ann Narrator","description":"A road lit by lanterns.","releaseDate":"2019-05-14T07:00:00Z","artworkUrl100":"https://img.example.org/a/100x100bb.jpg","primaryGenreName":"Fantasy"}]}`

// goGetter feeds the compiled client the same recorded answer the YAML port reads.
type goGetter struct{}

func (goGetter) GetJSON(_ context.Context, _ string, _ int64, into any) error {
	return jsonInto([]byte(itunesAnswer), into)
}

func TestTheYAMLPortOfTheITunesClientMapsTheSameAnswerToTheSameValues(t *testing.T) {
	compiled, err := itunes.New(goGetter{}).Lookup(context.Background(), 4242)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/itunes.yaml")
	if err != nil {
		t.Fatal(err)
	}
	def, err := specprovider.Parse("itunes-port", "iTunes", data)
	if err != nil {
		t.Fatal(err)
	}
	provider := specprovider.New(def, &fake{answers: map[string]string{"lookup": itunesAnswer}}, clock.NewFakeAtEpoch(), func() map[string]string { return nil })
	record, err := provider.Item(context.Background(), "4242")
	if err != nil {
		t.Fatal(err)
	}
	got := struct {
		Title, Author, Cover, Genre, Overview string
		Year                                  int
	}{record.Title, record.People[0].Name, record.Images[0].URL, record.Genres[0], record.Overview, record.Year}
	want := struct {
		Title, Author, Cover, Genre, Overview string
		Year                                  int
	}{compiled.Title(), compiled.Author, compiled.Cover(), compiled.Genre, compiled.Description, compiled.Year()}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("port = %+v\ncompiled = %+v", got, want)
	}
}

func jsonInto(data []byte, into any) error { return json.Unmarshal(data, into) }

const fantlabAnswer = `{"work_id":901,"work_name":"Lantern Road","work_name_orig":"The Lantern Road","work_description":"A road lit by lanterns.","work_year":2001,"image":"/images/editions/big/9/901?r=55","authors":[{"name":"Ann Writer","name_orig":"Ann Writer"}]}`

type fantlabGetter struct{}

func (fantlabGetter) GetJSON(_ context.Context, _ string, _ int64, into any) error {
	return jsonInto([]byte(fantlabAnswer), into)
}

func TestTheYAMLPortOfTheFantLabClientMapsTheSameAnswerToTheSameValues(t *testing.T) {
	compiled, err := fantlab.New(fantlabGetter{}).Work(context.Background(), 901)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/fantlab.yaml")
	if err != nil {
		t.Fatal(err)
	}
	def, err := specprovider.Parse("fantlab-port", "FantLab", data)
	if err != nil {
		t.Fatal(err)
	}
	provider := specprovider.New(def, &fake{answers: map[string]string{"/extended": fantlabAnswer}}, clock.NewFakeAtEpoch(), func() map[string]string { return nil })
	record, err := provider.Item(context.Background(), "901")
	if err != nil {
		t.Fatal(err)
	}
	type values struct {
		Title, Original, Overview, Author, Cover string
		Year                                     int
	}
	got := values{record.Title, record.OriginalTitle, record.Overview, record.People[0].Name, record.Images[0].URL, record.Year}
	want := values{compiled.Name, compiled.Original, compiled.Description, compiled.Authors[0].Name, compiled.Cover(), compiled.Year}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("port = %+v\ncompiled = %+v", got, want)
	}
}
