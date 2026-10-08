package music_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/migrations"
)

// fakeSource answers one canned Album per test, so enrich_job_test.go can pin
// exactly what refinementFor (unexported) does with a realistic MusicBrainz
// answer, through EnrichJob's own black-box behaviour -- the same indirection
// title.go's sortTitle/albumTitleFallback already rely on Consumer's tests for.
type fakeSource struct {
	rg      musicbrainz.ReleaseGroup
	release musicbrainz.Release
	err     error
	mu      sync.Mutex
	calls   int
}

func (f *fakeSource) Name() string { return "fake" }
func (f *fakeSource) Album(context.Context, string, string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.rg, f.release, f.err
}

// fakeArtistSource is a fakeSource that also implements ArtistLookup, so
// EnrichJob has something to resolve a real artist's bio facts against.
type fakeArtistSource struct {
	fakeSource
	artists     map[string]musicbrainz.Artist
	artistCalls int
}

func (f *fakeArtistSource) Artist(_ context.Context, mbid string) (musicbrainz.Artist, error) {
	f.artistCalls++
	artist, ok := f.artists[mbid]
	if !ok {
		return musicbrainz.Artist{}, errors.New("no such artist")
	}
	return artist, nil
}

// fakeBiographer answers a canned biography per QID, or an error for an
// unlisted one.
type fakeBiographer struct {
	biographies map[string]wikidata.Biography
	calls       int
	batches     int
}

func (f *fakeBiographer) Biographies(ctx context.Context, qids []string) (map[string]wikidata.Biography, error) {
	f.batches++
	found := map[string]wikidata.Biography{}
	for _, qid := range qids {
		if bio, err := f.Biography(ctx, qid); err == nil {
			found[qid] = bio
		}
	}
	return found, nil
}

func (f *fakeBiographer) Biography(_ context.Context, qid string) (wikidata.Biography, error) {
	f.calls++
	bio, ok := f.biographies[qid]
	if !ok {
		return wikidata.Biography{}, errors.New("no such entity")
	}
	return bio, nil
}

func newEnrichJob(t *testing.T, source musicbrainz.Source) (*music.EnrichJob, *store.Store, *librarydb.Queries) {
	return newEnrichJobWithBiographer(t, source, nil)
}

func newEnrichJobWithBiographer(
	t *testing.T, source musicbrainz.Source, biographer music.Biographer,
) (*music.EnrichJob, *store.Store, *librarydb.Queries) {
	t.Helper()
	return newEnrichJobRealigning(t, source, biographer, nil, nil)
}

// fakeRealigner records every batch of works it is asked to realign, and every OnRefined call.
type fakeRealigner struct {
	batches [][]int64
	manual  []bool
	refined int
}

func (f *fakeRealigner) RealignWorks(_ context.Context, workIDs []int64, manual bool) error {
	f.batches = append(f.batches, slices.Clone(workIDs))
	f.manual = append(f.manual, manual)
	return nil
}

func newEnrichJobRealigning(
	t *testing.T, source musicbrainz.Source, biographer music.Biographer, realigner *fakeRealigner, tags music.TagReader,
	configure ...func(*music.EnrichJobConfig),
) (*music.EnrichJob, *store.Store, *librarydb.Queries) {
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
	libraryOwner, err := migrations.Library()
	if err != nil {
		t.Fatalf("migrations.Library: %v", err)
	}
	if err := store.Migrate(t.Context(), libraryStore, backups, libraryOwner); err != nil {
		t.Fatalf("Migrate library: %v", err)
	}

	metadataStore, err := store.Open(t.Context(), fake, store.Config{
		Name: "metadata", Path: filepath.Join(directory, "metadata.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open metadata: %v", err)
	}
	t.Cleanup(func() { _ = metadataStore.Close() })
	metadataOwner, err := migrations.Metadata()
	if err != nil {
		t.Fatalf("migrations.Metadata: %v", err)
	}
	if err := store.Migrate(t.Context(), metadataStore, backups, metadataOwner); err != nil {
		t.Fatalf("Migrate metadata: %v", err)
	}

	identity := library.NewIdentity(libraryStore)
	provider := musicbrainz.New(musicbrainz.Config{
		Sources: []musicbrainz.Source{source}, Cache: metacache.New(metadataStore),
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})
	config := music.EnrichJobConfig{
		Identity: identity, People: library.NewPeople(libraryStore), Provider: provider,
		Biography: biographer, Clock: fake, Logger: slog.New(slog.DiscardHandler),
	}
	if realigner != nil {
		config.Realigner = realigner
		config.OnRefined = func(context.Context) { realigner.refined++ }
	}
	if tags != nil {
		config.Tags, config.Roots = tags, fakeArtworkRoots{}
	}
	for _, change := range configure {
		change(&config)
	}
	return music.NewEnrichJob(config), libraryStore, librarydb.New(libraryStore.Read())
}

// newEnrichJobWithImageFetch is newEnrichJobWithBiographer plus the artwork
// wiring (Artworks/HTTP/FS/ImagesDir) fetchPersonImage needs -- a separate
// constructor rather than growing the first one's own signature, since every
// other test has no use for it (fetchPersonImage no-ops with any of these
// nil, biographyFor's own tests never noticed).
func newEnrichJobWithImageFetch(
	t *testing.T, source musicbrainz.Source, biographer music.Biographer, imageServerURL string,
	options ...func(*music.EnrichJobConfig),
) (*music.EnrichJob, *store.Store, *librarydb.Queries, *fsx.Mem) {
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
	libraryOwner, err := migrations.Library()
	if err != nil {
		t.Fatalf("migrations.Library: %v", err)
	}
	if err := store.Migrate(t.Context(), libraryStore, backups, libraryOwner); err != nil {
		t.Fatalf("Migrate library: %v", err)
	}

	metadataStore, err := store.Open(t.Context(), fake, store.Config{
		Name: "metadata", Path: filepath.Join(directory, "metadata.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open metadata: %v", err)
	}
	t.Cleanup(func() { _ = metadataStore.Close() })
	metadataOwner, err := migrations.Metadata()
	if err != nil {
		t.Fatalf("migrations.Metadata: %v", err)
	}
	if err := store.Migrate(t.Context(), metadataStore, backups, metadataOwner); err != nil {
		t.Fatalf("Migrate metadata: %v", err)
	}

	identity := library.NewIdentity(libraryStore)
	provider := musicbrainz.New(musicbrainz.Config{
		Sources: []musicbrainz.Source{source}, Cache: metacache.New(metadataStore),
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})

	// The image server is httptest (loopback); the SSRF guard's allowlist has
	// to name it explicitly, artwork_job_test.go's own newArtworkJob pattern.
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
	config := music.EnrichJobConfig{
		Identity: identity, People: library.NewPeople(libraryStore), Provider: provider,
		Biography: biographer, Artworks: library.NewArtworks(libraryStore),
		HTTP: client, FS: memFS, ImagesDir: "/cache/images",
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	}
	for _, option := range options {
		option(&config)
	}
	job := music.NewEnrichJob(config)
	return job, libraryStore, librarydb.New(libraryStore.Read()), memFS
}

func createPlaceholder(t *testing.T, opened *store.Store, releaseGroupMBID, releaseMBID string) {
	t.Helper()
	identity := library.NewIdentity(opened)
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
}

var firstSeen = clock.NewFakeAtEpoch().Now()

func TestEnrichJob_RefinesAPlaceholderEditionEndToEnd(t *testing.T) {
	source := &fakeSource{
		rg: musicbrainz.ReleaseGroup{
			MBID: "rg-1", Title: "KI Pecezinu", PrimaryType: "Album", FirstReleaseDate: "1997-05-21",
		},
		release: musicbrainz.Release{
			// Status arrives already lowercased -- that is a Source's contract
			// (types.go), verified for both real sources in direct_test.go and
			// proxy_test.go; this fake stands in for a compliant one.
			MBID: "rel-1", Title: "KI Pecezinu", Status: "official", Date: "1997-06-16",
			CatalogueNumber: "CDNODATA02", Barcode: "724385502125",
			Media: []musicbrainz.Medium{{
				Position: 1, Format: "CD",
				Tracks: []musicbrainz.Track{
					{Position: 1, Title: "Sesoge", DurationMs: 284400, RecordingMBID: "rec-1"},
					{Position: 2, Title: "Zosidedu Bukerez", DurationMs: 383000, RecordingMBID: "rec-2"},
				},
			}},
		},
	}
	job, opened, queries := newEnrichJob(t, source)
	createPlaceholder(t, opened, "rg-1", "rel-1")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if source.calls != 1 {
		t.Errorf("source called %d times, want 1", source.calls)
	}

	edition, err := queries.GetEditionByExternalID(t.Context(), librarydb.GetEditionByExternalIDParams{
		Provider: music.ProviderRelease, ExternalID: "rel-1",
	})
	if err != nil {
		t.Fatalf("GetEditionByExternalID: %v", err)
	}
	// status lowercased into kind, real fields written, and the total runtime
	// summed across every track -- refinementFor's whole job, asserted as
	// whole values rather than piecemeal.
	if edition.Kind != "official" {
		t.Errorf("Kind = %q, want the lowercased status", edition.Kind)
	}
	if edition.Format.String != "CD" || edition.CatalogueNumber.String != "CDNODATA02" || edition.Barcode.String != "724385502125" {
		t.Errorf("edition = %+v, want the release's own fields", edition)
	}
	if !edition.Year.Valid || edition.Year.Int64 != 1997 {
		t.Errorf("Year = %+v, want 1997 parsed from 1997-06-16", edition.Year)
	}
	if !edition.RuntimeMs.Valid || edition.RuntimeMs.Int64 != 284400+383000 {
		t.Errorf("RuntimeMs = %+v, want the two tracks summed", edition.RuntimeMs)
	}

	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-1",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	if !work.Year.Valid || work.Year.Int64 != 1997 {
		t.Errorf("work.Year = %+v, want 1997 parsed from the release group's first-release-date", work.Year)
	}

	dates, err := queries.ListWorkDates(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListWorkDates: %v", err)
	}
	if len(dates) != 1 || dates[0].Kind != library.WorkDateStreet || dates[0].Date != "1997-05-21" {
		t.Errorf("work dates = %+v, want the full 1997-05-21 kept (todo 4.20, day precision the year alone loses)",
			dates)
	}

	parts, err := queries.ListWorkParts(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListWorkParts: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("ListWorkParts = %+v, want the two tracks", parts)
	}
	if parts[0].MediaRef != "disc1:track1" || parts[0].Title.String != "Sesoge" {
		t.Errorf("part 1 = %+v, want disc1:track1 'Sesoge'", parts[0])
	}
	if parts[0].SortKey >= parts[1].SortKey {
		t.Errorf("sort keys %q, %q not in track order", parts[0].SortKey, parts[1].SortKey)
	}

	recording, err := queries.FindPartsByExternalID(t.Context(), librarydb.FindPartsByExternalIDParams{
		Provider: music.ProviderRecording, ExternalID: "rec-1",
	})
	if err != nil {
		t.Fatalf("FindPartsByExternalID: %v", err)
	}
	if len(recording) != 1 {
		t.Errorf("FindPartsByExternalID(rec-1) = %+v, want exactly the Sesoge part", recording)
	}

	// The whole point of PlaceholderEditions shrinking on its own: nothing is
	// left to enrich, so a second run must not call the source again.
	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if source.calls != 1 {
		t.Errorf("source called %d times after a second run, want still 1 -- nothing left to enrich", source.calls)
	}
}

// The whole credits/genres pipeline, end to end: a release group's own
// artist-credit and genres (both fetched in the same Album call, no separate
// request) become a real person, a real work-level credit, and real tags.
func TestEnrichJob_PopulatesCreditsAndGenres(t *testing.T) {
	source := &fakeArtistSource{
		fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{
				MBID: "rg-1", Title: "KI Pecezinu", PrimaryType: "Album", FirstReleaseDate: "1997-05-21",
				ArtistCredit: []musicbrainz.ArtistCredit{{MBID: "artist-1", Name: "Katupoken"}},
				Genres:       []musicbrainz.Genre{{Name: "alternative rock", Count: 25}},
			},
			release: musicbrainz.Release{MBID: "rel-1", Title: "KI Pecezinu", Status: "official"},
		},
		artists: map[string]musicbrainz.Artist{
			"artist-1": {MBID: "artist-1", Name: "Katupoken", SortName: "Katupoken", Type: "Group", Country: "GB"},
		},
	}
	job, opened, queries := newEnrichJob(t, source)
	createPlaceholder(t, opened, "rg-1", "rel-1")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if source.artistCalls != 1 {
		t.Errorf("artistCalls = %d, want 1", source.artistCalls)
	}

	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-1",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}

	person, err := queries.GetPersonByExternalID(t.Context(), librarydb.GetPersonByExternalIDParams{
		Provider: music.ProviderArtist, ExternalID: "artist-1",
	})
	if err != nil {
		t.Fatalf("GetPersonByExternalID: %v", err)
	}
	if person.Name != "Katupoken" || person.Country.String != "GB" {
		t.Errorf("person = %+v, want Katupoken's own fields from the Artist lookup", person)
	}

	credits, err := queries.ListCreditsForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListCreditsForWork: %v", err)
	}
	if len(credits) != 1 || credits[0].PersonID != person.ID || credits[0].Role != music.CreditRoleArtist {
		t.Errorf("credits = %+v, want one artist credit for %d", credits, person.ID)
	}

	tags, err := queries.ListTagsForSubject(t.Context(), librarydb.ListTagsForSubjectParams{
		SubjectType: "work", SubjectID: work.ID,
	})
	if err != nil {
		t.Fatalf("ListTagsForSubject: %v", err)
	}
	if len(tags) != 1 || tags[0].Source != "musicbrainz" {
		t.Errorf("tags = %+v, want the one genre tag, source musicbrainz", tags)
	}
}

