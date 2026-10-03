package tv_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func TestRefreshSweepReadsASeriesOnlyWhenItsStatusMakesItDue(t *testing.T) {
	service, opened := newService(t, searchSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	service.Reader = library.NewSeriesReader(opened)
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 3, ProfileID: 1, Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	grown := fixtureSkyhook()
	grown.Episodes = append(grown.Episodes, skyhook.Episode{TVDBID: 9, SeasonNumber: 2, EpisodeNumber: 4, Title: "New", AirDate: "2026-10-19"})
	service.Skyhook = searchSkyhook{series: grown}
	fake, ok := service.Clock.(*clock.Fake)
	if !ok {
		t.Fatal("the service clock is not the fake")
	}
	job := tv.NewRefreshJob(tv.RefreshConfig{Reader: service.Reader, Service: service, DefaultProfileID: 1, Categories: categories{}, Clock: fake})
	episodes := func() int {
		detail, err := service.Reader.Get(t.Context(), 3, added.WorkID, fake.Now())
		if err != nil {
			t.Fatal(err)
		}
		return len(detail.Episodes)
	}

	fake.Set(addNow.Add(time.Hour))
	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if got := episodes(); got != 6 {
		t.Errorf("episodes = %d an hour after the add, want the series left alone", got)
	}
	fake.Set(addNow.Add(13 * time.Hour))
	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if got := episodes(); got != 7 {
		t.Errorf("episodes = %d after twelve hours, want the new episode", got)
	}
}
