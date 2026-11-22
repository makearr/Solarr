package navidrome_test

import (
	"errors"
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