// An artist detail lookup failing must not lose the credit entirely -- a
// person resolved from the bare name/id the artist-credit itself carries is
// still worth having.
func TestEnrichJob_AFailedArtistLookupStillResolvesAThinnerPerson(t *testing.T) {
	source := &fakeArtistSource{
		fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{
				MBID: "rg-1", Title: "KI Pecezinu", PrimaryType: "Album",
				ArtistCredit: []musicbrainz.ArtistCredit{{MBID: "artist-1", Name: "Katupoken"}},
			},
			release: musicbrainz.Release{MBID: "rel-1", Title: "KI Pecezinu", Status: "official"},
		},
		artists: map[string]musicbrainz.Artist{}, // no entry: the Artist lookup fails
	}
	job, opened, queries := newEnrichJob(t, source)
	createPlaceholder(t, opened, "rg-1", "rel-1")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	person, err := queries.GetPersonByExternalID(t.Context(), librarydb.GetPersonByExternalIDParams{
		Provider: music.ProviderArtist, ExternalID: "artist-1",
	})
	if err != nil {
		t.Fatalf("GetPersonByExternalID: %v, want the thinner person resolved anyway", err)
	}
	if person.Name != "Katupoken" {
		t.Errorf("person.Name = %q, want Katupoken from the bare artist-credit", person.Name)
	}
}

// The Wikidata QID an Artist lookup carries becomes a real biography --
// preferring the Wikipedia extract over Wikidata's own short description.
func TestEnrichJob_ResolvesABiographyFromTheArtistsWikidataQID(t *testing.T) {
	source := &fakeArtistSource{
		fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{
				MBID: "rg-1", Title: "The Sezo Siluko", PrimaryType: "Album",
				ArtistCredit: []musicbrainz.ArtistCredit{{MBID: "artist-1", Name: "Vutu Doci"}},
			},
			release: musicbrainz.Release{MBID: "rel-1", Title: "The Sezo Siluko", Status: "official"},
		},
		artists: map[string]musicbrainz.Artist{
			"artist-1": {MBID: "artist-1", Name: "Vutu Doci", SortName: "Bush, Vutu", WikidataQID: "Q636"},
		},
	}
	biographer := &fakeBiographer{biographies: map[string]wikidata.Biography{
		"Q636": {Description: "English singer (born 1958)", Extract: "Catherine Bush is an English singer."},
	}}
	job, opened, queries := newEnrichJobWithBiographer(t, source, biographer)
	createPlaceholder(t, opened, "rg-1", "rel-1")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if biographer.calls != 1 {
		t.Errorf("Biography called %d times, want 1", biographer.calls)
	}

	person, err := queries.GetPersonByExternalID(t.Context(), librarydb.GetPersonByExternalIDParams{
		Provider: music.ProviderArtist, ExternalID: "artist-1",
	})
	if err != nil {
		t.Fatalf("GetPersonByExternalID: %v", err)
	}
	if !person.FieldsJson.Valid || !strings.Contains(person.FieldsJson.String, "Catherine Bush is an English singer") {
		t.Errorf("fields_json = %+v, want the Wikipedia extract, not just the short description", person.FieldsJson)
	}
}

