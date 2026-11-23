package spotify_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/spotify"
)

type server struct{ asked []string }

func (s *server) Do(_ context.Context, request *http.Request) (*http.Response, error) {
	s.asked = append(s.asked, request.Method+" "+request.URL.Path+" "+request.Header.Get("Authorization"))
	body := `{"access_token":"tok","expires_in":3600}`
	switch {
	case strings.HasSuffix(request.URL.Path, "/search"):
		body = `{"albums":{"items":[{"id":"x1","name":"Tidal Maps","artists":[{"name":"Other"}]},{"id":"a1","name":"Tidal Maps","artists":[{"name":"Ada Quill"}]}]}}`
	case strings.Contains(request.URL.Path, "/albums/"):
		body = `{"id":"a1","popularity":61,"external_ids":{"upc":"0123"},"images":[{"url":"small","width":64},{"url":"big","width":640}]}`
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestFindAlbumGetsATokenOnceAndReadsPopularity(t *testing.T) {
	doer := &server{}
	at := time.Unix(1000, 0)
	client := spotify.New(doer, func() string { return "id:secret" }, func() time.Time { return at })
	for range 2 {
		got, err := client.FindAlbum(t.Context(), "Ada Quill", "Tidal Maps")
		if want := (spotify.Album{ID: "a1", Popularity: 61, Cover: "big", UPC: "0123"}); err != nil || got != want {
			t.Fatalf("FindAlbum = %+v, %v", got, err)
		}
	}
	tokens := 0
	for _, line := range doer.asked {
		if strings.HasPrefix(line, "POST /api/token Basic ") {
			tokens++
		}
	}
	if tokens != 1 || doer.asked[1] != "GET /v1/search Bearer tok" {
		t.Errorf("asked %v", doer.asked)
	}
}

func TestFindAlbumWithoutCredentialsAsksNothing(t *testing.T) {
	doer := &server{}
	client := spotify.New(doer, func() string { return "" }, time.Now)
	if _, err := client.FindAlbum(t.Context(), "A", "B"); !errors.Is(err, spotify.ErrNoKey) || len(doer.asked) != 0 || client.Configured() {
		t.Errorf("err = %v asked %v", err, doer.asked)
	}
}
