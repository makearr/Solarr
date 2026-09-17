package musicbrainz_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/httpx"
)

// okComputerCoverArtBody is a verbatim capture of coverartarchive.org's real
// response for release d319c059-baaa-4fc8-8ae2-cc3035c5fd19, fetched live
// while designing this package -- two images, only the second in array order
// is front+approved, which is what proves the preference filter runs rather
// than just returning images[0].
const okComputerCoverArtBody = `{
  "images": [
    {
      "approved": true, "back": false, "front": false,
      "id": 30730533321,
      "image": "https://coverartarchive.org/release/d319c059-baaa-4fc8-8ae2-cc3035c5fd19/30730533321.jpg",
      "types": ["Back"]
    },
    {
      "approved": true, "back": false, "front": true,
      "id": 43287476628,
      "image": "https://coverartarchive.org/release/d319c059-baaa-4fc8-8ae2-cc3035c5fd19/43287476628.png",
      "types": ["Front"]
    }
  ],
  "release": "https://musicbrainz.org/release/d319c059-baaa-4fc8-8ae2-cc3035c5fd19"
}`

func newCoverArtServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// newCoverArtClient builds a client whose guard allows serverURL's own host --
// the real guard refuses loopback (httptest.Server's address) by design, so
// the test server's host must be on the allowlist rather than trying to
// disable the guard.
func newCoverArtClient(t *testing.T, serverURL string) *httpx.Client {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", serverURL, err)
	}
	fake := clock.NewFakeAtEpoch()
	guard := httpx.NewGuard(nil, nil, []string{parsed.Hostname()})
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return httpx.NewClient(fake, guard, limiter, breaker, httpx.ClientConfig{
		Provider: musicbrainz.CoverArtName, UserAgent: "test",
	})
}

func TestCoverArt_FetchPrefersFrontApprovedOverArrayOrder(t *testing.T) {
	server := newCoverArtServer(t, http.StatusOK, okComputerCoverArtBody)
	client := newCoverArtClient(t, server.URL)
	coverArt := musicbrainz.NewCoverArt(client, server.URL)

	image, err := coverArt.Fetch(t.Context(), "d319c059-baaa-4fc8-8ae2-cc3035c5fd19")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := "https://coverartarchive.org/release/d319c059-baaa-4fc8-8ae2-cc3035c5fd19/43287476628.png"
	if image != want {
		t.Errorf("Fetch = %q, want the front+approved image %q, not array order", image, want)
	}
}

func TestCoverArt_FetchFallsBackToAnyImageWhenNoneIsFrontApproved(t *testing.T) {
	const body = `{"images": [{"approved": false, "front": false, "image": "https://coverartarchive.org/unapproved.jpg"}]}`
	server := newCoverArtServer(t, http.StatusOK, body)
	client := newCoverArtClient(t, server.URL)
	coverArt := musicbrainz.NewCoverArt(client, server.URL)

	image, err := coverArt.Fetch(t.Context(), "any-release")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if image != "https://coverartarchive.org/unapproved.jpg" {
		t.Errorf("Fetch = %q, want the sole image as fallback", image)
	}
}

func TestCoverArt_FetchReleaseGroupAsksForTheReleaseGroup(t *testing.T) {
	var requested string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		_, _ = w.Write([]byte(okComputerCoverArtBody))
	}))
	t.Cleanup(server.Close)
	coverArt := musicbrainz.NewCoverArt(newCoverArtClient(t, server.URL), server.URL)

	image, err := coverArt.FetchReleaseGroup(t.Context(), "04e9a8bd-175f-44a1-83a0-17d5e9fbd116")
	if err != nil {
		t.Fatalf("FetchReleaseGroup: %v", err)
	}
	if requested != "/release-group/04e9a8bd-175f-44a1-83a0-17d5e9fbd116" {
		t.Errorf("requested %q, want the release-group endpoint", requested)
	}
	const wantImage = "https://coverartarchive.org/release/d319c059-baaa-4fc8-8ae2-cc3035c5fd19/43287476628.png"
	if image != wantImage {
		t.Errorf("image = %q, want %q", image, wantImage)
	}
}