// The image fallback chain asked for 2026-09-11 (song -> album -> artist)
// starts here: an artist credit whose Wikidata entity carries a P18 image
// gets that image fetched, resized and recorded against the resolved
// person, end to end -- real bytes through a real httptest server, real
// artwork.Process resizing, a real (in-memory) filesystem write.
func TestEnrichJob_FetchesAndRecordsTheArtistsWikidataImage(t *testing.T) {
	server := testImageServer(t)
	source := &fakeArtistSource{
		fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{
				MBID: "rg-1", Title: "The Sezo Siluko", PrimaryType: "Album",
				ArtistCredit: []musicbrainz.ArtistCredit{{MBID: "artist-1", Name: "Vutu Doci"}},
			},
			release: musicbrainz.Release{MBID: "rel-1", Title: "The Sezo Siluko", Status: "official"},
		},
		artists: map[string]musicbrainz.Artist{
			"artist-1": {MBID: "artist-1", Name: "Vutu Doci", SortName: "Bush, Vutu", WikidataQID: "Q636"},
		},
	}
	biographer := &fakeBiographer{biographies: map[string]wikidata.Biography{
		"Q636": {Description: "English singer", ImageURL: server.URL + "/cover.png"},
	}}
	job, opened, queries, memFS := newEnrichJobWithImageFetch(t, source, biographer, server.URL)
	createPlaceholder(t, opened, "rg-1", "rel-1")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	person, err := queries.GetPersonByExternalID(t.Context(), librarydb.GetPersonByExternalIDParams{
		Provider: music.ProviderArtist, ExternalID: "artist-1",
	})
	if err != nil {
		t.Fatalf("GetPersonByExternalID: %v", err)
	}

	artworks := library.NewArtworks(opened)
	recorded, err := artworks.GetForPerson(t.Context(), person.ID)
	if err != nil {
		t.Fatalf("GetForPerson: %v", err)
	}
	if !recorded.Found {
		t.Fatal("no artwork recorded for the resolved person")
	}
	for _, size := range []string{"thumb", "small", "full"} {
		path := "/cache/images/" + recorded.CacheKey + "-" + size + ".webp"
		if _, err := memFS.Stat(path); err != nil {
			t.Errorf("%s was not written: %v", path, err)
		}
	}
}

// The release group's own Wikidata QID (distinct from the artist's) becomes
// the work's description.
func TestEnrichJob_ResolvesAWorkDescriptionFromTheReleaseGroupsWikidataQID(t *testing.T) {
	source := &fakeArtistSource{
		fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{
				MBID: "rg-1", Title: "The Fura Husu of the Teke", PrimaryType: "Album",
				WikidataQID: "Q150901",
			},
			release: musicbrainz.Release{MBID: "rel-1", Title: "The Fura Husu of the Teke", Status: "official"},
		},
	}
	biographer := &fakeBiographer{biographies: map[string]wikidata.Biography{
		"Q150901": {Description: "1973 studio album by Dipa Cogaf", Extract: "The Fura Husu of the Teke is..."},
	}}
	job, opened, queries := newEnrichJobWithBiographer(t, source, biographer)
	createPlaceholder(t, opened, "rg-1", "rel-1")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-1",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	if !work.Description.Valid || work.Description.String != "The Fura Husu of the Teke is..." {
		t.Errorf("Description = %+v, want the Wikipedia extract", work.Description)
	}
}

// No Biographer configured at all (nil, the default) must not fail
// resolveArtist -- MusicBrainz's own facts are still a complete person.
func TestEnrichJob_WithNoBiographerConfiguredStillResolvesThePerson(t *testing.T) {
	source := &fakeArtistSource{
		fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{
				MBID: "rg-1", Title: "The Sezo Siluko", PrimaryType: "Album",
				ArtistCredit: []musicbrainz.ArtistCredit{{MBID: "artist-1", Name: "Vutu Doci"}},
			},
			release: musicbrainz.Release{MBID: "rel-1", Title: "The Sezo Siluko", Status: "official"},
		},
		artists: map[string]musicbrainz.Artist{
			"artist-1": {MBID: "artist-1", Name: "Vutu Doci", SortName: "Bush, Vutu", WikidataQID: "Q636"},
		},
	}
	job, opened, queries := newEnrichJob(t, source) // biographer nil
	createPlaceholder(t, opened, "rg-1", "rel-1")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	person, err := queries.GetPersonByExternalID(t.Context(), librarydb.GetPersonByExternalIDParams{
		Provider: music.ProviderArtist, ExternalID: "artist-1",
	})
	if err != nil {
		t.Fatalf("GetPersonByExternalID: %v", err)
	}
	if person.Name != "Vutu Doci" {
		t.Errorf("person.Name = %q, want Vutu Doci resolved with no biography", person.Name)
	}
}

// A source failure on one edition must not stop the sweep from finishing --
// per-edition isolation, exactly like Consumer.identifyBatch's own "log and
// continue" (internal/media/music/consumer.go).
func TestEnrichJob_ASourceFailureIsolatesToOneEdition(t *testing.T) {
	source := &fakeSource{err: errors.New("musicbrainz unavailable")}
	job, opened, _ := newEnrichJob(t, source)
	createPlaceholder(t, opened, "rg-1", "rel-1")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	identity := library.NewIdentity(opened)
	remaining, err := identity.PlaceholderEditions(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("PlaceholderEditions: %v", err)
	}
	// Still a placeholder: the failed edition is left for the next run, not
	// abandoned or half-written.
	if len(remaining) != 1 {
		t.Errorf("PlaceholderEditions = %+v, want the failed edition still waiting", remaining)
	}
}

// resolveBareArtist stands in for a person resolved before images or
// biographies had a source -- name and id only, exactly what the library was
// full of when no artist picture had ever appeared (2026-09-13).
func resolveBareArtist(t *testing.T, opened *store.Store, mbid, name string) int64 {
	t.Helper()
	personID, err := library.NewPeople(opened).ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: music.ProviderArtist, ExternalID: mbid,
		Person: library.PersonIdentity{Name: name},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	return personID
}

// More editions failing for good than one batch holds are each tried once and the run ends: the
// shrinking set without a cursor served the same 20 forever (the main library, 2026-09-26).
func TestEnrichJob_AFullBatchOfFailuresEndsTheRun(t *testing.T) {
	source := &fakeSource{err: errors.New("release retired")}
	job, opened, _ := newEnrichJob(t, source)
	const editions = 25
	for i := range editions {
		createPlaceholder(t, opened, fmt.Sprintf("rg-%d", i), fmt.Sprintf("rel-%d", i))
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	if err := job.Run(ctx, jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v, want the run to end once every edition was tried", err)
	}
	if source.calls != editions {
		t.Errorf("source asked %d times, want each of the %d editions once", source.calls, editions)
	}
}

func TestEnrichJob_BackfillsAnAlreadyResolvedArtistsImageAndBiography(t *testing.T) {
	server := testImageServer(t)
	source := &fakeArtistSource{artists: map[string]musicbrainz.Artist{
		"artist-9": {
			MBID: "artist-9", Name: "Vutu Doci", SortName: "Bush, Vutu",
			Country: "GB", WikidataQID: "Q636",
		},
	}}
	biographer := &fakeBiographer{biographies: map[string]wikidata.Biography{
		"Q636": {Description: "English singer", ImageURL: server.URL + "/cover.png"},
	}}
	job, opened, queries, memFS := newEnrichJobWithImageFetch(t, source, biographer, server.URL)

	personID := resolveBareArtist(t, opened, "artist-9", "Vutu Doci")

	// No placeholder editions at all: the backfill pass is the only thing
	// with anything to do.
	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recorded, err := library.NewArtworks(opened).GetForPerson(t.Context(), personID)
	if err != nil {
		t.Fatalf("GetForPerson: %v", err)
	}
	if !recorded.Found {
		t.Fatal("the already-resolved artist gained no image")
	}
	for _, size := range []string{"thumb", "small", "full"} {
		path := "/cache/images/" + recorded.CacheKey + "-" + size + ".webp"
		if _, err := memFS.Stat(path); err != nil {
			t.Errorf("%s was not written: %v", path, err)
		}
	}

	person, err := queries.GetPerson(t.Context(), personID)
	if err != nil {
		t.Fatalf("GetPerson: %v", err)
	}
	if !strings.Contains(person.FieldsJson.String, "English singer") {
		t.Errorf("fields_json = %q, want the backfilled biography", person.FieldsJson.String)
	}
	if person.Country.String != "GB" {
		t.Errorf("country = %q, want GB backfilled", person.Country.String)
	}
}

// An artist Wikidata has no photo for must be remembered as such, or the
// sweep asks about the same lowest-id people every run and never reaches
// the rest -- at one MusicBrainz lookup each, against a shared 1 req/s budget.
func TestEnrichJob_RecordsThatAnArtistHasNoImageSoTheSweepMovesOn(t *testing.T) {
	server := testImageServer(t)
	source := &fakeArtistSource{artists: map[string]musicbrainz.Artist{
		"artist-8": {MBID: "artist-8", Name: "Obscure Act", SortName: "Obscure Act"},
	}}
	biographer := &fakeBiographer{biographies: map[string]wikidata.Biography{}}
	job, opened, _, _ := newEnrichJobWithImageFetch(t, source, biographer, server.URL)

	personID := resolveBareArtist(t, opened, "artist-8", "Obscure Act")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	artworks := library.NewArtworks(opened)
	recorded, err := artworks.GetForPerson(t.Context(), personID)
	if err != nil {
		t.Fatalf("GetForPerson: %v", err)
	}
	if recorded.Found {
		t.Fatalf("GetForPerson = %+v, want no servable image for an artist without one", recorded)
	}

	remaining, err := artworks.PeopleNeedingArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("PeopleNeedingArtwork: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("PeopleNeedingArtwork = %+v, want the artist dropped from the sweep", remaining)
	}
}

// A Wikidata lookup that errors says nothing about whether the artist has a
// photo, so it must stay in the sweep rather than be written off for good.
func TestEnrichJob_BackfillRetriesAnArtistWhoseWikidataLookupFailed(t *testing.T) {
	server := testImageServer(t)
	source := &fakeArtistSource{artists: map[string]musicbrainz.Artist{
		"artist-7": {MBID: "artist-7", Name: "Heka", SortName: "Heka", WikidataQID: "Q-unreachable"},
	}}
	biographer := &fakeBiographer{biographies: map[string]wikidata.Biography{}}
	job, opened, _, _ := newEnrichJobWithImageFetch(t, source, biographer, server.URL)
	personID := resolveBareArtist(t, opened, "artist-7", "Heka")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	remaining, err := library.NewArtworks(opened).PeopleNeedingArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("PeopleNeedingArtwork: %v", err)
	}
	if len(remaining) != 1 || remaining[0].PersonID != personID {
		t.Fatalf("PeopleNeedingArtwork = %+v, want person %d still waiting for a retry", remaining, personID)
	}
}

