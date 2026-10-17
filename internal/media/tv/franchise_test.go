package tv_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

type fakeFranchises struct {
	franchise wikidata.Franchise
	members   []wikidata.FranchiseSeries
}

func (f fakeFranchises) SeriesFranchise(_ context.Context, tvdb, _ int64) (wikidata.Franchise, bool, error) {
	return f.franchise, tvdb == 1234, nil
}

func (f fakeFranchises) FranchiseSeriesOf(context.Context, string) ([]wikidata.FranchiseSeries, error) {
	return f.members, nil
}

func TestTheFranchisePassGathersTheSeriesTheLibraryHoldsAndOffersTheOthers(t *testing.T) {
	service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: 1, Monitor: tv.MonitorNone})
	if err != nil {
		t.Fatal(err)
	}
	source := fakeFranchises{
		franchise: wikidata.Franchise{QID: "Q4321", Label: "Example Franchise"},
		members: []wikidata.FranchiseSeries{
			{QID: "Q4321", Label: "Example Show", TVDB: 1234, Year: 2020},
			{QID: "Q9", Label: "Example: Next Generation", TVDB: 999, Year: 2024},
		},
	}
	franchises := library.NewFranchises(opened)
	pass := tv.FranchisePass{Source: source, Franchises: franchises, Crossovers: library.NewCrossovers(opened), Series: service.Series, Logger: slog.New(slog.DiscardHandler)}
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}

	listed, err := franchises.InCategory(t.Context(), "tv", 7)
	if err != nil || len(listed) != 1 || listed[0].Name != "Example Franchise" || len(listed[0].Members) != 1 || listed[0].Members[0].WorkID != added.WorkID {
		t.Fatalf("collections = %+v, %v; want the franchise holding the series the library has", listed, err)
	}
	if again, _ := franchises.SeriesWithoutFranchiseCheck(t.Context(), 10); len(again) != 0 {
		t.Errorf("a series is asked about once: %+v", again)
	}

	provider := tv.Franchising{Source: source, Franchises: franchises, Series: service.Series}
	missing, err := provider.MissingFranchiseMembers(t.Context(), 7, listed[0].ID)
	if err != nil || len(missing) != 1 || missing[0].Provider != "tvdb" || missing[0].ExternalID != "999" || missing[0].WorkID != 0 {
		t.Errorf("missing = %+v, %v; want the series the library lacks, to add by its TheTVDB id", missing, err)
	}
}
