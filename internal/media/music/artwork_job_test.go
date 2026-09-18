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

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/migrations"
)

// fakeCoverArt answers one canned URL (or musicbrainz.ErrNoCoverArt) per release,
// so a test never depends on the real Cover Art Archive.
type fakeCoverArt struct {
	urlByMBID         map[string]string
	urlByReleaseGroup map[string]string
	calls             int
	releaseGroupCalls int
}

func (f *fakeCoverArt) FetchReleaseGroup(_ context.Context, releaseGroupMBID string) (string, error) {
	f.releaseGroupCalls++
	url, ok := f.urlByReleaseGroup[releaseGroupMBID]
	if !ok {
		return "", musicbrainz.ErrNoCoverArt
	}
	return url, nil
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
	encoded := testPNG(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cover.png" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(encoded)
	}))
	t.Cleanup(server.Close)
	return server
}

// testPNG is a small, real, decodable, non-flat PNG.
func testPNG(t *testing.T) []byte {
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
	return encoded.Bytes()
}

func newArtworkJob(t *testing.T, coverArt *fakeCoverArt, imageServerURL string) (*music.ArtworkJob, *store.Store, *fsx.Mem) {
	t.Helper()
	return newArtworkJobReadingPictures(t, coverArt, imageServerURL, nil)
}

// fakePictures answers a canned embedded picture per absolute path, or
// mediatool.ErrNoEmbeddedPicture for any other.
type fakePictures struct {
	byPath map[string][]byte
	asked  []string
}

func (f *fakePictures) EmbeddedPicture(_ context.Context, path string) ([]byte, error) {
	f.asked = append(f.asked, path)
	picture, ok := f.byPath[path]
	if !ok {
		return nil, mediatool.ErrNoEmbeddedPicture
	}
	return picture, nil
}

// fakeArtworkRoots knows one root folder, id 1, at /music.
type fakeArtworkRoots struct{}

func (fakeArtworkRoots) RootFolder(id int64) (settings.RootFolder, bool) {
	if id != 1 {
		return settings.RootFolder{}, false
	}
	return settings.RootFolder{ID: 1, Path: "/music"}, true
}

func newArtworkJobReadingPictures(
	t *testing.T, coverArt *fakeCoverArt, imageServerURL string, pictures *fakePictures,
) (*music.ArtworkJob, *store.Store, *fsx.Mem) {
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
	config := music.ArtworkJobConfig{
		Artworks:  library.NewArtworks(libraryStore),
		CoverArt:  coverArt,
		HTTP:      client,
		FS:        memFS,
		ImagesDir: "/cache/images",
		Clock:     fake,
		Logger:    slog.New(slog.DiscardHandler),
	}
	if pictures != nil {
		config.Pictures, config.Roots = pictures, fakeArtworkRoots{}
	}
	return music.NewArtworkJob(config), libraryStore, memFS
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
	remaining, err := artworks.NeedingArtwork(t.Context(), 0, 10)
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
			nil, "", "", library.EditionRefinement{Kind: "official"}, nil); err != nil {
			t.Fatalf("RefineEdition: %v", err)
		}
		return edition.WorkID
	}
	t.Fatalf("no placeholder edition found for %s", releaseMBID)
	return 0
}