// Bytes that do not decode now will not decode next run either.
func TestEnrichJob_BackfillRecordsAnUndecodableImageAsNoImage(t *testing.T) {
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/tiff")
		_, _ = w.Write([]byte("II*\x00 not an image this decoder understands"))
	}))
	t.Cleanup(garbage.Close)

	source := &fakeArtistSource{artists: map[string]musicbrainz.Artist{
		"artist-6": {MBID: "artist-6", Name: "P!po", SortName: "P!po", WikidataQID: "Q2"},
	}}
	biographer := &fakeBiographer{biographies: map[string]wikidata.Biography{
		"Q2": {Description: "American singer", ImageURL: garbage.URL + "/photo.tif"},
	}}
	job, opened, _, _ := newEnrichJobWithImageFetch(t, source, biographer, garbage.URL)
	personID := resolveBareArtist(t, opened, "artist-6", "P!po")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	artworks := library.NewArtworks(opened)
	remaining, err := artworks.PeopleNeedingArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("PeopleNeedingArtwork: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("PeopleNeedingArtwork = %+v, want the undecodable artist dropped from the sweep", remaining)
	}
	recorded, err := artworks.GetForPerson(t.Context(), personID)
	if err != nil {
		t.Fatalf("GetForPerson: %v", err)
	}
	if recorded.Found {
		t.Fatalf("GetForPerson = %+v, want no servable image", recorded)
	}
}

// With no Wikidata source configured every artist looks imageless; recording
// that would write off the whole library permanently.
func TestEnrichJob_BackfillRecordsNothingWithoutAWikidataSource(t *testing.T) {
	server := testImageServer(t)
	source := &fakeArtistSource{artists: map[string]musicbrainz.Artist{
		"artist-5": {MBID: "artist-5", Name: "Pevilodon", SortName: "Pevilodon", WikidataQID: "Q5"},
	}}
	job, opened, _, _ := newEnrichJobWithImageFetch(t, source, nil, server.URL)
	resolveBareArtist(t, opened, "artist-5", "Pevilodon")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	remaining, err := library.NewArtworks(opened).PeopleNeedingArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("PeopleNeedingArtwork: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("PeopleNeedingArtwork = %+v, want the artist untouched", remaining)
	}
}

// Switching Wikidata off must not record every artist as photo-less, or switching it back on
// would never fetch one.
func TestEnrichJob_BackfillRecordsNothingWithWikidataSwitchedOff(t *testing.T) {
	server := testImageServer(t)
	source := &fakeArtistSource{artists: map[string]musicbrainz.Artist{
		"artist-5": {MBID: "artist-5", Name: "Pevilodon", SortName: "Pevilodon", WikidataQID: "Q5"},
	}}
	biographer := &fakeBiographer{biographies: map[string]wikidata.Biography{
		"Q5": {Description: "American singer", ImageURL: server.URL + "/cover.png"},
	}}
	job, opened, _, _ := newEnrichJobWithImageFetch(t, source, biographer, server.URL,
		func(config *music.EnrichJobConfig) { config.WikidataEnabled = func() bool { return false } })
	resolveBareArtist(t, opened, "artist-5", "Pevilodon")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	remaining, err := library.NewArtworks(opened).PeopleNeedingArtwork(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("PeopleNeedingArtwork: %v", err)
	}
	if len(remaining) != 1 || biographer.calls != 0 {
		t.Fatalf("PeopleNeedingArtwork = %+v, Wikidata calls %d; want the artist untouched and no call",
			remaining, biographer.calls)
	}
}

// More failing people than one page must not hide the ones after them: the
// sweep once re-read the same first page for its whole budget.
func TestEnrichJob_BackfillReachesPastAFullPageOfFailures(t *testing.T) {
	server := testImageServer(t)
	source := &fakeArtistSource{artists: map[string]musicbrainz.Artist{
		"artist-last": {MBID: "artist-last", Name: "Pióurazo", SortName: "Pióurazo", WikidataQID: "Q9"},
	}}
	biographer := &fakeBiographer{biographies: map[string]wikidata.Biography{
		"Q9": {Description: "German singer", ImageURL: server.URL + "/cover.png"},
	}}
	job, opened, _, _ := newEnrichJobWithImageFetch(t, source, biographer, server.URL)

	// Twenty artists MusicBrainz has no record of, ahead of the one it does.
	for index := range 20 {
		mbid := "artist-missing-" + strconv.Itoa(index)
		resolveBareArtist(t, opened, mbid, mbid)
	}
	lastID := resolveBareArtist(t, opened, "artist-last", "Pióurazo")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recorded, err := library.NewArtworks(opened).GetForPerson(t.Context(), lastID)
	if err != nil {
		t.Fatalf("GetForPerson: %v", err)
	}
	if !recorded.Found {
		t.Fatal("the artist after a full page of failures was never reached")
	}
}

// The configured budget bounds one run: of five artists waiting, each run looks up two.
func TestEnrichJob_BackfillStopsAtTheConfiguredBudget(t *testing.T) {
	server := testImageServer(t)
	artists := map[string]musicbrainz.Artist{}
	biographies := map[string]wikidata.Biography{}
	for index := range 5 {
		mbid, qid := "artist-"+strconv.Itoa(index), "Q"+strconv.Itoa(index)
		artists[mbid] = musicbrainz.Artist{MBID: mbid, Name: mbid, SortName: mbid, WikidataQID: qid}
		// No photo: a download would wait on the fake clock, and "none" is recorded all the same.
		biographies[qid] = wikidata.Biography{Description: "singer"}
	}
	job, opened, _, _ := newEnrichJobWithImageFetch(t, &fakeArtistSource{artists: artists},
		&fakeBiographer{biographies: biographies}, server.URL,
		func(config *music.EnrichJobConfig) { config.PeopleBackfillPerRun = func() int { return 2 } })
	for index := range 5 {
		resolveBareArtist(t, opened, "artist-"+strconv.Itoa(index), "artist-"+strconv.Itoa(index))
	}

	artworks := library.NewArtworks(opened)
	for run, want := range []int{3, 1} {
		if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		waiting, err := artworks.PeopleNeedingArtwork(t.Context(), 0, 50)
		if err != nil {
			t.Fatalf("PeopleNeedingArtwork: %v", err)
		}
		if len(waiting) != want {
			t.Errorf("after run %d: %d artists still waiting, want %d", run+1, len(waiting), want)
		}
	}
}

// A scan matches files only against tracks that already exist, and on a fresh
// library the tracklist arrives here, afterwards. Every refined album must be
// handed back for its files to be matched, or it stays wanted with none present.
func TestEnrichJob_HandsRefinedWorksBackToBeRealigned(t *testing.T) {
	source := &fakeSource{
		rg: musicbrainz.ReleaseGroup{MBID: "rg-1", Title: "Detisisu Binip", PrimaryType: "Album"},
		release: musicbrainz.Release{
			MBID: "rel-1", Title: "Detisisu Binip", Status: "official",
			Media: []musicbrainz.Medium{{Position: 1, Tracks: []musicbrainz.Track{{Position: 1, Title: "Detisisu Binip"}}}},
		},
	}
	realigner := &fakeRealigner{}
	job, opened, queries := newEnrichJobRealigning(t, source, nil, realigner, nil)
	createPlaceholder(t, opened, "rg-1", "rel-1")

	// "Run now" says so, for a library's "Only After Manual Refresh".
	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType, Payload: []byte(`{"manual":true}`)}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(realigner.manual, []bool{true}) {
		t.Errorf("realigned as manual %v, want [true]", realigner.manual)
	}

	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-1",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	if len(realigner.batches) != 1 || !slices.Equal(realigner.batches[0], []int64{work.ID}) {
		t.Errorf("realigned %v, want one batch holding work %d", realigner.batches, work.ID)
	}
	// The new tracklist is counted at once, not at the daily counter sweep.
	if realigner.refined != 1 {
		t.Errorf("OnRefined called %d times, want 1", realigner.refined)
	}
}

