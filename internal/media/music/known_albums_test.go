package music_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

type discographySource struct{ groups []musicbrainz.ReleaseGroup }

func (discographySource) Name() string { return "musicbrainz" }
func (discographySource) Album(context.Context, string, string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error) {
	return musicbrainz.ReleaseGroup{}, musicbrainz.Release{}, musicbrainz.ErrNotFound
}
func (d discographySource) ArtistReleaseGroups(context.Context, string) ([]musicbrainz.ReleaseGroup, error) {
	return d.groups, nil
}

type wikidataArtists map[string]string

func (w wikidataArtists) MusicBrainzArtists(_ context.Context, qids []string) (map[string]string, error) {
	answers := map[string]string{}
	for _, qid := range qids {
		if id, ok := w[qid]; ok {
			answers[qid] = id
		}
	}
	return answers, nil
}

func openKnownAlbumsLibrary(t *testing.T) *store.Store {
	t.Helper()
	directory := t.TempDir()
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, directory, owner); err != nil {
		t.Fatal(err)
	}
	return opened
}

func newMetadataCache(t *testing.T) metacache.Cache {
	t.Helper()
	directory := t.TempDir()
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "metadata", Path: filepath.Join(directory, "metadata.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, directory, owner); err != nil {
		t.Fatal(err)
	}
	return metacache.New(opened)
}

func TestKnownAlbumsListsTheStudioAlbumsAndEPsTheLibraryLacksNewestFirst(t *testing.T) {
	libraryStore := openKnownAlbumsLibrary(t)
	identity := library.NewIdentity(libraryStore)
	if _, err := identity.ResolveWorks(t.Context(), clock.NewFakeAtEpoch().Now(), []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: "rg-held", Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "Held", SortTitle: "held"},
	}}); err != nil {
		t.Fatal(err)
	}
	source := discographySource{groups: []musicbrainz.ReleaseGroup{
		{MBID: "rg-old", Title: "First Light", PrimaryType: "Album", FirstReleaseDate: "2009-04-01"},
		{MBID: "rg-held", Title: "Held", PrimaryType: "Album", FirstReleaseDate: "2012"},
		{MBID: "rg-live", Title: "Live at Somewhere", PrimaryType: "Album", SecondaryTypes: []string{"Live"}, FirstReleaseDate: "2014"},
		{MBID: "rg-single", Title: "A Single", PrimaryType: "Single", FirstReleaseDate: "2015"},
		{MBID: "rg-ep", Title: "Short Story", PrimaryType: "EP", FirstReleaseDate: "2016-09"},
	}}
	albums := music.KnownAlbums{
		Wikidata: wikidataArtists{"Q42": "artist-1"},
		Provider: musicbrainz.New(musicbrainz.Config{Sources: []musicbrainz.Source{source}, Cache: newMetadataCache(t), Clock: clock.NewFakeAtEpoch()}),
		Identity: identity,
	}
	artist, err := albums.ArtistFor(t.Context(), "q42")
	if err != nil || artist != "artist-1" {
		t.Fatalf("ArtistFor = %q, %v, want artist-1", artist, err)
	}
	got, err := albums.Albums(t.Context(), artist)
	if err != nil {
		t.Fatal(err)
	}
	want := []media.KnownFor{
		{Provider: music.ProviderReleaseGroup, ExternalID: "rg-ep", Kind: "album", AddType: music.MediaType, Title: "Short Story", Year: 2016, Role: "EP",
			PosterURL: "https://coverartarchive.org/release-group/rg-ep/front-250"},
		{Provider: music.ProviderReleaseGroup, ExternalID: "rg-old", Kind: "album", AddType: music.MediaType, Title: "First Light", Year: 2009, Role: "Album",
			PosterURL: "https://coverartarchive.org/release-group/rg-old/front-250"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("albums = %+v\nwant %+v", got, want)
	}
}
