package tv_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/cutoff"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/db/statistics/statisticstest"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
)

type upgradeFixture struct {
	cache    *settings.Cache
	opened   *store.Store
	category int64
	workID   int64
	season1  int64
	owned    int64
	brought  int64
	second   int64
}

// upgradeSeries is a series whose first episode has an owned file and, when brought is not empty, a downloaded one;
// the library wants 1080p.
func upgradeSeries(t *testing.T, owned, brought string) upgradeFixture {
	t.Helper()
	cache := settingsCache(t)
	category, err := cache.CreateCategory(t.Context(), "tv", "Series", 1)
	if err != nil {
		t.Fatal(err)
	}
	profile := profileID(t, cache, "Any")
	if err := cache.SetCategoryQuality(t.Context(), category.ID, []int64{profile}, nil, aimTarget(t, cache, "tv", settings.QualityTarget{Resolution: "1080p"})); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetCategoryQualityUpgrades(t.Context(), category.ID, true); err != nil {
		t.Fatal(err)
	}
	service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: category.ID, ProfileID: profile, Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	items, err := library.NewDesired(opened).AlbumItemsForWork(t.Context(), added.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	fixture := upgradeFixture{cache: cache, opened: opened, category: category.ID, workID: added.WorkID}
	for _, item := range items {
		if item.MediaRef == "s01" {
			fixture.season1 = item.ID
		}
	}
	files := []library.PhysicalFile{{RelPath: "Example Show/Season 01/e01 [" + owned + "].mkv", SizeBytes: 1, QuickHash: "owned", FirstSeenAt: time.Unix(1, 0)}}
	if brought != "" {
		files = append(files, library.PhysicalFile{RelPath: "Example Show/Season 01/e01 [" + brought + "] (new).mkv", SizeBytes: 1, QuickHash: "brought", FirstSeenAt: time.Unix(2, 0)})
	}
	files = append(files, library.PhysicalFile{RelPath: "Example Show/Season 01/e02 [1080p].mkv", SizeBytes: 1, QuickHash: "second", FirstSeenAt: time.Unix(1, 0)})
	physical := library.NewPhysical(opened)
	registered, err := physical.RegisterBatch(t.Context(), 1, files)
	if err != nil {
		t.Fatal(err)
	}
	fixture.owned, fixture.second = registered[0].MediaObjectID, registered[len(registered)-1].MediaObjectID
	// The second episode is already good, so the season is complete and only the first can be upgraded.
	resolutions, episodes := []string{owned}, []int{1}
	if brought != "" {
		fixture.brought = registered[1].MediaObjectID
		resolutions, episodes = append(resolutions, brought), append(episodes, 1)
	}
	resolutions, episodes = append(resolutions, "1080p"), append(episodes, 2)
	var links []library.SeriesFileLink
	for index, resolution := range resolutions {
		object := registered[index].MediaObjectID
		links = append(links, library.SeriesFileLink{MediaObjectID: object, Parts: []library.PartKey{{MediaRef: tv.MediaRef(1, episodes[index])}}})
		if err := physical.RecordMeasured(t.Context(), object, library.AttributeClaim{Resolution: resolution, VideoCodec: "h264", Container: "mkv"},
			library.MeasuredScalars{VideoBitrate: 4_000_000}, "", 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := library.NewSeries(opened).AttachFiles(t.Context(), addNow.Add(-72*time.Hour), added.WorkID, 1, links); err != nil {
		t.Fatal(err)
	}
	if brought != "" {
		acquisition, err := library.NewAcquisitions(opened).Create(t.Context(), library.NewAcquisition{
			DesiredItemID: &fixture.season1, CandidateGUID: "guid-1", CandidateTitle: "Example.Show.S01E01", DownloaderID: 1,
			DownloadClientRef: "nzo-1", Protocol: "usenet", State: "IMPORTED", CreatedAt: 1, UpdatedAt: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		sessions := library.NewImportSessions(opened)
		sessionID, err := sessions.Create(t.Context(), library.NewSession{SourcePath: "/downloads/x", Origin: "acquisition", AcquisitionID: &acquisition.ID, StartedAt: 1})
		if err != nil {
			t.Fatal(err)
		}
		units, err := sessions.AddUnits(t.Context(), sessionID, []library.NewUnit{{RelPath: ".", Shape: "season", FileCount: 1, EvidenceJSON: "{}"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := sessions.RecordPlacement(t.Context(), units[0], library.Placement{WorkID: added.WorkID, MediaObjectIDs: []int64{fixture.brought}}); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (f upgradeFixture) job(recycled map[int64]bool) tv.CutoffJob {
	desired := library.NewDesired(f.opened)
	return tv.CutoffJob{
		Desired: desired, Series: library.NewSeriesReader(f.opened), Settings: f.cache, Logger: slog.New(slog.DiscardHandler),
		Recycle: func(ctx context.Context, object, _ int64, replaced bool) error {
			recycled[object] = replaced
			return desired.DetachAndRecompute(ctx, f.season1, f.workID, object, []string{"primary"})
		},
	}
}

func (f upgradeFixture) run(t *testing.T, recycled map[int64]bool) string {
	t.Helper()
	if err := runCutoff(t, library.NewDesired(f.opened), f.cache, f.job(recycled), f.workID); err != nil {
		t.Fatal(err)
	}
	item, err := library.NewDesired(f.opened).Get(t.Context(), f.season1)
	if err != nil {
		t.Fatal(err)
	}
	return item.State
}

func TestCutoffJobMarksASeasonBelowTheTargetUpgradable(t *testing.T) {
	below := upgradeSeries(t, "720p", "")
	if state := below.run(t, map[int64]bool{}); state != tv.ItemStateUpgradable {
		t.Errorf("a 720p file against a 1080p target: state %s, want UPGRADABLE", state)
	}
	enough := upgradeSeries(t, "1080p", "")
	if state := enough.run(t, map[int64]bool{}); state != tv.ItemStateComplete {
		t.Errorf("a 1080p file: state %s, want COMPLETE", state)
	}
}

func TestCutoffJobKeepsTheBetterOfTwoFilesForAnEpisode(t *testing.T) {
	better := upgradeSeries(t, "720p", "1080p")
	recycled := map[int64]bool{}
	better.run(t, recycled)
	if len(recycled) != 1 || !recycled[better.owned] {
		t.Errorf("recycled %v, want only the owned 720p, as replaced", recycled)
	}

	same := upgradeSeries(t, "1080p", "1080p")
	recycled = map[int64]bool{}
	same.run(t, recycled)
	if replaced, found := recycled[same.brought]; len(recycled) != 1 || !found || replaced {
		t.Errorf("recycled %v, want only the downloaded copy, as no better (a tie keeps the owned file)", recycled)
	}
}

// A two-episode file beaten on one episode is still the other's only file, so it stays.
func TestCutoffJobKeepsAMultiEpisodeFileStillTheOnlyCopyOfAnother(t *testing.T) {
	fixture := upgradeSeries(t, "720p", "1080p")
	desired := library.NewDesired(fixture.opened)
	if err := desired.DetachAndRecompute(t.Context(), fixture.season1, fixture.workID, fixture.second, []string{"primary"}); err != nil {
		t.Fatal(err)
	}
	links := []library.SeriesFileLink{{MediaObjectID: fixture.owned, Parts: []library.PartKey{{MediaRef: tv.MediaRef(1, 2)}}}}
	if _, err := library.NewSeries(fixture.opened).AttachFiles(t.Context(), addNow.Add(-72*time.Hour), fixture.workID, 1, links); err != nil {
		t.Fatal(err)
	}

	recycled := map[int64]bool{}
	fixture.run(t, recycled)
	if len(recycled) != 0 {
		t.Errorf("recycled %v, want nothing: the owned file still holds episode 2 alone", recycled)
	}
}

func TestSearchOfAnUpgradableSeasonOnlyAcceptsAReleaseThatBeatsTheOwnedFile(t *testing.T) {
	fixture := upgradeSeries(t, "720p", "")
	fixture.run(t, map[int64]bool{})
	fake := &fakeIndexer{byFunction: map[string][]indexer.RawItem{"tvsearch": {
		{Title: "Example.Show.S01E01.720p.HDTV.x264-GRP", GUID: "same", Link: "https://example.test/same"},
		{Title: "Example.Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-GRP", GUID: "better", Link: "https://example.test/better"},
		{Title: "Example.Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-GRP", GUID: "other-episode", Link: "https://example.test/other"},
	}}}
	search := tv.Search{
		Desired: library.NewDesired(fixture.opened), Series: library.NewSeriesReader(fixture.opened), Candidates: statistics.NewCandidates(statisticstest.Open(t)),
		Settings: fixture.cache, Clock: clockAt(addNow),
	}
	result, err := search.SearchItem(t.Context(), fixture.season1, []judge.Conn{{ID: 1, Impl: "newznab", Client: fake}}, addNow)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]string{}
	for _, detail := range result.Decisions {
		outcomes[detail.ReleaseCandidate.GUID] = detail.Decision.Outcome
	}
	if len(outcomes) != 2 || outcomes["better"] != "ACCEPT" || outcomes["same"] != "REJECT" {
		t.Errorf("outcomes = %v; want the 1080p accepted, the 720p rejected and the episode that is not below the cutoff not searched", outcomes)
	}
}

// A season whose other episodes have yet to air is complete, so the cutoff check finds the episode it has below the
// cutoff and the search upgrades it without waiting for the season to end.
func TestSearchOfASeasonStillAiringUpgradesTheEpisodesItHas(t *testing.T) {
	fixture := upgradeSeries(t, "1080p", "")
	items, _ := library.NewDesired(fixture.opened).AlbumItemsForWork(t.Context(), fixture.workID)
	var season2 int64
	for _, item := range items {
		if item.MediaRef == "s02" {
			season2 = item.ID
		}
	}
	physical := library.NewPhysical(fixture.opened)
	registered, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{{RelPath: "Example Show/Season 02/e01.mkv", SizeBytes: 1, QuickHash: "s2", FirstSeenAt: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	object := registered[0].MediaObjectID
	if err := physical.RecordMeasured(t.Context(), object, library.AttributeClaim{Resolution: "720p", VideoCodec: "h264", Container: "mkv"}, library.MeasuredScalars{VideoBitrate: 3_000_000}, "", 1); err != nil {
		t.Fatal(err)
	}
	links := []library.SeriesFileLink{{MediaObjectID: object, Parts: []library.PartKey{{MediaRef: tv.MediaRef(2, 1)}}}}
	if _, err := library.NewSeries(fixture.opened).AttachFiles(t.Context(), addNow.Add(-72*time.Hour), fixture.workID, 1, links); err != nil {
		t.Fatal(err)
	}
	if airing, err := library.NewDesired(fixture.opened).Get(t.Context(), season2); err != nil || airing.State != "COMPLETE" || airing.RequiredCount.Int64 != 1 {
		t.Fatalf("season 2 = %+v, %v; want COMPLETE with the one aired episode required", airing, err)
	}
	fixture.run(t, map[int64]bool{})
	fake := &fakeIndexer{byFunction: map[string][]indexer.RawItem{"tvsearch": {
		{Title: "Example.Show.S02E01.720p.HDTV.x264-GRP", GUID: "same", Link: "https://example.test/same"},
		{Title: "Example.Show.S02E01.1080p.WEB-DL.DDP5.1.H.264-GRP", GUID: "better", Link: "https://example.test/better"},
	}}}
	search := tv.Search{
		Desired: library.NewDesired(fixture.opened), Series: library.NewSeriesReader(fixture.opened), Candidates: statistics.NewCandidates(statisticstest.Open(t)),
		Settings: fixture.cache, Clock: clockAt(addNow),
	}
	result, err := search.SearchItem(t.Context(), season2, []judge.Conn{{ID: 1, Impl: "newznab", Client: fake}}, addNow)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]string{}
	for _, detail := range result.Decisions {
		outcomes[detail.ReleaseCandidate.GUID] = detail.Decision.Outcome
	}
	if outcomes["better"] != "ACCEPT" || outcomes["same"] != "REJECT" {
		t.Errorf("outcomes = %v; want the 1080p accepted over the owned 720p and the 720p rejected", outcomes)
	}
}

// aimTarget stores a target under a fresh name and answers its id, the way a library picks one.
func aimTarget(t *testing.T, cache *settings.Cache, mediaType string, target settings.QualityTarget) *int64 {
	t.Helper()
	target.Name, target.MediaType = fmt.Sprintf("%s %d", t.Name(), time.Now().UnixNano()), mediaType
	created, err := cache.CreateQualityTarget(t.Context(), target)
	if err != nil {
		t.Fatalf("CreateQualityTarget: %v", err)
	}
	return &created.ID
}

// runCutoff runs the shared cutoff job with check as its only media type, for every item or the one work named.
func runCutoff(t *testing.T, desired library.Desired, cache *settings.Cache, check cutoff.Check, workID int64) error {
	t.Helper()
	payload, err := json.Marshal(cutoff.Payload{WorkID: workID})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	job := cutoff.NewJob(cutoff.Config{
		Desired: desired, Settings: cache, Logger: slog.New(slog.DiscardHandler),
		Sources: func(int64) []cutoff.Source { return []cutoff.Source{{Name: "test", Check: check}} },
	})
	return job.Run(t.Context(), jobs.Job{Type: cutoff.JobType, Payload: payload})
}

func TestCutoffJobCountsTheFilesBelowTheTarget(t *testing.T) {
	for _, c := range []struct {
		resolution string
		below      int64
	}{{"720p", 1}, {"1080p", 0}} {
		fixture := upgradeSeries(t, c.resolution, "")
		job := fixture.job(map[int64]bool{})
		items, err := job.CutoffPage(t.Context(), fixture.workID, 0, 100)
		if err != nil || len(items) == 0 {
			t.Fatalf("items %+v, %v", items, err)
		}
		outcome, err := job.CheckCutoff(t.Context(), fixture.cache.Snapshot(), items[0])
		if err != nil {
			t.Fatal(err)
		}
		if outcome.Judged != 2 || outcome.BelowCutoff != c.below {
			t.Errorf("%s: judged %d, below %d; want 2 and %d", c.resolution, outcome.Judged, outcome.BelowCutoff, c.below)
		}
	}
}