// Nothing refined, nothing to realign: an idle sweep must not queue rescans.
func TestEnrichJob_RealignsNothingWhenNothingWasRefined(t *testing.T) {
	realigner := &fakeRealigner{}
	job, _, _ := newEnrichJobRealigning(t, &fakeSource{}, nil, realigner, nil)

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(realigner.batches) != 0 || realigner.refined != 0 {
		t.Errorf("realigned %v, refined %d times; want nothing", realigner.batches, realigner.refined)
	}
}

// failingOnceArtistSource fails the first artist lookup, as MusicBrainz does
// with a 503 under load, and answers normally after that.
type failingOnceArtistSource struct {
	fakeArtistSource
	failed bool
}

func (f *failingOnceArtistSource) Artist(ctx context.Context, mbid string) (musicbrainz.Artist, error) {
	if !f.failed {
		f.failed = true
		return musicbrainz.Artist{}, errors.New("musicbrainz returned 503")
	}
	return f.fakeArtistSource.Artist(ctx, mbid)
}

// A person first created from a failed lookup must still gain the biography a
// later successful lookup finds, even though that same lookup also records
// their photo -- after which nothing else would ever come back for them.
func TestEnrichJob_ALaterSuccessfulLookupFillsAPersonAFailedOneLeftBare(t *testing.T) {
	server := testImageServer(t)
	source := &failingOnceArtistSource{fakeArtistSource: fakeArtistSource{
		fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{
				MBID: "rg-1", Title: "Detisisu Binip", PrimaryType: "Album",
				ArtistCredit: []musicbrainz.ArtistCredit{{MBID: "heka", Name: "Heka"}},
			},
			release: musicbrainz.Release{MBID: "rel-1", Title: "Detisisu Binip", Status: "official"},
		},
		artists: map[string]musicbrainz.Artist{
			"heka": {MBID: "heka", Name: "Heka", SortName: "Heka", Country: "IE", WikidataQID: "Q193744"},
		},
	}}
	biographer := &fakeBiographer{biographies: map[string]wikidata.Biography{
		"Q193744": {Extract: "Heka is an Irish singer, songwriter and musician.", ImageURL: server.URL + "/cover.png"},
	}}
	job, opened, queries, _ := newEnrichJobWithImageFetch(t, source, biographer, server.URL)
	createPlaceholder(t, opened, "rg-1", "rel-1")
	createPlaceholder(t, opened, "rg-2", "rel-2")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	person, err := queries.GetPersonByExternalID(t.Context(), librarydb.GetPersonByExternalIDParams{
		Provider: music.ProviderArtist, ExternalID: "heka",
	})
	if err != nil {
		t.Fatalf("GetPersonByExternalID: %v", err)
	}
	if !strings.Contains(person.FieldsJson.String, "Irish singer") || person.Country.String != "IE" {
		t.Errorf("person = %+v, want the later lookup's biography and country filled in", person)
	}
}

// The text search identifies an album at release-group level, with no edition.
// Enrichment walked placeholder editions only, so such an album never gained an
// artist and sat under "Unknown artist" -- Heka's Detisisu Binip among them.
func TestEnrichJob_EnrichesAWorkIdentifiedWithoutAnEdition(t *testing.T) {
	source := &fakeArtistSource{
		fakeSource: fakeSource{rg: musicbrainz.ReleaseGroup{
			MBID: "rg-shepherd", Title: "Detisisu Binip", PrimaryType: "Album", FirstReleaseDate: "1991-11-04",
			ArtistCredit: []musicbrainz.ArtistCredit{{MBID: "heka", Name: "Heka"}},
			Genres:       []musicbrainz.Genre{{Name: "New Age"}},
		}},
		artists: map[string]musicbrainz.Artist{"heka": {MBID: "heka", Name: "Heka", SortName: "Heka"}},
	}
	job, opened, queries := newEnrichJob(t, source)
	resolved, err := library.NewIdentity(opened).ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-shepherd", Confidence: 0.65,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "Detisisu Binip", SortTitle: "detisisu binip"},
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	work, err := queries.GetWork(t.Context(), resolved[0])
	if err != nil {
		t.Fatalf("GetWork: %v", err)
	}
	if !work.PrimaryArtistID.Valid || work.Year.Int64 != 1991 || work.PrimaryType.String != "Album" {
		t.Errorf("work = %+v, want primary artist set, year 1991, type Album", work)
	}
	remaining, err := library.NewIdentity(opened).EditionlessWorks(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("EditionlessWorks: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("EditionlessWorks = %+v, want the enriched work gone from the pass", remaining)
	}
}

// releaseListingSource answers Album for whichever of its releases is asked
// for, and lists them all.
type releaseListingSource struct {
	fakeArtistSource
	releases []musicbrainz.Release
}

func (f *releaseListingSource) Album(_ context.Context, _, releaseMBID string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error) {
	for _, candidate := range f.releases {
		if candidate.MBID == releaseMBID {
			return f.rg, candidate, nil
		}
	}
	return f.rg, musicbrainz.Release{}, nil
}

func (f *releaseListingSource) Releases(context.Context, string) ([]musicbrainz.Release, error) {
	return f.releases, nil
}

// fakeTags answers each path with the tags recorded for its base name.
type fakeTags map[string]mediatool.Tags

func (f fakeTags) ReadTags(_ context.Context, paths []string) ([]mediatool.Probe, error) {
	probes := make([]mediatool.Probe, len(paths))
	for index, one := range paths {
		probes[index] = mediatool.Probe{Path: one, Tags: f[filepath.Base(one)]}
	}
	return probes, nil
}

// An album the text search identified has no edition and so no tracklist:
// Detisisu Binip showed "Missing" with its files on disk. Enrichment now picks
// the release its files fit, refines it, and hands the album on to be realigned.
func TestEnrichJob_ChoosesTheBestFittingEditionForAnAlbumWithoutOne(t *testing.T) {
	track := func(position int, title string) musicbrainz.Track {
		return musicbrainz.Track{Position: position, Title: title}
	}
	source := &releaseListingSource{
		fakeArtistSource: fakeArtistSource{fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{MBID: "rg-shepherd", Title: "Detisisu Binip", PrimaryType: "Album"},
		}},
		releases: []musicbrainz.Release{
			{MBID: "rel-deluxe", Title: "Detisisu Binip", Status: "official", Date: "2009", Media: []musicbrainz.Medium{{
				Position: 1, Tracks: []musicbrainz.Track{track(1, "Detisisu Binip"), track(2, "Covabapoz Fofu"), track(3, "Bonus")},
			}}},
			{MBID: "rel-original", Title: "Detisisu Binip", Status: "official", Date: "1991-11-04", Media: []musicbrainz.Medium{{
				Position: 1, Tracks: []musicbrainz.Track{track(1, "Detisisu Binip"), track(2, "Covabapoz Fofu")},
			}}},
		},
	}
	tags := fakeTags{
		"01.m4a": {"title": "Detisisu Binip", "track": "1", "disc": "1"},
		"02.m4a": {"title": "Covabapoz Fofu", "track": "2", "disc": "1"},
	}
	realigner := &fakeRealigner{}
	job, opened, queries := newEnrichJobRealigning(t, source, nil, realigner, tags)

	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Heka/Detisisu Binip (1991)/01.m4a", SizeBytes: 1, QuickHash: "h1", FirstSeenAt: firstSeen},
		{RelPath: "Heka/Detisisu Binip (1991)/02.m4a", SizeBytes: 1, QuickHash: "h2", FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	links := make([]library.WorkLink, len(registered))
	for index, file := range registered {
		links[index] = library.WorkLink{
			Provider: music.ProviderReleaseGroup, ExternalID: "rg-shepherd", Confidence: 0.65, RootFolderID: 1,
			Work:          library.WorkIdentity{MediaType: "music", CanonicalTitle: "Detisisu Binip", SortTitle: "detisisu binip"},
			MediaObjectID: file.MediaObjectID,
		}
	}
	workIDs, err := library.NewIdentity(opened).ResolveWorks(t.Context(), firstSeen, links)
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	edition, err := queries.GetEditionByExternalID(t.Context(), librarydb.GetEditionByExternalIDParams{
		Provider: music.ProviderRelease, ExternalID: "rel-original",
	})
	if err != nil {
		t.Fatalf("GetEditionByExternalID(rel-original): %v -- want the release the files fit exactly", err)
	}
	if edition.WorkID != workIDs[0] || edition.Kind != "official" {
		t.Errorf("edition = %+v, want refined (official) under work %d", edition, workIDs[0])
	}
	parts, err := queries.ListWorkParts(t.Context(), workIDs[0])
	if err != nil {
		t.Fatalf("ListWorkParts: %v", err)
	}
	if len(parts) != 2 {
		t.Errorf("parts = %d, want the chosen release's two tracks", len(parts))
	}
	if len(realigner.batches) != 1 || !slices.Contains(realigner.batches[0], workIDs[0]) {
		t.Errorf("realigned %v, want work %d handed on to have its files matched", realigner.batches, workIDs[0])
	}
}

// The user's answer (b), 2026-09-28: a file that fits no release still gives its album the
// best-ranked tracklist, so the album stops waiting for metadata.
func TestEnrichJob_TakesTheBestRankedEditionWhenNoFileFits(t *testing.T) {
	source := &releaseListingSource{
		fakeArtistSource: fakeArtistSource{fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{MBID: "rg-single", Title: "Detisisu Binip", PrimaryType: "Single"},
		}},
		releases: []musicbrainz.Release{
			{MBID: "rel-single", Title: "Detisisu Binip", Status: "official", Date: "1991", Media: []musicbrainz.Medium{{
				Position: 1, Tracks: []musicbrainz.Track{{Position: 1, Title: "Detisisu Binip"}},
			}}},
		},
	}
	tags := fakeTags{"01.m4a": {"title": "Sifa Rebup Live", "track": "7", "disc": "2"}}
	realigner := &fakeRealigner{}
	job, opened, queries := newEnrichJobRealigning(t, source, nil, realigner, tags)

	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Heka/Detisisu Binip/01.m4a", SizeBytes: 1, QuickHash: "h1", FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	workIDs, err := library.NewIdentity(opened).ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-single", Confidence: 0.65, RootFolderID: 1,
		Work:          library.WorkIdentity{MediaType: "music", CanonicalTitle: "Detisisu Binip", SortTitle: "detisisu binip"},
		MediaObjectID: registered[0].MediaObjectID,
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	edition, err := queries.GetEditionByExternalID(t.Context(), librarydb.GetEditionByExternalIDParams{
		Provider: music.ProviderRelease, ExternalID: "rel-single",
	})
	if err != nil {
		t.Fatalf("GetEditionByExternalID(rel-single): %v -- want the best-ranked release taken", err)
	}
	if edition.WorkID != workIDs[0] {
		t.Errorf("edition under work %d, want %d", edition.WorkID, workIDs[0])
	}
	parts, err := queries.ListWorkParts(t.Context(), workIDs[0])
	if err != nil {
		t.Fatalf("ListWorkParts: %v", err)
	}
	if len(parts) != 1 {
		t.Errorf("parts = %d, want the single's one track", len(parts))
	}
	if len(realigner.batches) != 1 || !slices.Contains(realigner.batches[0], workIDs[0]) {
		t.Errorf("realigned %v, want work %d handed on", realigner.batches, workIDs[0])
	}
}

