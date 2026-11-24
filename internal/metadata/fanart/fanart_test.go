package fanart_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/fanart"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct{}

func (answer) RoundTrip(request *http.Request) (*http.Response, error) {
	status, body := http.StatusNotFound, `{"status":"error","error message":"Not found"}`
	if request.URL.Path == "/v3/movies/7001" && request.URL.Query().Get("api_key") == "owner-key" {
		status, body = http.StatusOK, `{"name":"Example Film","hdmovielogo":[{"id":"1","url":"https://assets.fanart.tv/logo.png","lang":"en","likes":"4"}],
		  "moviedisc":[{"id":"2","url":"https://assets.fanart.tv/disc.png","lang":"en","likes":"1","disc":"1","disc_type":"bluray"}]}`
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestAFilmsPicturesAreReadByTMDBID(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	http := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}), httpx.ClientConfig{Provider: "fanart", UserAgent: "test", Transport: answer{}})
	pictures := fanart.New(http, func() string { return "owner-key" })
	movie, err := pictures.Movie(t.Context(), 7001, "")
	if err != nil || len(movie.Logos) != 1 || movie.Logos[0].URL != "https://assets.fanart.tv/logo.png" || len(movie.Discs) != 1 {
		t.Fatalf("movie = %+v, %v", movie, err)
	}
	if _, err := pictures.Movie(t.Context(), 1, ""); !errors.Is(err, fanart.ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}

func TestBestPrefersEnglishThenLikes(t *testing.T) {
	best, ok := fanart.Best([]fanart.Image{
		{URL: "de", Lang: "de", Likes: "99"}, {URL: "none", Lang: "00", Likes: "50"}, {URL: "en-few", Lang: "en", Likes: "1"}, {URL: "en-many", Lang: "en", Likes: "7"},
	})
	if !ok || best.URL != "en-many" {
		t.Fatalf("best = %+v, %v", best, ok)
	}
	if _, ok := fanart.Best(nil); ok {
		t.Error("no pictures gave a best one")
	}
}

type extended struct{}

func (extended) RoundTrip(request *http.Request) (*http.Response, error) {
	bodies := map[string]string{
		"/v3/tv/555":                  `{"name":"Example Show","hdtvlogo":[{"url":"https://assets.fanart.tv/tvlogo.png","lang":"en","likes":"2"}],"seasonposter":[{"url":"https://assets.fanart.tv/s1.jpg","lang":"en","likes":"1","season":"1"}]}`,
		"/v3/music/mbid-artist":       `{"name":"Example Band","artistbackground":[{"url":"https://assets.fanart.tv/bg.jpg","likes":"3"}],"hdmusiclogo":[{"url":"https://assets.fanart.tv/ml.png","likes":"1"}]}`,
		"/v3/music/albums/mbid-group": `{"name":"Example Band","albums":{"mbid-group":{"albumcover":[{"url":"https://assets.fanart.tv/cover.jpg","likes":"5"}],"cdart":[{"url":"https://assets.fanart.tv/cd.png","likes":"1","disc":"1","size":"1000"}]}}}`,
	}
	status, body := http.StatusNotFound, `{}`
	if text, found := bodies[request.URL.Path]; found && request.URL.Query().Get("api_key") == "owner-key" {
		status, body = http.StatusOK, text
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestShowsArtistsAndAlbumsHaveTheirPictures(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	client := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}), httpx.ClientConfig{Provider: "fanart", UserAgent: "test", Transport: extended{}})
	pictures := fanart.New(client, func() string { return "owner-key" })
	show, err := pictures.Show(t.Context(), 555)
	if err != nil || show.Name != "Example Show" || len(show.Logos) != 1 || len(show.SeasonPoster) != 1 {
		t.Fatalf("show = %+v, %v", show, err)
	}
	artist, err := pictures.Artist(t.Context(), "mbid-artist")
	if err != nil || len(artist.Backgrounds) != 1 || len(artist.Logos) != 1 {
		t.Fatalf("artist = %+v, %v", artist, err)
	}
	album, err := pictures.Album(t.Context(), "mbid-group")
	if err != nil || len(album.Albums["mbid-group"].Covers) != 1 || len(album.Albums["mbid-group"].Discs) != 1 {
		t.Fatalf("album = %+v, %v", album, err)
	}
	if _, err := pictures.Show(t.Context(), 9); !errors.Is(err, fanart.ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}
