package specprovider_test

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/specprovider"
	"github.com/makearr/Solarr/internal/platform/clock"
)

const galleryListing = `<table class="itg gltc"><tr><td class="gl1c glcat"><div class="cn ct1">Misc</div></td>
<td class="gl3c glname"><a href="https://e-hentai.org/g/1234567/abcdef0123/"><div class="glink">[Circle] Sample Gallery One</div><div><div class="gt" title="parody:sample">sample</div></div></a></td></tr>
<tr><td class="gl3c glname"><a href="https://e-hentai.org/g/7654321/0123abcdef/"><div class="glink">[Circle] Sample Gallery Two</div></a></td></tr></table>`

const galleryData = `{"gmetadata":[{"gid":1234567,"token":"abcdef0123","title":"[Circle] Sample Gallery One","title_jpn":"","category":"Misc","thumb":"https://thumbs.example.org/one.jpg","filecount":"32","rating":"4.50","tags":["parody:sample","artist:ann artist","female:shy"]}]}`

func TestTheShippedGalleryDefinitionsReadAListingAndAGalleryIntoRecords(t *testing.T) {
	files := specprovider.Builtin()
	for _, key := range []string{"ehentai", "exhentai"} {
		if _, ok := files[key]; !ok {
			t.Fatalf("%s is not shipped", key)
		}
		if _, err := specprovider.Parse(key, key, files[key]); err != nil {
			t.Fatalf("%s does not parse: %v", key, err)
		}
	}
	def, err := specprovider.Parse("ehentai", "e-Hentai", files["ehentai"])
	if err != nil {
		t.Fatal(err)
	}
	doer := &fake{answers: map[string]string{"f_search=": galleryListing, "api.php": galleryData}}
	provider := specprovider.New(def, doer, clock.NewFakeAtEpoch(), func() map[string]string { return nil })
	found, err := provider.Search(context.Background(), specprovider.Query{Title: "sample"})
	if err != nil {
		t.Fatal(err)
	}
	var ids, titles []string
	for _, record := range found {
		ids, titles = append(ids, record.IDs["ehentai"]), append(titles, record.Title)
	}
	if !reflect.DeepEqual(ids, []string{"1234567/abcdef0123", "7654321/0123abcdef"}) || !reflect.DeepEqual(titles, []string{"[Circle] Sample Gallery One", "[Circle] Sample Gallery Two"}) {
		t.Fatalf("search = %v %v", ids, titles)
	}
	item, err := provider.Item(context.Background(), "1234567/abcdef0123")
	if err != nil {
		t.Fatal(err)
	}
	want := specprovider.Record{
		IDs: map[string]string{}, Title: "[Circle] Sample Gallery One", Genres: []string{"Misc"}, Tags: []string{"parody:sample", "artist:ann artist", "female:shy"},
		PageCount: 32, Adult: true, Rating: specprovider.Rating{Value: 4.5},
		Images: []specprovider.Image{{Kind: "cover", URL: "https://thumbs.example.org/one.jpg"}},
	}
	if !reflect.DeepEqual(item, want) {
		t.Fatalf("item = %+v\nwant %+v", item, want)
	}
	last := doer.requests[len(doer.requests)-1]
	if body := readBody(t, last); body != `{"method":"gdata","gidlist":[["1234567","abcdef0123"]],"namespace":1}` {
		t.Fatalf("gdata body = %s", body)
	}
}

func readBody(t *testing.T, request *http.Request) string {
	t.Helper()
	body, err := request.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(body)
	return string(data)
}