// "Refresh metadata" asks the source again past the cache, rewrites the album's genres, and has
// the artist's folders rescanned as a manual refresh.
func TestEnrichJob_RefreshArtistFetchesAgainAndRescans(t *testing.T) {
	source := &fakeArtistSource{
		fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{
				MBID: "rg-1", Title: "KI Pecezinu", PrimaryType: "Album",
				ArtistCredit: []musicbrainz.ArtistCredit{{MBID: "artist-1", Name: "Katupoken"}},
				Genres:       []musicbrainz.Genre{{Name: "alternative rock", Count: 25}},
			},
			release: musicbrainz.Release{MBID: "rel-1", Title: "KI Pecezinu", Status: "official",
				Media: []musicbrainz.Medium{{Position: 1, Tracks: []musicbrainz.Track{{Position: 1, Title: "Sesoge"}}}}},
		},
		artists: map[string]musicbrainz.Artist{"artist-1": {MBID: "artist-1", Name: "Katupoken", SortName: "Katupoken"}},
	}
	realigner := &fakeRealigner{}
	job, opened, queries := newEnrichJobRealigning(t, source, nil, realigner, nil)
	createPlaceholder(t, opened, "rg-1", "rel-1")
	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	person, err := queries.GetPersonByExternalID(t.Context(), librarydb.GetPersonByExternalIDParams{
		Provider: music.ProviderArtist, ExternalID: "artist-1",
	})
	if err != nil {
		t.Fatalf("GetPersonByExternalID: %v", err)
	}

	source.rg.Genres = []musicbrainz.Genre{{Name: "art rock", Count: 30}}
	artistCallsBefore := source.artistCalls
	if err := job.RefreshArtist(t.Context(), person.ID, true); err != nil {
		t.Fatalf("RefreshArtist: %v", err)
	}
	if source.artistCalls == artistCallsBefore {
		t.Error("the artist was answered from the cache; a refresh must ask the source")
	}
	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := library.NewWorks(opened).Detail(t.Context(), work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(detail.Genres, "art rock") {
		t.Errorf("genres = %v, want the refreshed one", detail.Genres)
	}
	if n := len(realigner.manual); n == 0 || !realigner.manual[n-1] || !slices.Equal(realigner.batches[n-1], []int64{work.ID}) {
		t.Errorf("rescans = %v (manual %v), want the album rescanned as a manual refresh", realigner.batches, realigner.manual)
	}
}

// An artist Wikidata knows nothing about still gets TheAudioDB's biography (2026-09-25); one it
// does know keeps Wikidata's, and the second source is not asked.
func TestEnrichJob_FillsWhatWikidataLacksFromTheSecondSource(t *testing.T) {
	source := &fakeArtistSource{
		fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{
				MBID: "rg-1", Title: "Dumucamoko", PrimaryType: "Album",
				ArtistCredit: []musicbrainz.ArtistCredit{{MBID: "artist-1", Name: "Heka"}},
			},
			release: musicbrainz.Release{MBID: "rel-1", Title: "Dumucamoko", Status: "official"},
		},
		artists: map[string]musicbrainz.Artist{"artist-1": {MBID: "artist-1", Name: "Heka", SortName: "Heka"}},
	}
	var asked []string
	job, opened, queries := newEnrichJobRealigning(t, source, &fakeBiographer{}, nil, nil, func(config *music.EnrichJobConfig) {
		config.ArtistArt = func(_ context.Context, mbid string) (string, string, error) {
			asked = append(asked, mbid)
			return "", "Heka is an Irish singer.", nil
		}
	})
	createPlaceholder(t, opened, "rg-1", "rel-1")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	person, err := queries.GetPersonByExternalID(t.Context(), librarydb.GetPersonByExternalIDParams{
		Provider: music.ProviderArtist, ExternalID: "artist-1",
	})
	if err != nil {
		t.Fatalf("GetPersonByExternalID: %v", err)
	}
	if !strings.Contains(person.FieldsJson.String, "Heka is an Irish singer.") || !slices.Contains(asked, "artist-1") {
		t.Errorf("fields_json = %q, asked %v; want the second source's biography", person.FieldsJson.String, asked)
	}
}

