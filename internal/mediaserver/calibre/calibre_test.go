package calibre_test

import (
	"crypto/md5" //nolint:gosec // the test recomputes the digest the client must send
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/mediaserver/calibre"
	"github.com/makearr/Solarr/internal/mediaserver/mediaservertest"
)

func md5hex(text string) string {
	sum := md5.Sum([]byte(text)) //nolint:gosec // see import
	return hex.EncodeToString(sum[:])
}

func TestClient_AnswersADigestChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		if authorization == "" {
			w.Header().Set("WWW-Authenticate", `Digest realm="calibre", nonce="n0nce", qop="auth", opaque="op", algorithm=MD5`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		ha1, ha2 := md5hex("reader:calibre:pw"), md5hex("GET:/ajax/library-info")
		want := md5hex(strings.Join([]string{ha1, "n0nce", "00000001", "cn", "auth", ha2}, ":"))
		if !strings.Contains(authorization, `response="`+want+`"`) || !strings.HasPrefix(authorization, "Digest ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"library_map":{"shelf":"Shelf","extra":"Extra"},"default_library":"shelf"}`))
	}))
	defer server.Close()
	client := calibre.New(mediaservertest.Direct(), server.URL, "reader", "pw", func() string { return "cn" })
	libraries, err := client.Libraries(t.Context())
	want := []mediaserver.Library{{ID: "extra", Name: "Extra", Type: "book"}, {ID: "shelf", Name: "Shelf", Type: "book"}}
	if err != nil || !reflect.DeepEqual(libraries, want) {
		t.Errorf("libraries = %+v, err %v; want %+v", libraries, err, want)
	}
}

func TestClient_WithoutAUserNameSendsNothing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected Authorization %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"library_map":{"a":"A"}}`))
	}))
	defer server.Close()
	info, err := calibre.New(mediaservertest.Direct(), server.URL, "", "", nil).Test(t.Context())
	if err != nil || info.Name != "Calibre content server" {
		t.Errorf("info = %+v, err %v", info, err)
	}
}

func TestClient_ItemByProviderIDSearchesTheIdentifierAndReadsTheBook(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/ajax/search":
			if r.URL.Query().Get("query") != "identifiers:amazon:B000TEST01" {
				_, _ = w.Write([]byte(`{"book_ids":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"book_ids":[3]}`))
		case "/ajax/books":
			_, _ = w.Write([]byte(`{"3":{"title":"The Lantern Keeper","authors":["Mara Quell"],"comments":"<div><p>Home words.</p></div>","tags":["Fantasy"],"publisher":"Invented House","languages":["eng"],"pubdate":"2015-04-02T00:00:00+00:00","series":"Lanterns","series_index":2.0,"identifiers":{"isbn":"9780000000002","amazon":"B000TEST01"}}}`))
		}
	}))
	defer server.Close()
	client := calibre.New(mediaservertest.Direct(), server.URL, "", "", func() string { return "c" })
	got, found, err := client.ItemByProviderID(t.Context(), "book", "asin", "B000TEST01")
	want := mediaserver.Item{
		ID: "3", MediaType: "book", Title: "The Lantern Keeper", Overview: "Home words.", Year: 2015, Date: "2015-04-02", Genres: []string{"Fantasy"}, Language: "eng",
		Organisations: []string{"Invented House"}, Parent: &mediaserver.ItemRef{Title: "Lanterns #2"},
		People: []mediaserver.Credit{{Name: "Mara Quell", Kind: "author"}},
		IDs:    map[string]string{"isbn": "9780000000002", "amazon": "B000TEST01", "asin": "B000TEST01"},
	}
	if err != nil || !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("item = %+v, %v, %v; queries %q", got, found, err, queries)
	}
	if _, found, _ := client.ItemByProviderID(t.Context(), "book", "isbn", "0"); found {
		t.Error("an unknown identifier was found")
	}
}
