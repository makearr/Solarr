package library_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

var seriesAt = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func sampleSeries() library.SeriesWrite {
	runtime := int64(2_400_000)
	available := int64(1_578_279_600)
	return library.SeriesWrite{
		Work:             library.WorkIdentity{MediaType: "tv", CanonicalTitle: "Example Show", SortTitle: "example show"},
		OriginalLanguage: "eng", RuntimeMs: &runtime, Description: "Plot.",
		IDs:     []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: "1234"}, {Provider: "tvmaze", ExternalID: "42"}},
		Aliases: []string{"Alt Name"}, AliasSource: "tvmaze",
		Genres: []string{"Drama"}, GenreSource: "skyhook", Network: "Example Net", NetworkCountry: "US",
		Dates: []library.SeriesDateWrite{{Kind: "first_aired", Date: "2020-01-05", Source: "skyhook"}},
		Parts: []library.SeriesPartWrite{
			{MediaRef: "s01e01", SortKey: "s0001e0001", Title: "Pilot", RuntimeMs: &runtime, Date: "2020-01-05", AvailableAt: &available,
				AvailableConfidence: "high", AvailableBasis: "tvmaze airstamp", MonitoredIfNew: true,
				AltNumbers: []library.AltNumberWrite{{Scheme: "absolute", Provider: "tvdb", Ref: "1"}}},
			{MediaRef: "s00e01", SortKey: "s0000e0001", Title: "Recap", MonitoredIfNew: false},
		},
	}
}

func TestSeriesUpsertCreatesThenRefreshesWithoutTouchingMonitoring(t *testing.T) {
	opened, queries := newLibrary(t)
	series := library.NewSeries(opened)

	first, err := series.Upsert(t.Context(), seriesAt, sampleSeries())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.PartsCreated != 2 || first.PartsUpdated != 0 || len(first.PartIDs) != 2 {
		t.Fatalf("first = %+v", first)
	}

	pilot := first.PartIDs["s01e01"]
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetWorkPartMonitored(ctx, librarydb.SetWorkPartMonitoredParams{Monitored: 0, ID: pilot})
	})

	refreshed := sampleSeries()
	refreshed.Parts[0].Title = "Pilot (retitled)"
	refreshed.Aliases = []string{"Other Alias"}
	second, err := series.Upsert(t.Context(), seriesAt.Add(time.Hour), refreshed)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.WorkID != first.WorkID || second.PartsCreated != 0 || second.PartsUpdated != 2 || second.PartIDs["s01e01"] != pilot {
		t.Fatalf("second = %+v", second)
	}

	part, err := queries.GetWorkPart(t.Context(), pilot)
	if err != nil {
		t.Fatal(err)
	}
	if part.Title.String != "Pilot (retitled)" || part.Monitored != 0 || part.AvailableConfidence != "high" || part.AvailableAt.Int64 != 1_578_279_600 {
		t.Errorf("part = %+v: a refresh updates data and leaves the user's monitoring alone", part)
	}
	special, _ := queries.GetWorkPart(t.Context(), first.PartIDs["s00e01"])
	if special.Monitored != 0 {
		t.Errorf("a new special follows MonitoredIfNew=false, got %d", special.Monitored)
	}

	titles, _ := queries.ListWorkTitles(t.Context(), first.WorkID)
	if len(titles) != 1 || titles[0].Title != "Other Alias" {
		t.Errorf("aliases = %+v, want the refreshed list to replace the provider's old one", titles)
	}
	found, err := queries.FindPartByAltNumber(t.Context(), librarydb.FindPartByAltNumberParams{WorkID: first.WorkID, Scheme: "absolute", AltRef: "1"})
	if err != nil || found.ID != pilot {
		t.Errorf("absolute number 1 -> %+v, %v", found, err)
	}
	orgs, _ := queries.ListOrganisationsForWork(t.Context(), first.WorkID)
	if len(orgs) != 1 || orgs[0].Role != "network" {
		t.Errorf("organisations = %+v", orgs)
	}
}