// The sweep re-read the lowest twenty works still without a cover. Twenty
// releases the archive has no art for therefore came back forever, and nothing
// after them was ever reached -- why only 516 of a library's albums ever got
// covers. Bounded by a deadline so the old behaviour fails rather than hangs.
func TestArtworkJob_ReachesPastAFullPageOfReleasesWithNoArt(t *testing.T) {
	server := testImageServer(t)
	coverArt := &fakeCoverArt{urlByMBID: map[string]string{"release-with-art": server.URL + "/cover.png"}}
	job, opened, _ := newArtworkJob(t, coverArt, server.URL)

	identity := library.NewIdentity(opened)
	for index := range 20 {
		refineWithReleaseMBIDForArtworkJob(t, identity, fmt.Sprintf("rg-bare-%d", index), fmt.Sprintf("release-bare-%d", index))
	}
	lastWorkID := refineWithReleaseMBIDForArtworkJob(t, identity, "rg-with-art", "release-with-art")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := job.Run(ctx, jobs.Job{Type: music.ArtworkJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if coverArt.calls != 21 {
		t.Errorf("CoverArt fetched %d times, want each of the 21 releases asked exactly once", coverArt.calls)
	}
	recorded, err := library.NewArtworks(opened).GetForWork(t.Context(), lastWorkID)
	if err != nil {
		t.Fatalf("GetForWork: %v", err)
	}
	if !recorded.Found {
		t.Fatal("the work after a full page of art-less releases never got its cover")
	}
}

// A wanted album discovered from a discography has a release group but no
// edition, so it has no release id to ask the archive about.
func TestArtworkJob_CoversAWorkWithOnlyAReleaseGroup(t *testing.T) {
	server := testImageServer(t)
	coverArt := &fakeCoverArt{urlByReleaseGroup: map[string]string{"rg-wanted": server.URL + "/cover.png"}}
	job, opened, _ := newArtworkJob(t, coverArt, server.URL)

	identity := library.NewIdentity(opened)
	resolved, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-wanted", Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "Dumucamoko", SortTitle: "dumucamoko"},
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	workID := resolved[0]

	if err := job.Run(t.Context(), jobs.Job{Type: music.ArtworkJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recorded, err := library.NewArtworks(opened).GetForWork(t.Context(), workID)
	if err != nil {
		t.Fatalf("GetForWork: %v", err)
	}
	if !recorded.Found {
		t.Fatal("a work with only a release group got no cover")
	}
}

// A release the archive has no art for may still belong to a release group
// that does, taken from one of its other releases.
func TestArtworkJob_FallsBackToTheReleaseGroupWhenTheReleaseHasNoArt(t *testing.T) {
	server := testImageServer(t)
	coverArt := &fakeCoverArt{urlByReleaseGroup: map[string]string{"rg-fallback": server.URL + "/cover.png"}}
	job, opened, _ := newArtworkJob(t, coverArt, server.URL)
	workID := refineWithReleaseMBIDForArtworkJob(t, library.NewIdentity(opened), "rg-fallback", "release-no-art")

	if err := job.Run(t.Context(), jobs.Job{Type: music.ArtworkJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if coverArt.calls != 1 || coverArt.releaseGroupCalls != 1 {
		t.Errorf("calls = %d release, %d release group; want the release asked first, then its group",
			coverArt.calls, coverArt.releaseGroupCalls)
	}
	recorded, err := library.NewArtworks(opened).GetForWork(t.Context(), workID)
	if err != nil {
		t.Fatalf("GetForWork: %v", err)
	}
	if !recorded.Found {
		t.Fatal("the release group's art was never used")
	}
}

// resolveWorkFromFile stands in for a scan: one file on disk under root 1, and
// a work identification resolved from it.
func resolveWorkFromFile(t *testing.T, opened *store.Store, releaseGroupMBID, relPath string) int64 {
	t.Helper()
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: relPath, SizeBytes: 1, QuickHash: "hash-" + relPath, FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	workIDs, err := library.NewIdentity(opened).ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: releaseGroupMBID, Confidence: 1.0,
		Work:         library.WorkIdentity{MediaType: "music", CanonicalTitle: relPath, SortTitle: relPath},
		RootFolderID: 1, MediaObjectID: registered[0].MediaObjectID,
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	return workIDs[0]
}

// The files carry their own cover art, including the ones with no MusicBrainz
// id at all; when the archive has nothing, that is the picture to show.
func TestArtworkJob_UsesTheCoverArtEmbeddedInTheFile(t *testing.T) {
	server := testImageServer(t)
	pictures := &fakePictures{byPath: map[string][]byte{"/music/Heka/Detisisu Binip/02.m4a": testPNG(t)}}
	job, opened, memFS := newArtworkJobReadingPictures(t, &fakeCoverArt{}, server.URL, pictures)
	workID := resolveWorkFromFile(t, opened, "rg-no-archive-art", "Heka/Detisisu Binip/02.m4a")

	if err := job.Run(t.Context(), jobs.Job{Type: music.ArtworkJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recorded, err := library.NewArtworks(opened).GetForWork(t.Context(), workID)
	if err != nil {
		t.Fatalf("GetForWork: %v", err)
	}
	if !recorded.Found {
		t.Fatal("the embedded picture was never used")
	}
	for _, size := range []string{"thumb", "small", "full"} {
		path := "/cache/images/" + recorded.CacheKey + "-" + size + ".webp"
		if _, err := memFS.Stat(path); err != nil {
			t.Errorf("%s was not written: %v", path, err)
		}
	}
}

// Archive art is the more exact image, so a work it covers never has its file read.
func TestArtworkJob_PrefersTheArchiveOverTheEmbeddedPicture(t *testing.T) {
	server := testImageServer(t)
	pictures := &fakePictures{byPath: map[string][]byte{}}
	coverArt := &fakeCoverArt{urlByReleaseGroup: map[string]string{"rg-archived": server.URL + "/cover.png"}}
	job, opened, _ := newArtworkJobReadingPictures(t, coverArt, server.URL, pictures)
	resolveWorkFromFile(t, opened, "rg-archived", "P!po/Trustfall/01.m4a")

	if err := job.Run(t.Context(), jobs.Job{Type: music.ArtworkJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(pictures.asked) != 0 {
		t.Errorf("embedded pictures read for %v, want none once the archive covered the work", pictures.asked)
	}
}
