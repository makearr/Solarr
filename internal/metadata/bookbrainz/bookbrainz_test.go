package bookbrainz_test

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/bookbrainz"
)

type fake struct{ address, body string }

func (f *fake) GetJSON(_ context.Context, address string, _ int64, into any) error {
	f.address = address
	return json.Unmarshal([]byte(f.body), into)
}

const sample = `{"total":3,"results":[
 {"bbid":"w-1","name":"Moon Garden","type":"Work","workType":"Novel","disambiguation":{"comment":"English title"},
  "defaultAlias":{"language":{"isoCode1":"en"}},"annotation":{"content":" A gardener finds the moon. "},
  "relationshipSet":{"relationships":[
    {"typeId":9,"source":{"name":"Tess Translator","type":"Author"}},
    {"typeId":8,"source":{"name":"Ann Writer","type":"Author"}},
    {"typeId":56,"source":{"name":"Moon Garden","type":"Work"}},
    {"typeId":10,"source":{"name":"Moon Garden","type":"Edition"}}]}},
 {"bbid":"e-2","name":"An Edition","type":"Edition"},
 {"bbid":"w-3","name":"  ","type":"Work"}]}`

func TestSearch_ReadsWorksWithTheirAuthorsOnly(t *testing.T) {
	source := &fake{body: sample}
	got, err := bookbrainz.New(source).Search(t.Context(), "moon garden", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []bookbrainz.Work{{BBID: "w-1", Name: "Moon Garden", Disambiguation: "English title", Type: "Novel", Language: "en",
		Annotation: "A gardener finds the moon.", Authors: []string{"Ann Writer"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("works = %+v, want %+v", got, want)
	}
	parsed, _ := url.Parse(source.address)
	if parsed.Path != "/search/search" || parsed.Query().Get("type") != "work" || parsed.Query().Get("q") != "moon garden" || parsed.Query().Get("size") != "5" {
		t.Errorf("asked %q", source.address)
	}
}