func TestSeriesUpsertNeverStealsAnIDAndKeepsAManualTitle(t *testing.T) {
	opened, queries := newLibrary(t)
	series := library.NewSeries(opened)

	other := sampleSeries()
	other.IDs = []library.ExternalIDWrite{{Provider: "tvmaze", ExternalID: "42"}}
	other.Work.CanonicalTitle = "Other Show"
	otherResult, err := series.Upsert(t.Context(), seriesAt, other)
	if err != nil {
		t.Fatal(err)
	}

	mine := sampleSeries()
	mine.IDs = []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: "1234"}, {Provider: "tvmaze", ExternalID: "42"}}
	mine.Parts = nil
	result, err := series.Upsert(t.Context(), seriesAt, mine)
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkID != otherResult.WorkID {
		t.Fatalf("the tvdb id is new but the tvmaze id names the other work, so this is that work: %+v vs %+v", result, otherResult)
	}

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetWorkField(ctx, librarydb.SetWorkFieldParams{
			WorkID: result.WorkID, Field: "title", Provider: "__manual__", Value: sql.NullString{String: "My Name", Valid: true}, FetchedAt: 1,
		})
	})
	renamed := sampleSeries()
	renamed.Work.CanonicalTitle = "Provider Retitle"
	renamed.Parts = nil
	if _, err := series.Upsert(t.Context(), seriesAt, renamed); err != nil {
		t.Fatal(err)
	}
	work, _ := queries.GetWork(t.Context(), result.WorkID)
	if work.CanonicalTitle != "Example Show" {
		t.Errorf("title = %q: a manual title field stops a provider retitle", work.CanonicalTitle)
	}
}

func TestSeasonItemsCountOnlyTheirOwnMonitoredParts(t *testing.T) {
	opened, queries := newLibrary(t)
	write := sampleSeries()
	write.Parts = nil
	for _, ref := range []string{"s01:e01", "s01:e02", "s01:e03", "s02:e01", "s02:e02"} {
		write.Parts = append(write.Parts, library.SeriesPartWrite{MediaRef: ref, SortKey: ref, MonitoredIfNew: true})
	}
	result, err := library.NewSeries(opened).Upsert(t.Context(), seriesAt, write)
	if err != nil {
		t.Fatal(err)
	}

	desired := library.NewDesired(opened)
	var seasons []librarydb.DesiredItem
	for _, ref := range []string{"s01", "s02"} {
		item, err := desired.EnsureSeasonItem(t.Context(), library.SeasonRequest{
			WorkID: result.WorkID, MediaRef: ref, SortKey: ref, CategoryID: 1, ProfileID: 1, Monitored: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		seasons = append(seasons, item)
	}
	if seasons[0].RequiredCount.Int64 != 3 || seasons[1].RequiredCount.Int64 != 2 || seasons[0].State != "MISSING" {
		t.Fatalf("seasons = %+v / %+v", seasons[0], seasons[1])
	}

	if err := desired.SetPartMonitored(t.Context(), result.WorkID, result.PartIDs["s01:e02"], false); err != nil {
		t.Fatal(err)
	}
	first, _ := queries.GetDesiredItem(t.Context(), seasons[0].ID)
	second, _ := queries.GetDesiredItem(t.Context(), seasons[1].ID)
	if first.RequiredCount.Int64 != 2 || second.RequiredCount.Int64 != 2 {
		t.Errorf("unmonitoring one episode shrinks only its own season: %d and %d", first.RequiredCount.Int64, second.RequiredCount.Int64)
	}

	again, err := desired.EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 1, ProfileID: 9, Monitored: false})
	if err != nil || again.ID != seasons[0].ID || again.Monitored != 1 || again.ProfileID != 1 {
		t.Errorf("an existing season item is reused as it is: %+v, %v", again, err)
	}
}