// A first scan identifies an album before its tracklist exists; enrichment brings the tracklist and
// must attach the files the scan placed, not leave every album on disk missing until a full rescan
// (the main library, 2026-09-27: 189 of ~200,000 identified files were attached).
func TestEnrichJob_AttachesTheFilesAScanPlacedOnceTheTracklistArrives(t *testing.T) {
	source := &fakeSource{
		rg: musicbrainz.ReleaseGroup{MBID: "rg-1", Title: "KI Pecezinu", PrimaryType: "Album"},
		release: musicbrainz.Release{
			MBID: "rel-1", Title: "KI Pecezinu", Status: "official",
			Media: []musicbrainz.Medium{{Position: 1, Tracks: []musicbrainz.Track{
				{Position: 1, Number: "1", Title: "Sesoge", DurationMs: 284400, RecordingMBID: "rec-1"},
				{Position: 2, Number: "2", Title: "Zosidedu Bukerez", DurationMs: 383000, RecordingMBID: "rec-2"},
			}}},
		},
	}
	desired := new(library.Desired)
	job, opened, queries := newEnrichJobRealigning(t, source, nil, nil, nil, func(config *music.EnrichJobConfig) {
		config.Desired = desired
	})
	*desired = library.NewDesired(opened)

	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Katupoken/KI Pecezinu (1997)/01.flac", SizeBytes: 1, QuickHash: "h1", FirstSeenAt: firstSeen},
		{RelPath: "Katupoken/KI Pecezinu (1997)/02.flac", SizeBytes: 1, QuickHash: "h2", FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	links := make([]library.WorkLink, len(registered))
	for index, file := range registered {
		links[index] = library.WorkLink{
			Provider: music.ProviderReleaseGroup, ExternalID: "rg-1", Confidence: 1, RootFolderID: 1, MediaObjectID: file.MediaObjectID,
			Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"},
			Edition: &library.EditionLink{
				Provider: music.ProviderRelease, ExternalID: "rel-1", Confidence: 1,
				Edition: library.EditionIdentity{Kind: "unknown", Label: "rel-1"},
			},
		}
	}
	workIDs, err := library.NewIdentity(opened).ResolveWorks(t.Context(), firstSeen, links)
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	categories := fakeCategories{discoveryCategoryID: {ID: discoveryCategoryID, MonitorDiscovered: settings.MonitorDiscoveredMonitored}}
	if err := music.DiscoverWanted(t.Context(), desired, categories, clock.NewFakeAtEpoch(), 1, discoveryCategoryID, workIDs[0], []music.TrackFile{
		{MediaObjectID: registered[0].MediaObjectID, Disc: 1, Track: 1, Title: "Sesoge", RecordingMBID: "rec-1"},
		{MediaObjectID: registered[1].MediaObjectID, Disc: 1, Track: 2, Title: "Zosidedu Bukerez", RecordingMBID: "rec-2"},
	}); err != nil {
		t.Fatalf("DiscoverWanted: %v", err)
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	items, err := queries.ListDesiredItemsForWork(t.Context(), workIDs[0])
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %v, %v; want the one album item", items, err)
	}
	if item := items[0]; item.State != "COMPLETE" || item.PresentCount != 2 {
		t.Errorf("item state %s present %d, want COMPLETE with both files", item.State, item.PresentCount)
	}
}

// A work is named after its release group, not after the album tag of whichever file a scan met
// first: every night of "Live! At the Rainbow" was "Live at the Fikarak, 1et Fapo 1977" (2026-09-27).
func TestEnrichJob_NamesTheWorkAfterItsReleaseGroup(t *testing.T) {
	source := &fakeSource{
		rg:      musicbrainz.ReleaseGroup{MBID: "rg-rainbow", Title: "Live! At the Rainbow", PrimaryType: "Album"},
		release: musicbrainz.Release{MBID: "rel-2nd", Title: "Live at The Rainbow, 2nd June 1977", Status: "official"},
	}
	job, opened, queries := newEnrichJob(t, source)
	workIDs, err := library.NewIdentity(opened).ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-rainbow", Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "Live at the Fikarak, 1et Fapo 1977", SortTitle: "live at the fikarak, 1et fapo 1977"},
		Edition: &library.EditionLink{
			Provider: music.ProviderRelease, ExternalID: "rel-2nd", Confidence: 1,
			Edition: library.EditionIdentity{Kind: "unknown", Label: "rel-2nd"},
		},
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	work, err := queries.GetWork(t.Context(), workIDs[0])
	if err != nil {
		t.Fatalf("GetWork: %v", err)
	}
	if work.CanonicalTitle != "Live! At the Rainbow" || work.SortTitle != music.SortTitle("Live! At the Rainbow") {
		t.Errorf("work titled %q / %q, want the release group's title", work.CanonicalTitle, work.SortTitle)
	}
}

// overlapSource holds each lookup until another is in flight, recording the most at once.
type overlapSource struct {
	fakeSource
	lock      sync.Mutex
	inFlight  int
	most      int
	arrived   chan struct{}
	arrivedAt sync.Once
}

func (o *overlapSource) Album(ctx context.Context, rg, release string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error) {
	o.lock.Lock()
	o.inFlight++
	o.most = max(o.most, o.inFlight)
	if o.inFlight >= 2 {
		o.arrivedAt.Do(func() { close(o.arrived) })
	}
	o.lock.Unlock()
	select {
	case <-o.arrived:
	case <-time.After(2 * time.Second):
	}
	o.lock.Lock()
	o.inFlight--
	o.lock.Unlock()
	return o.fakeSource.Album(ctx, rg, release)
}

// BrainzMash answers in about a second but allows ten a second: a batch's lookups must overlap.
func TestEnrichJob_LooksUpABatchsAlbumsConcurrently(t *testing.T) {
	source := &overlapSource{
		fakeSource: fakeSource{rg: musicbrainz.ReleaseGroup{MBID: "rg-1", Title: "KI Pecezinu", PrimaryType: "Album"}},
		arrived:    make(chan struct{}),
	}
	job, opened, _ := newEnrichJob(t, source)
	for index := range 4 {
		createPlaceholder(t, opened, fmt.Sprintf("rg-%d", index), fmt.Sprintf("rel-%d", index))
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if source.most < 2 || source.calls != 4 {
		t.Errorf("at most %d lookups at once over %d calls, want overlapping lookups, each album once", source.most, source.calls)
	}
}

// Wikidata paces one request a second: a batch of albums is described in one call, not one each.
func TestEnrichJob_DescribesABatchsAlbumsInOneCall(t *testing.T) {
	source := &fakeSource{rg: musicbrainz.ReleaseGroup{MBID: "rg-0", Title: "Vorn", PrimaryType: "Album", WikidataQID: "Q7"}}
	biographer := &fakeBiographer{biographies: map[string]wikidata.Biography{"Q7": {Description: "1997 studio album"}}}
	job, opened, queries := newEnrichJobWithBiographer(t, source, biographer)
	for index := range 3 {
		createPlaceholder(t, opened, fmt.Sprintf("rg-%d", index), fmt.Sprintf("rel-%d", index))
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if biographer.batches != 1 {
		t.Errorf("described in %d calls, want 1 for the whole batch", biographer.batches)
	}
	for index := range 3 {
		work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
			Provider: music.ProviderReleaseGroup, ExternalID: fmt.Sprintf("rg-%d", index),
		})
		if err != nil {
			t.Fatalf("GetWorkByExternalID: %v", err)
		}
		if work.Description.String != "1997 studio album" {
			t.Errorf("work %d description = %q, want the batch's answer", work.ID, work.Description.String)
		}
	}
}

// A run on a fresh library lasts hours: every checkpoint hands refined albums on to be realigned
// and counted, rather than all of them at the very end.
func TestEnrichJob_HandsRefinedWorksOnAtEveryCheckpoint(t *testing.T) {
	source := &fakeSource{rg: musicbrainz.ReleaseGroup{MBID: "rg-0", Title: "Vorn", PrimaryType: "Album"}}
	realigner := &fakeRealigner{}
	job, opened, _ := newEnrichJobRealigning(t, source, nil, realigner, nil,
		func(config *music.EnrichJobConfig) { config.CheckpointBatches = 1 })
	for index := range 21 {
		createPlaceholder(t, opened, fmt.Sprintf("rg-%d", index), fmt.Sprintf("rel-%d", index))
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sizes := make([]int, len(realigner.batches))
	for index, batch := range realigner.batches {
		sizes[index] = len(batch)
	}
	if !slices.Equal(sizes, []int{20, 1}) || realigner.refined != 2 {
		t.Errorf("handed on in batches of %v, counted %d times; want [20 1] and 2", sizes, realigner.refined)
	}
}

// An artist's biography costs a Wikidata request: one run asks once per artist, not once per album.
func TestEnrichJob_ResolvesEachArtistOncePerRun(t *testing.T) {
	source := &fakeArtistSource{
		fakeSource: fakeSource{rg: musicbrainz.ReleaseGroup{
			MBID: "rg-0", Title: "Vorn", PrimaryType: "Album",
			ArtistCredit: []musicbrainz.ArtistCredit{{MBID: "artist-1", Name: "Katupoken"}},
		}},
		artists: map[string]musicbrainz.Artist{
			"artist-1": {MBID: "artist-1", Name: "Katupoken", SortName: "Katupoken", WikidataQID: "Q1"},
		},
	}
	biographer := &fakeBiographer{biographies: map[string]wikidata.Biography{"Q1": {Description: "English band"}}}
	job, opened, queries := newEnrichJobWithBiographer(t, source, biographer)
	for index := range 3 {
		createPlaceholder(t, opened, fmt.Sprintf("rg-%d", index), fmt.Sprintf("rel-%d", index))
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if biographer.calls != 1 {
		t.Errorf("artist biography fetched %d times, want once for three albums", biographer.calls)
	}
	for index := range 3 {
		work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
			Provider: music.ProviderReleaseGroup, ExternalID: fmt.Sprintf("rg-%d", index),
		})
		if err != nil {
			t.Fatalf("GetWorkByExternalID: %v", err)
		}
		if !work.PrimaryArtistID.Valid {
			t.Errorf("work %d has no artist; every album must still be credited", work.ID)
		}
	}
}

// retiredReleaseSource answers 404 for a release it no longer lists, as MusicBrainz does for a
// retired one, while its release group and the other releases stay.
type retiredReleaseSource struct{ releaseListingSource }

func (f *retiredReleaseSource) Album(ctx context.Context, rg, releaseMBID string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error) {
	if releaseMBID != "" && !slices.ContainsFunc(f.releases, func(r musicbrainz.Release) bool { return r.MBID == releaseMBID }) {
		return musicbrainz.ReleaseGroup{}, musicbrainz.Release{}, &httpx.StatusError{Provider: "fake", Status: http.StatusNotFound}
	}
	return f.releaseListingSource.Album(ctx, rg, releaseMBID)
}

// A release MBID in the tags that MusicBrainz has since retired left its edition a placeholder
// forever. While the release group lives, an edition is chosen by the files instead, and the dead
// placeholder is withdrawn so it is not asked about every run.
func TestEnrichJob_ReplacesARetiredReleaseByOneTheFilesFit(t *testing.T) {
	source := &retiredReleaseSource{releaseListingSource{
		fakeArtistSource: fakeArtistSource{fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{MBID: "rg-shepherd", Title: "Detisisu Binip", PrimaryType: "Album"},
		}},
		releases: []musicbrainz.Release{{MBID: "rel-current", Title: "Detisisu Binip", Status: "official", Media: []musicbrainz.Medium{{
			Position: 1, Tracks: []musicbrainz.Track{{Position: 1, Title: "Detisisu Binip"}, {Position: 2, Title: "Covabapoz Fofu"}},
		}}}},
	}}
	tags := fakeTags{
		"01.m4a": {"title": "Detisisu Binip", "track": "1", "disc": "1"},
		"02.m4a": {"title": "Covabapoz Fofu", "track": "2", "disc": "1"},
	}
	job, opened, queries := newEnrichJobRealigning(t, source, nil, &fakeRealigner{}, tags)
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Heka/Detisisu Binip (1991)/01.m4a", SizeBytes: 1, QuickHash: "h1", FirstSeenAt: firstSeen},
		{RelPath: "Heka/Detisisu Binip (1991)/02.m4a", SizeBytes: 1, QuickHash: "h2", FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	links := make([]library.WorkLink, len(registered))
	for index, file := range registered {
		links[index] = library.WorkLink{
			Provider: music.ProviderReleaseGroup, ExternalID: "rg-shepherd", Confidence: 1, RootFolderID: 1,
			Work:          library.WorkIdentity{MediaType: "music", CanonicalTitle: "Detisisu Binip", SortTitle: "detisisu binip"},
			MediaObjectID: file.MediaObjectID,
			Edition: &library.EditionLink{Provider: music.ProviderRelease, ExternalID: "rel-retired", Confidence: 1,
				Edition: library.EditionIdentity{Kind: "unknown", Label: "rel-retired"}},
		}
	}
	if _, err := library.NewIdentity(opened).ResolveWorks(t.Context(), firstSeen, links); err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	current, err := queries.GetEditionByExternalID(t.Context(), librarydb.GetEditionByExternalIDParams{
		Provider: music.ProviderRelease, ExternalID: "rel-current",
	})
	if err != nil || current.Kind != "official" {
		t.Fatalf("current edition = %+v, %v; want it chosen and refined", current, err)
	}
	retired, err := queries.GetEditionByExternalID(t.Context(), librarydb.GetEditionByExternalIDParams{
		Provider: music.ProviderRelease, ExternalID: "rel-retired",
	})
	if err != nil || retired.Kind != "withdrawn" {
		t.Errorf("retired edition = %+v, %v; want withdrawn", retired, err)
	}
	placeholders, err := library.NewIdentity(opened).PlaceholderEditions(t.Context(), 0, 10)
	if err != nil || len(placeholders) != 0 {
		t.Errorf("placeholders left = %+v, %v; want none to ask about again", placeholders, err)
	}
}

// M1f: MusicBrainz merged the release group this album was tagged with; the old id still answers,
// under the new one. Enrichment moves the work to the new id so lookups and matching use it.
func TestEnrichJob_FollowsAMergedReleaseGroup(t *testing.T) {
	source := &releaseListingSource{
		fakeArtistSource: fakeArtistSource{fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{MBID: "rg-merged-into", Title: "Detisisu Binip", PrimaryType: "Album"},
		}},
		releases: []musicbrainz.Release{{MBID: "rel-current", Title: "Detisisu Binip", Status: "official", Media: []musicbrainz.Medium{{
			Position: 1, Tracks: []musicbrainz.Track{{Position: 1, Title: "Detisisu Binip"}},
		}}}},
	}
	job, opened, queries := newEnrichJobRealigning(t, source, nil, &fakeRealigner{}, fakeTags{
		"01.m4a": {"title": "Detisisu Binip", "track": "1", "disc": "1"},
	})
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Heka/Detisisu Binip (1991)/01.m4a", SizeBytes: 1, QuickHash: "h1", FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	if _, err := library.NewIdentity(opened).ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-merged-away", Confidence: 1, RootFolderID: 1,
		Work:          library.WorkIdentity{MediaType: "music", CanonicalTitle: "Detisisu Binip", SortTitle: "detisisu binip"},
		MediaObjectID: registered[0].MediaObjectID,
		Edition: &library.EditionLink{Provider: music.ProviderRelease, ExternalID: "rel-current", Confidence: 1,
			Edition: library.EditionIdentity{Kind: "unknown", Label: "rel-current"}},
	}}); err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-merged-into",
	}); err != nil {
		t.Errorf("the work is not found under the release group it was merged into: %v", err)
	}
}

