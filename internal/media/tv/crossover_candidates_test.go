package tv_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// seriesByID answers Skyhook with a series per TheTVDB id.
type seriesByID map[int64]skyhook.Series

func (s seriesByID) Series(_ context.Context, id int64) (skyhook.Series, error) { return s[id], nil }
func (s seriesByID) Search(context.Context, string) ([]skyhook.Series, error) {
	return nil, nil
}

// episodeListsByID answers the pass with a series per TheTVDB id, as the providers list it.
type episodeListsByID map[int64]tv.Series

func (e episodeListsByID) Fetch(_ context.Context, ids tv.IDs) (tv.Series, error) {
	return e[ids.TVDB], nil
}

func eventSeries(id int64, title string, episodes ...skyhook.Episode) skyhook.Series {
	return skyhook.Series{TVDBID: id, Title: title, FirstAired: "2018-01-01", Status: "Continuing", Episodes: episodes}
}

func eventEpisode(id int64, season, number int, title, date string) skyhook.Episode {
	return skyhook.Episode{TVDBID: id, SeasonNumber: season, EpisodeNumber: number, Title: title, AirDate: date}
}

func listOf(title string, episodes ...tv.Episode) tv.Series {
	return tv.Series{Title: title, Episodes: episodes}
}

func TestTheFranchisePassFindsACrossoverInProviderListsAndJoinsTheSeriesItTouches(t *testing.T) {
	sky := seriesByID{
		11: eventSeries(11, "Invented Show", eventEpisode(111, 1, 5, "Great Night (1)", "2018-12-09")),
		22: eventSeries(22, "Invented Spinoff", eventEpisode(222, 1, 8, "Great Night (3)", "2018-12-11")),
	}
	service, opened := newService(t, sky, fakeTVmaze{err: tvmaze.ErrNotFound})
	for _, id := range []int64{11, 22} {
		if _, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: id}, CategoryID: 7, ProfileID: 1, Monitor: tv.MonitorNone}); err != nil {
			t.Fatal(err)
		}
	}
	// Wikidata puts each held series in a franchise of its own with the sequel the library lacks.
	source := fakeFranchises{
		ties: map[wikidata.SeriesRef]wikidata.SeriesTies{
			{TVDB: 11}: {Franchises: []wikidata.Franchise{{QID: "Q100", Label: "Show Family", Named: true}}},
			{TVDB: 22}: {Franchises: []wikidata.Franchise{{QID: "Q200", Label: "Spinoff Family", Named: true}}},
		},
		members: map[string][]wikidata.FranchiseSeries{
			"Q100": {{QID: "Q1", Label: "Invented Show", TVDB: 11}, {QID: "Q3", Label: "Invented Sequel", TVDB: 33}},
			"Q200": {{QID: "Q2", Label: "Invented Spinoff", TVDB: 22}, {QID: "Q3", Label: "Invented Sequel", TVDB: 33}},
		},
	}
	lists := episodeListsByID{
		11: listOf("Invented Show", tv.Episode{Season: 1, Number: 5, Title: "Great Night (1)", AirDate: "2018-12-09"}),
		22: listOf("Invented Spinoff", tv.Episode{Season: 1, Number: 8, Title: "Great Night (3)", AirDate: "2018-12-11"}),
		33: listOf("Invented Sequel", tv.Episode{Season: 2, Number: 3, Title: "Great Night (2)", AirDate: "2018-12-10"}),
	}
	franchises, crossovers := library.NewFranchises(opened), library.NewCrossovers(opened)
	pass := tv.FranchisePass{
		Source: source, Franchises: franchises, Crossovers: crossovers, Series: service.Series, Episodes: lists, Clock: clock.NewFakeAtEpoch(),
		Logger: slog.New(slog.DiscardHandler),
	}
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}

	events, err := crossovers.List(t.Context(), "tv")
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v, %v; want the one event both families found, merged", events, err)
	}
	event := events[0]
	if event.Name != "Great Night" || event.Total != 3 || len(event.Members) != 2 || len(event.Missing) != 1 || event.Missing[0].Number != 2 ||
		event.Missing[0].ExternalID != "33" || event.Missing[0].WorkID != 0 || event.Gaps != nil {
		t.Errorf("event = %+v; want Great Night, 3 parts, two held and part 2 an episode of the sequel the library lacks", event)
	}

	collections, err := franchises.InCategory(t.Context(), "tv", 7)
	if err != nil {
		t.Fatal(err)
	}
	var joined library.FranchiseRow
	for _, collection := range collections {
		if len(collection.Members) == 2 {
			joined = collection
		}
	}
	if joined.ID == 0 {
		t.Fatalf("collections = %+v; want the two series that share the event in one collection", collections)
	}

	// Taking one out sticks: the next pass does not put it back, though the event is still there.
	if err := franchises.Dismiss(t.Context(), joined.ID, joined.Members[1].WorkID); err != nil {
		t.Fatal(err)
	}
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if collections, err = franchises.InCategory(t.Context(), "tv", 7); err != nil {
		t.Fatal(err)
	}
	for _, collection := range collections {
		if len(collection.Members) > 1 {
			t.Errorf("collection %q holds %d series again after one was taken out", collection.Name, len(collection.Members))
		}
	}
}
