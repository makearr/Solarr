package music_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/metadata/metacache"
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
	"github.com/makearr/MediaNexus/internal/metadata/wikidata"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/migrations"
)

// fakeSource answers one canned Album per test, so enrich_job_test.go can pin
// exactly what refinementFor (unexported) does with a realistic MusicBrainz
// answer, through EnrichJob's own black-box behaviour -- the same indirection
// title.go's sortTitle/albumTitleFallback already rely on Consumer's tests for.
type fakeSource struct {
	rg      musicbrainz.ReleaseGroup
	release musicbrainz.Release
	err     error
	calls   int
}

func (f *fakeSource) Name() string { return "fake" }
func (f *fakeSource) Album(context.Context, string, string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error) {
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
	job := music.NewEnrichJob(music.EnrichJobConfig{
		Identity: identity, People: library.NewPeople(libraryStore), Provider: provider,
		Biography: biographer, Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})
	return job, libraryStore, librarydb.New(libraryStore.Read())
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
	remaining, err := identity.PlaceholderEditions(t.Context(), 10)
	if err != nil {
		t.Fatalf("PlaceholderEditions: %v", err)
	}
	// Still a placeholder: the failed edition is left for the next run, not
	// abandoned or half-written.
	if len(remaining) != 1 {
		t.Errorf("PlaceholderEditions = %+v, want the failed edition still waiting", remaining)
	}
}
