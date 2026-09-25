package theaudiodb_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/theaudiodb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type cannedTransport struct {
	body      string
	requested []string
}

func (c *cannedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	c.requested = append(c.requested, request.URL.String())
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(c.body)), Request: request}, nil
}

func newClient(t *testing.T, transport http.RoundTripper, key string) theaudiodb.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	client := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: "theaudiodb-test", UserAgent: "test", Transport: transport})
	return theaudiodb.New(client, func() string { return key })
}

// Shapes as the live API answered them 2026-09-25 (Heka, Dumucamoko), trimmed to what is read.
func TestArtistAndAlbumCover(t *testing.T) {
	artist := &cannedTransport{body: `{"artists":[{"strArtist":"Heka","strArtistThumb":"https://r2.theaudiodb.com/a/thumb.jpg",` +
		`"strArtistFanart":"https://r2.theaudiodb.com/a/fanart.jpg","strArtistLogo":"https://r2.theaudiodb.com/a/logo.png","strBiographyEN":null}]}`}
	got, err := newClient(t, artist, "123").Artist(t.Context(), "9546cacb-d515-4327-878c-de2125eb70af")
	if err != nil {
		t.Fatalf("Artist: %v", err)
	}
	if want := (theaudiodb.Artist{Photo: "https://r2.theaudiodb.com/a/thumb.jpg", Fanart: "https://r2.theaudiodb.com/a/fanart.jpg",
		Logo: "https://r2.theaudiodb.com/a/logo.png"}); got != want {
		t.Errorf("Artist = %+v, want %+v", got, want)
	}
	if want := "https://www.theaudiodb.com/api/v1/json/123/artist-mb.php?i=9546cacb-d515-4327-878c-de2125eb70af"; artist.requested[0] != want {
		t.Errorf("asked %s", artist.requested[0])
	}

	album := &cannedTransport{body: `{"album":[{"strAlbum":"Dumucamoko","strAlbumThumb":"https://r2.theaudiodb.com/b/thumb.jpg"}]}`}
	cover, err := newClient(t, album, "123").AlbumCover(t.Context(), "a1f4688e-c580-464f-8bfa-9c11b54e954e")
	if err != nil || cover != "https://r2.theaudiodb.com/b/thumb.jpg" {
		t.Errorf("AlbumCover = %q, %v", cover, err)
	}

	unknown := &cannedTransport{body: `{"artists":null}`}
	if _, err := newClient(t, unknown, "123").Artist(t.Context(), "x"); !errors.Is(err, theaudiodb.ErrNotFound) {
		t.Errorf("unknown artist: err = %v, want ErrNotFound", err)
	}
}
