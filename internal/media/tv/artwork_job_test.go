package tv_test

import (
	"context"
	"sync"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
)

type fakePictures struct {
	mu     sync.Mutex
	have   map[string]bool
	ensure []string
}

func (f *fakePictures) Cached(address string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.have[address]
}

func (f *fakePictures) Ensure(_ context.Context, address string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure = append(f.ensure, address)
	f.have[address] = true
	return "key", nil
}

func TestArtworkJobFetchesThePostersNotYetCachedAndNothingTwice(t *testing.T) {
	show := fixtureSkyhook()
	show.Images = []skyhook.Image{{CoverType: "Poster", URL: "https://artworks.thetvdb.com/p.jpg"}}
	service, opened := newService(t, fixedSkyhook{show}, fakeTVmaze{err: tvmaze.ErrNotFound})
	if _, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: 3, Monitor: tv.MonitorAll}); err != nil {
		t.Fatal(err)
	}
	pictures := &fakePictures{have: map[string]bool{}}
	job := tv.NewArtworkJob(tv.ArtworkConfig{Reader: library.NewSeriesReader(opened), Cache: pictures})

	for range 2 {
		if err := job.Run(t.Context(), jobs.Job{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(pictures.ensure) != 1 || pictures.ensure[0] != "https://artworks.thetvdb.com/p.jpg" {
		t.Errorf("fetched = %v, want the poster once, however many runs", pictures.ensure)
	}
}
