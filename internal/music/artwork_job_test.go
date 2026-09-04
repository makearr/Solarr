package music_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/httpx"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/music"
	"github.com/makearr/MediaNexus/internal/musicbrainz"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/migrations"
)

// fakeCoverArt answers one canned URL (or musicbrainz.ErrNoCoverArt) per release,
// so a test never depends on the real Cover Art Archive.
type fakeCoverArt struct {
	urlByMBID map[string]string
	calls     int
}

func (f *fakeCoverArt) Fetch(_ context.Context, releaseMBID string) (string, error) {
	f.calls++
	url, ok := f.urlByMBID[releaseMBID]
	if !ok {
		return "", musicbrainz.ErrNoCoverArt
	}
	return url, nil
}

// testImageServer serves one small, real, decodable PNG at /cover.png -- large
// enough (64x64, non-flat) that resizing has something real to do.
func testImageServer(t *testing.T) *httptest.Server {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 128, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatalf("encode test image: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cover.png" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(encoded.Bytes())
	}))
	t.Cleanup(server.Close)
	return server
}

func newArtworkJob(t *testing.T, coverArt *fakeCoverArt, imageServerURL string) (*music.ArtworkJob, *store.Store, *fsx.Mem) {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	fake := clock.NewFakeAtEpoch()

	libraryStore, err := store.Open(t.Context(), fake, store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open library: %v", err)
	}
	t.Cleanup(func() { _ = libraryStore.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatalf("migrations.Library: %v", err)
	}
	if err := store.Migrate(t.Context(), libraryStore, backups, owner); err != nil {
		t.Fatalf("Migrate library: %v", err)
	}

	// The image server is httptest (loopback); the SSRF guard's allowlist has to
	// name it explicitly, the same way coverart_test.go's hermetic tests do.
	parsed, err := url.Parse(imageServerURL)
	if err != nil {
		t.Fatalf("parse image server URL: %v", err)
	}
	guard := httpx.NewGuard(nil, nil, []string{parsed.Hostname()})
	limiter := httpx.NewLimiter(fake, httpx.Config{}, func(time.Duration) time.Duration { return 0 })
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{})
	client := httpx.NewClient(fake, guard, limiter, breaker, httpx.ClientConfig{
		Provider: "test-image-server", UserAgent: "test",
	})

	memFS := fsx.NewMem(fake)
	job := music.NewArtworkJob(music.ArtworkJobConfig{
		Artworks:  library.NewArtworks(libraryStore),
		CoverArt:  coverArt,
		HTTP:      client,
		FS:        memFS,
		ImagesDir: "/cache/images",
		Clock:     fake,
		Logger:    slog.New(slog.DiscardHandler),
	})
	return job, libraryStore, memFS
}

func TestArtworkJob_FetchesResizesAndRecordsEndToEnd(t *testing.T) {
	server := testImageServer(t)
	coverArt := &fakeCoverArt{urlByMBID: map[string]string{"release-1": server.URL + "/cover.png"}}
	job, opened, memFS := newArtworkJob(t, coverArt, server.URL)

	identity := library.NewIdentity(opened)
	workID := refineWithReleaseMBIDForArtworkJob(t, identity, "rg-1", "release-1")

	if err := job.Run(t.Context(), jobs.Job{Type: music.ArtworkJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if coverArt.calls != 1 {
		t.Errorf("CoverArt fetched %d times, want 1", coverArt.calls)
	}

	artworks := library.NewArtworks(opened)
	remaining, err := artworks.NeedingArtwork(t.Context(), 10)
	if err != nil {
		t.Fatalf("NeedingArtwork: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("NeedingArtwork after Run = %+v, want none left", remaining)
	}

	for _, size := range []string{"thumb", "small", "full"} {
		path := fmt.Sprintf("/cache/images/work-%d-cover-%s.webp", workID, size)
		info, err := memFS.Stat(path)
		if err != nil {
			t.Errorf("Stat(%s): %v, want the file written by the job", path, err)
			continue
		}
		if info.Size == 0 {
			t.Errorf("%s is empty, want real WebP bytes", path)
		}
	}
}

// A release Cover Art Archive has nothing for must not be treated as a failure
// worth retrying -- it is recorded nowhere, and simply asked about again later.
func TestArtworkJob_SkipsAReleaseWithNoCoverArtWithoutFailing(t *testing.T) {
	server := testImageServer(t)
	coverArt := &fakeCoverArt{urlByMBID: map[string]string{}}
	job, opened, _ := newArtworkJob(t, coverArt, server.URL)

	identity := library.NewIdentity(opened)
	refineWithReleaseMBIDForArtworkJob(t, identity, "rg-2", "release-2")

	if err := job.Run(t.Context(), jobs.Job{Type: music.ArtworkJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if coverArt.calls != 1 {
		t.Errorf("CoverArt fetched %d times, want 1", coverArt.calls)
	}
}

func refineWithReleaseMBIDForArtworkJob(t *testing.T, identity library.Identity, releaseGroupMBID, releaseMBID string) int64 {
	t.Helper()
	_, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: releaseGroupMBID, Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"},
		Edition: &library.EditionLink{
			Provider: music.ProviderRelease, ExternalID: releaseMBID, Confidence: 1.0,
			Edition: library.EditionIdentity{Kind: "unknown", Label: releaseMBID},
		},
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	found, err := identity.PlaceholderEditions(t.Context(), 10)
	if err != nil {
		t.Fatalf("PlaceholderEditions: %v", err)
	}
	for _, edition := range found {
		if edition.ExternalID != releaseMBID {
			continue
		}
		if err := identity.RefineEdition(t.Context(), firstSeen.Unix(), edition.EditionID, edition.WorkID,
			nil, library.EditionRefinement{Kind: "official"}, nil); err != nil {
			t.Fatalf("RefineEdition: %v", err)
		}
		return edition.WorkID
	}
	t.Fatalf("no placeholder edition found for %s", releaseMBID)
	return 0
}