// goneSource answers 404 for every album: the release group was removed from MusicBrainz.
type goneSource struct{ releaseListingSource }

func (f *goneSource) Album(context.Context, string, string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error) {
	return musicbrainz.ReleaseGroup{}, musicbrainz.Release{}, &httpx.StatusError{Provider: "fake", Status: http.StatusNotFound}
}

// M1f: an album whose release group exists nowhere any more goes back to Review instead of being
// logged and left a placeholder forever.
func TestEnrichJob_ReturnsAnAlbumWhoseReleaseGroupIsGoneToReview(t *testing.T) {
	job, opened, queries := newEnrichJobRealigning(t, &goneSource{}, nil, &fakeRealigner{}, fakeTags{})
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Heka/Detisisu Binip (1991)/01.m4a", SizeBytes: 1, QuickHash: "h1", FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	if _, err := library.NewIdentity(opened).ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-gone", Confidence: 1, RootFolderID: 1,
		Work:          library.WorkIdentity{MediaType: "music", CanonicalTitle: "Detisisu Binip", SortTitle: "detisisu binip"},
		MediaObjectID: registered[0].MediaObjectID,
		Edition: &library.EditionLink{Provider: music.ProviderRelease, ExternalID: "rel-gone", Confidence: 1,
			Edition: library.EditionIdentity{Kind: "unknown", Label: "rel-gone"}},
	}}); err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	entry, err := queries.GetUnidentified(t.Context(), registered[0].MediaObjectID)
	if err != nil || !strings.Contains(entry.CandidatesJson, `"external_id":"rg-gone"`) {
		t.Fatalf("queue entry = %+v, %v; want the file back in Review with the gone release group as a hint", entry, err)
	}
	if _, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-gone",
	}); err == nil {
		t.Error("the album nobody asked for is still in the library")
	}
}

// An album's label comes with its release: linked as the work's label organisation, and "[no label]" links none.
func TestEnrichJob_LinksTheReleasesLabel(t *testing.T) {
	for _, tc := range []struct {
		name, mbid string
		want       int
	}{
		{"Mute", "label-1", 1},
		{"[no label]", "157afde4-4bf5-4039-8ad2-5a15acc85176", 0},
	} {
		source := &fakeSource{
			rg:      musicbrainz.ReleaseGroup{MBID: "rg-1", Title: "KI Pecezinu", PrimaryType: "Album"},
			release: musicbrainz.Release{MBID: "rel-1", Title: "KI Pecezinu", Status: "official", LabelName: tc.name, LabelMBID: tc.mbid},
		}
		organisations := &library.Organisations{}
		job, opened, _ := newEnrichJobRealigning(t, source, nil, nil, nil, func(config *music.EnrichJobConfig) {
			config.Organisations = organisations
		})
		*organisations = library.NewOrganisations(opened)
		createPlaceholder(t, opened, "rg-1", "rel-1")
		if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		var linked int
		if err := opened.Read().QueryRowContext(t.Context(),
			`SELECT COUNT(*) FROM work_organisation wo JOIN organisation o ON o.id = wo.organisation_id
			 JOIN organisation_external_id x ON x.organisation_id = o.id AND x.provider = 'musicbrainz:label' AND x.external_id = ?
			 WHERE wo.role = 'label' AND o.kind = 'label' AND o.name = ?`, tc.mbid, tc.name).Scan(&linked); err != nil || linked != tc.want {
			t.Errorf("%s: labels linked = %d, %v; want %d", tc.name, linked, err, tc.want)
		}
		if asked, _ := organisations.WorksToAsk(t.Context(), "music", music.ProviderReleaseGroup, music.RoleLabel, "musicbrainz", 0, 10); len(asked) != 0 {
			t.Errorf("%s: the album is still to be asked about its label: %+v", tc.name, asked)
		}
	}
}