// Detisisu Binip' real front image, captured 2026-09-17, trimmed to one image.
const shepherdMoonsCoverArtBody = `{"images": [{"approved": true, "front": true,
  "image": "http://coverartarchive.org/release/6bd24988-6767-4089-8d3d-f6efdcf7e624/34783123300.jpg",
  "thumbnails": {
    "1200": "http://coverartarchive.org/release/6bd24988-6767-4089-8d3d-f6efdcf7e624/34783123300-1200.jpg",
    "250": "http://coverartarchive.org/release/6bd24988-6767-4089-8d3d-f6efdcf7e624/34783123300-250.jpg",
    "500": "http://coverartarchive.org/release/6bd24988-6767-4089-8d3d-f6efdcf7e624/34783123300-500.jpg",
    "large": "http://coverartarchive.org/release/6bd24988-6767-4089-8d3d-f6efdcf7e624/34783123300-500.jpg",
    "small": "http://coverartarchive.org/release/6bd24988-6767-4089-8d3d-f6efdcf7e624/34783123300-250.jpg"}}]}`

// The original upload is shrunk to 1000px anyway; the 1200 thumbnail is enough
// and far smaller to fetch.
func TestCoverArt_FetchPrefersTheLargeThumbnailOverTheOriginal(t *testing.T) {
	server := newCoverArtServer(t, http.StatusOK, shepherdMoonsCoverArtBody)
	coverArt := musicbrainz.NewCoverArt(newCoverArtClient(t, server.URL), server.URL)

	image, err := coverArt.Fetch(t.Context(), "6bd24988-6767-4089-8d3d-f6efdcf7e624")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	const want = "http://coverartarchive.org/release/6bd24988-6767-4089-8d3d-f6efdcf7e624/34783123300-1200.jpg"
	if image != want {
		t.Errorf("image = %q, want the 1200 thumbnail %q", image, want)
	}
}

func TestCoverArt_FetchReturnsErrNoCoverArtOn404(t *testing.T) {
	server := newCoverArtServer(t, http.StatusNotFound,
		"<!doctype html><title>404 Not Found</title><h1>Not Found</h1>")
	client := newCoverArtClient(t, server.URL)
	coverArt := musicbrainz.NewCoverArt(client, server.URL)

	_, err := coverArt.Fetch(t.Context(), "23724534-80a4-43fc-8172-905f2cde008f")
	if !errors.Is(err, musicbrainz.ErrNoCoverArt) {
		t.Errorf("Fetch error = %v, want errors.Is ErrNoCoverArt", err)
	}
}

func TestCoverArt_FetchReturnsErrNoCoverArtWhenImagesIsEmpty(t *testing.T) {
	server := newCoverArtServer(t, http.StatusOK, `{"images": []}`)
	client := newCoverArtClient(t, server.URL)
	coverArt := musicbrainz.NewCoverArt(client, server.URL)

	_, err := coverArt.Fetch(t.Context(), "no-art-release")
	if !errors.Is(err, musicbrainz.ErrNoCoverArt) {
		t.Errorf("Fetch error = %v, want errors.Is ErrNoCoverArt", err)
	}
}

// TestCoverArt_FetchAgainstTheLiveAPI hits the real coverartarchive.org,
// following its real archive.org redirect chain, against MBIDs the parent
// session already confirmed live. It skips rather than fails when the
// sandbox has no outbound network, so make test stays deterministic
// wherever it runs.
func TestCoverArt_FetchAgainstTheLiveAPI(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	guard := httpx.NewGuard(nil, nil, nil)
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	client := httpx.NewClient(fake, guard, limiter, breaker, httpx.ClientConfig{
		Provider: musicbrainz.CoverArtName, UserAgent: httpx.UserAgent("test"),
	})
	coverArt := musicbrainz.NewCoverArt(client, "https://coverartarchive.org")

	image, err := coverArt.Fetch(t.Context(), "d319c059-baaa-4fc8-8ae2-cc3035c5fd19")
	if err != nil {
		t.Skipf("live coverartarchive.org unreachable, skipping: %v", err)
	}
	if image == "" {
		t.Error("Fetch = \"\", want a real image URL for KI Pecezinu")
	}

	_, err = coverArt.Fetch(t.Context(), "23724534-80a4-43fc-8172-905f2cde008f")
	if err != nil && !errors.Is(err, musicbrainz.ErrNoCoverArt) {
		t.Errorf("Fetch on a no-art release = %v, want ErrNoCoverArt", err)
	}
}
