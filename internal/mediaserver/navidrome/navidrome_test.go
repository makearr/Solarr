package navidrome_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/mediaserver/mediaservertest"
	"github.com/makearr/Solarr/internal/mediaserver/navidrome"
)

func TestClient_TestAndLibraries(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/rest/ping":
			_, _ = w.Write([]byte(`{"subsonic-response":{"status":"ok","version":"1.16.1","serverVersion":"0.54.0"}}`))
		case "/rest/getMusicFolders":
			_, _ = w.Write([]byte(`{"subsonic-response":{"status":"ok","musicFolders":{"musicFolder":[{"id":1,"name":"Music"}]}}}`))
		}
	}))
	defer server.Close()
	client := navidrome.New(mediaservertest.Direct(), server.URL, "guest", "sesame", func() string { return "c19b2d" })

	info, err := client.Test(t.Context())
	if err != nil || info != (mediaserver.Info{Name: "Navidrome", Version: "0.54.0"}) {
		t.Errorf("info = %+v, err %v", info, err)
	}
	libraries, _ := client.Libraries(t.Context())
	if want := []mediaserver.Library{{ID: "1", Name: "Music", Type: "music"}}; !reflect.DeepEqual(libraries, want) {
		t.Errorf("libraries = %+v, want %+v", libraries, want)
	}
	// md5("sesame" + "c19b2d"); the password itself is never in the request.
	wantQuery := "/rest/ping?c=solarr&f=json&s=c19b2d&t=26719a1196d2a940705a59634eb18eab&u=guest&v=1.16.1"
	if queries[0] != wantQuery {
		t.Errorf("first request = %q, want %q", queries[0], wantQuery)
	}
}

func TestClient_ABadPasswordIsRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"subsonic-response":{"status":"failed","error":{"code":40,"message":"Wrong username or password"}}}`))
	}))
	defer server.Close()
	_, err := navidrome.New(mediaservertest.Direct(), server.URL, "guest", "x", func() string { return "s" }).Test(t.Context())
	if !errors.Is(err, mediaserver.ErrRejected) {
		t.Errorf("err = %v, want ErrRejected", err)
	}
}

func TestClient_ChangedStartsAnIncrementalScanUnlessOneRuns(t *testing.T) {
	scanning := false
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+" fullScan="+r.URL.Query().Get("fullScan"))
		if r.URL.Path == "/rest/getScanStatus" && scanning {
			_, _ = w.Write([]byte(`{"subsonic-response":{"status":"ok","scanStatus":{"scanning":true}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"subsonic-response":{"status":"ok","scanStatus":{"scanning":false}}}`))
	}))
	defer server.Close()
	client := navidrome.New(mediaservertest.Direct(), server.URL, "guest", "x", func() string { return "s" })
	if err := client.Changed(t.Context(), nil); err != nil {
		t.Fatalf("Changed: %v", err)
	}
	if want := []string{"/rest/getScanStatus fullScan=", "/rest/startScan fullScan=false"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("requests = %q, want %q", paths, want)
	}
	scanning = true
	if err := client.Changed(t.Context(), nil); !errors.Is(err, mediaserver.ErrBusy) {
		t.Errorf("Changed while scanning = %v, want ErrBusy", err)
	}
}

func TestClient_ItemReadsAnAlbumWithItsReleaseMBIDAndSearchFindsIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/getAlbum":
			_, _ = io.WriteString(w, `{"subsonic-response":{"status":"ok","album":{"id":"a1","name":"Invented Album","artist":"Invented Band","year":1999,"duration":120,"musicBrainzId":"11111111-2222-3333-4444-555555555555","genres":[{"name":"Rock"}],"recordLabels":[{"name":"Invented Label"}],"releaseDate":{"year":1999,"month":3,"day":9},"song":[]}}}`)
		case "/rest/search3":
			_, _ = io.WriteString(w, `{"subsonic-response":{"status":"ok","searchResult3":{"album":[{"id":"a1","name":"Invented Album"}]}}}`)
		}
	}))
	defer server.Close()
	client := navidrome.New(mediaservertest.Direct(), server.URL, "u", "p", func() string { return "s" })
	got, err := client.Item(t.Context(), "a1")
	want := mediaserver.Item{
		ID: "a1", MediaType: "music", Title: "Invented Album", Year: 1999, Date: "1999-03-09", RuntimeMS: 120000, Genres: []string{"Rock"}, Organisations: []string{"Invented Label"},
		People: []mediaserver.Credit{{Name: "Invented Band", Kind: "artist"}}, IDs: map[string]string{"musicbrainzalbum": "11111111-2222-3333-4444-555555555555"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("item = %+v, %v", got, err)
	}
	if found, _ := client.Search(t.Context(), "music", "invented", 5); len(found) != 1 || found[0].ID != "a1" {
		t.Errorf("search = %+v", found)
	}
}
