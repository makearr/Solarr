package library_test

import (
	"context"
	"database/sql"
	"errors"
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

func TestSeriesReaderListsAndDetailsOnlyWhatTheCategoryHolds(t *testing.T) {
	opened, _ := newLibrary(t)
	write := sampleSeries()
	write.Parts = nil
	for _, ref := range []string{"s00:e01", "s01:e01", "s01:e02"} {
		date := "2020-01-05"
		write.Parts = append(write.Parts, library.SeriesPartWrite{MediaRef: ref, SortKey: ref, Date: date, MonitoredIfNew: ref != "s00:e01"})
	}
	result, err := library.NewSeries(opened).Upsert(t.Context(), seriesAt, write)
	if err != nil {
		t.Fatal(err)
	}
	desired := library.NewDesired(opened)
	for _, ref := range []string{"s00", "s01"} {
		if _, err := desired.EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: ref, CategoryID: 5, ProfileID: 2, Monitored: ref == "s01"}); err != nil {
			t.Fatal(err)
		}
	}

	reader := library.NewSeriesReader(opened)
	got, err := reader.Get(t.Context(), 5, result.WorkID, seriesAt)
	if err != nil {
		t.Fatal(err)
	}
	row := got.Summary
	if row.Counts.Total != 2 || row.Counts.Monitored != 2 || row.Counts.AiredMissing != 2 || !row.Monitored || row.ProfileID != 2 ||
		row.Fields["network"] != "" || len(row.Seasons) != 2 || row.Genres[0] != "Drama" {
		t.Errorf("row = %+v", row)
	}
	if _, err := reader.Get(t.Context(), 6, result.WorkID, seriesAt); !errors.Is(err, library.ErrSeriesNotFound) {
		t.Errorf("Get in the wrong category: %v", err)
	}
	detail, err := reader.Get(t.Context(), 5, result.WorkID, seriesAt)
	if err != nil || len(detail.Episodes) != 3 || detail.IDs["tvdb"] != "1234" || detail.Episodes[1].Season != 1 || detail.Episodes[1].Number != 1 {
		t.Errorf("detail = %+v, %v", detail, err)
	}
}

