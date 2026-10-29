package music_test

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// fakeLibraries holds every album in library 7 and every artist in library 9.
type fakeLibraries struct{}

func (fakeLibraries) OfWork(context.Context, int64) int64   { return 7 }
func (fakeLibraries) OfPerson(context.Context, int64) int64 { return 9 }

// orderLog records the library each lookup asked its order for.
type orderLog struct {
	mu    sync.Mutex
	asked []int64
}

func (o *orderLog) order(library int64) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.asked = append(o.asked, library)
	return []string{"fake"}
}

func (o *orderLog) take() []int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	asked := o.asked
	o.asked = nil
	return asked
}

// Enrichment looks an album and its credited artists up in the album's library; an artist's refresh looks the artist
// up in the artist's, and each of their albums in that album's.
func TestEnrichJob_LooksEachAlbumAndArtistUpInItsOwnLibrary(t *testing.T) {
	source := &fakeArtistSource{
		fakeSource: fakeSource{
			rg: musicbrainz.ReleaseGroup{
				MBID: "rg-1", Title: "KI Pecezinu", PrimaryType: "Album",
				ArtistCredit: []musicbrainz.ArtistCredit{{MBID: "artist-1", Name: "Katupoken"}},
			},
			release: musicbrainz.Release{MBID: "rel-1", Title: "KI Pecezinu", Status: "official"},
		},
		artists: map[string]musicbrainz.Artist{"artist-1": {MBID: "artist-1", Name: "Katupoken", SortName: "Katupoken"}},
	}
	log := &orderLog{}
	job, opened, queries := newEnrichJobRealigning(t, source, nil, nil, nil, func(config *music.EnrichJobConfig) {
		config.Libraries = fakeLibraries{}
		config.Provider = musicbrainz.New(musicbrainz.Config{
			Sources: []musicbrainz.Source{source}, LiveOrder: log.order, Cache: newMetadataCache(t),
			Clock: clock.NewFakeAtEpoch(), Logger: slog.New(slog.DiscardHandler),
		})
	})
	createPlaceholder(t, opened, "rg-1", "rel-1")

	if err := job.Run(t.Context(), jobs.Job{Type: music.EnrichJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := log.take(), []int64{7, 7}; !slices.Equal(got, want) {
		t.Errorf("enrichment asked the orders of libraries %v, want %v (the album, then its artist)", got, want)
	}

	person, err := queries.GetPersonByExternalID(t.Context(), librarydb.GetPersonByExternalIDParams{
		Provider: music.ProviderArtist, ExternalID: "artist-1",
	})
	if err != nil {
		t.Fatalf("GetPersonByExternalID: %v", err)
	}
	if err := job.RefreshArtist(t.Context(), person.ID, true); err != nil {
		t.Fatalf("RefreshArtist: %v", err)
	}
	if got, want := log.take(), []int64{9, 9, 7, 7}; !slices.Equal(got, want) {
		t.Errorf("the refresh asked the orders of libraries %v, want %v (artist, discography, album, its artist)", got, want)
	}
}