func TestRecomputeWorkItemsFollowsRemovedFiles(t *testing.T) {
	opened, queries := newLibrary(t)
	write := sampleSeries()
	write.Parts = []library.SeriesPartWrite{{MediaRef: "s01:e01", SortKey: "a", MonitoredIfNew: true}, {MediaRef: "s01:e02", SortKey: "b", MonitoredIfNew: true}}
	series := library.NewSeries(opened)
	result, err := series.Upsert(t.Context(), seriesAt, write)
	if err != nil {
		t.Fatal(err)
	}
	desired := library.NewDesired(opened)
	item, err := desired.EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 1, ProfileID: 1, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	physical := library.NewPhysical(opened)
	registered, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{{RelPath: "a.mkv", SizeBytes: 1, QuickHash: "h", FirstSeenAt: seriesAt}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := series.AttachFiles(t.Context(), seriesAt, result.WorkID, 1, []library.SeriesFileLink{{MediaObjectID: registered[0].MediaObjectID, Parts: []library.PartKey{{MediaRef: "s01:e01"}}}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := queries.GetDesiredItem(t.Context(), item.ID); got.PresentCount != 1 || got.State != "PARTIAL" {
		t.Fatalf("after attach = %+v", got)
	}
	if err := physical.DeleteObject(t.Context(), registered[0].MediaObjectID); err != nil {
		t.Fatal(err)
	}
	if err := physical.RecomputeWorkItems(t.Context(), result.WorkID); err != nil {
		t.Fatal(err)
	}
	if got, _ := queries.GetDesiredItem(t.Context(), item.ID); got.PresentCount != 0 || got.State != "MISSING" {
		t.Errorf("after removing the file = %+v: the counts follow the files", got)
	}
}

func TestWantedListsAiredMonitoredEpisodesWithoutAFileNewestFirst(t *testing.T) {
	opened, _ := newLibrary(t)
	write := sampleSeries()
	write.Parts = []library.SeriesPartWrite{
		{MediaRef: "s01:e01", SortKey: "a", Date: "2020-01-05", MonitoredIfNew: true},
		{MediaRef: "s01:e02", SortKey: "b", Date: "2020-01-12", MonitoredIfNew: true},
		{MediaRef: "s01:e03", SortKey: "c", Date: "2020-01-19", MonitoredIfNew: false},
		{MediaRef: "s01:e04", SortKey: "d", Date: "2999-01-01", MonitoredIfNew: true},
	}
	series := library.NewSeries(opened)
	result, err := series.Upsert(t.Context(), seriesAt, write)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewDesired(opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 4, ProfileID: 1, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	physical := library.NewPhysical(opened)
	registered, _ := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{{RelPath: "a.mkv", SizeBytes: 1, QuickHash: "h", FirstSeenAt: seriesAt}})
	if _, err := series.AttachFiles(t.Context(), seriesAt, result.WorkID, 1, []library.SeriesFileLink{{MediaObjectID: registered[0].MediaObjectID, Parts: []library.PartKey{{MediaRef: "s01:e01"}}}}); err != nil {
		t.Fatal(err)
	}

	works := library.NewWorks(opened)
	got, err := works.WantedParts(t.Context(), 4, library.WantedPartsCursor{}, 10, seriesAt)
	if err != nil || len(got) != 1 || got[0].MediaRef != "s01:e02" || got[0].WorkTitle != "Example Show" {
		t.Fatalf("wanted = %+v, %v: only e02 is monitored, aired and missing", got, err)
	}
	if other, _ := works.WantedParts(t.Context(), 5, library.WantedPartsCursor{}, 10, seriesAt); len(other) != 0 {
		t.Errorf("another library sees %+v", other)
	}
}

func TestCalendarListsMonitoredEpisodesAndCountsTheirDates(t *testing.T) {
	opened, _ := newLibrary(t)
	write := sampleSeries()
	write.Parts = []library.SeriesPartWrite{
		{MediaRef: "s01:e01", SortKey: "a", Title: "Pilot", Date: "2020-01-05", MonitoredIfNew: true},
		{MediaRef: "s01:e02", SortKey: "b", Title: "Second", Date: "2020-01-05", MonitoredIfNew: true},
		{MediaRef: "s01:e03", SortKey: "c", Title: "Skipped", Date: "2020-01-06", MonitoredIfNew: false},
	}
	result, err := library.NewSeries(opened).Upsert(t.Context(), seriesAt, write)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewDesired(opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 4, ProfileID: 1, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	calendar := library.NewCalendar(opened)
	entries, err := calendar.Entries(t.Context(), "2020-01-01", "2020-01-31", []int64{4}, 50)
	if err != nil || len(entries) != 2 || entries[0].PartID == 0 || entries[0].CategoryID != 4 || entries[0].Artist != "S01E01 Pilot" || entries[0].Subtype != "Episode" {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	if other, _ := calendar.Entries(t.Context(), "2020-01-01", "2020-01-31", []int64{9}, 50); len(other) != 0 {
		t.Errorf("another library sees %+v", other)
	}
	dates, err := calendar.Dates(t.Context(), "2020-01-01", "2020-01-31", []int64{4})
	if err != nil || len(dates) != 1 || dates[0].Date != "2020-01-05" || dates[0].Releases != 2 {
		t.Errorf("dates = %+v, %v", dates, err)
	}
}

// An episode dated today is not owed until its wait has passed, the same rule the season counts follow.
func TestWantedWaitsOutTheGraceOfAnEpisodeAiredToday(t *testing.T) {
	opened, _ := newLibrary(t)
	write := sampleSeries()
	write.Parts = []library.SeriesPartWrite{{MediaRef: "s01:e01", SortKey: "a", Date: "2026-10-16", MonitoredIfNew: true}}
	result, err := library.NewSeries(opened).Upsert(t.Context(), seriesAt, write)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewDesired(opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 4, ProfileID: 1, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	works := library.NewWorks(opened)
	midnight := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	if got, _ := works.WantedParts(t.Context(), 4, library.WantedPartsCursor{}, 10, midnight.Add(time.Hour)); len(got) != 0 {
		t.Errorf("an hour after a date-only airing: wanted %+v, want none until the grace has passed", got)
	}
	if got, _ := works.WantedParts(t.Context(), 4, library.WantedPartsCursor{}, 10, midnight.Add(48*time.Hour)); len(got) != 1 {
		t.Errorf("two days later: wanted %+v, want the episode", got)
	}
}
