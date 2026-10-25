package movie_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/movie"
)

// versionFile is one file of the film: its resolution and whether a download brought it.
type versionFile struct {
	resolution string
	downloaded bool
}

// cutoffFilm holds the example film with the files named, in a library that aims at 1080p and takes upgrades, and
// answers the job, the film's item and the files' media object ids in order.
func cutoffFilm(t *testing.T, files ...versionFile) (movie.CutoffJob, library.CutoffItem, []int64, *settings.Cache, map[int64]bool) {
	t.Helper()
	cache := settingsCache(t)
	category, err := cache.CreateCategory(t.Context(), movie.MediaType, "Movies", 1)
	if err != nil {
		t.Fatal(err)
	}
	profile := movieProfile(t, cache, "Movie HD 1080p")
	target, err := cache.CreateQualityTarget(t.Context(), settings.QualityTarget{Name: t.Name(), MediaType: movie.MediaType, Resolution: "1080p"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.SetCategoryQuality(t.Context(), category.ID, []int64{profile}, nil, &target.ID); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetCategoryQualityUpgrades(t.Context(), category.ID, true); err != nil {
		t.Fatal(err)
	}
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	added, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: category.ID, ProfileID: profile, Monitor: movie.MonitorMovieOnly})
	if err != nil {
		t.Fatal(err)
	}
	physical := library.NewPhysical(opened)
	var registered []library.PhysicalFile
	for index, file := range files {
		registered = append(registered, library.PhysicalFile{
			RelPath: fmt.Sprintf("The Example Film (1999)/version %d [%s].mkv", index, file.resolution), SizeBytes: 1,
			QuickHash: fmt.Sprintf("hash-%d", index), FirstSeenAt: time.Unix(int64(index+1), 0),
		})
	}
	objects, err := physical.RegisterBatch(t.Context(), 1, registered)
	if err != nil {
		t.Fatal(err)
	}
	var ids, brought []int64
	for index, object := range objects {
		ids = append(ids, object.MediaObjectID)
		if files[index].downloaded {
			brought = append(brought, object.MediaObjectID)
		}
		if err := physical.RecordMeasured(t.Context(), object.MediaObjectID, library.AttributeClaim{Resolution: files[index].resolution, VideoCodec: "h264", Container: "mkv"},
			library.MeasuredScalars{VideoBitrate: 4_000_000}, "", 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := library.NewSeries(opened).AttachWholeFiles(t.Context(), addNow.Add(-72*time.Hour), added.WorkID, 1, ids); err != nil {
		t.Fatal(err)
	}
	if len(brought) > 0 {
		acquisition, err := library.NewAcquisitions(opened).Create(t.Context(), library.NewAcquisition{
			DesiredItemID: &added.ItemID, CandidateGUID: "guid-1", CandidateTitle: "The.Example.Film.1999", DownloaderID: 1,
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
		units, err := sessions.AddUnits(t.Context(), sessionID, []library.NewUnit{{RelPath: ".", Shape: "movie", FileCount: int64(len(brought)), EvidenceJSON: "{}"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := sessions.RecordPlacement(t.Context(), units[0], library.Placement{WorkID: added.WorkID, MediaObjectIDs: brought}); err != nil {
			t.Fatal(err)
		}
	}
	desired := library.NewDesired(opened)
	recycled := map[int64]bool{}
	job := movie.CutoffJob{
		Desired: desired, Reader: library.NewSeriesReader(opened), Settings: cache,
		Recycle: func(ctx context.Context, object, _ int64, replaced bool) error {
			recycled[object] = replaced
			return desired.DetachAndRecompute(ctx, added.ItemID, added.WorkID, object, []string{"primary"})
		},
	}
	items, err := job.CutoffPage(t.Context(), added.WorkID, 0, 100)
	if err != nil || len(items) != 1 {
		t.Fatalf("items %+v, %v", items, err)
	}
	return job, items[0], ids, cache, recycled
}

func TestCutoff_ADownloadedBetterVersionReplacesTheOwnedOne(t *testing.T) {
	job, item, ids, cache, recycled := cutoffFilm(t, versionFile{"720p", false}, versionFile{"1080p", true})
	outcome, err := job.CheckCutoff(t.Context(), cache.Snapshot(), item)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Replaced != 1 || !recycled[ids[0]] || len(recycled) != 1 || outcome.State != movie.ItemStateComplete {
		t.Errorf("outcome %+v, recycled %v; want the owned 720p replaced and the film complete", outcome, recycled)
	}
}

func TestCutoff_TwoHeldEditionsAreNoContestForADownload(t *testing.T) {
	job, item, ids, cache, recycled := cutoffFilm(t, versionFile{"720p", false}, versionFile{"720p", false}, versionFile{"720p", true})
	outcome, err := job.CheckCutoff(t.Context(), cache.Snapshot(), item)
	if err != nil {
		t.Fatal(err)
	}
	if len(recycled) != 1 || !hasKey(recycled, ids[2]) || recycled[ids[2]] {
		t.Errorf("recycled %v; want only the download, the two held editions kept", recycled)
	}
	if outcome.State != movie.ItemStateUpgradable {
		t.Errorf("state = %q; no version reaches 1080p", outcome.State)
	}
}

func TestCutoff_OneVersionAtTheCutoffCompletesTheFilm(t *testing.T) {
	job, item, _, cache, recycled := cutoffFilm(t, versionFile{"720p", false}, versionFile{"1080p", false})
	outcome, err := job.CheckCutoff(t.Context(), cache.Snapshot(), item)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != movie.ItemStateComplete || len(recycled) != 0 {
		t.Errorf("outcome %+v, recycled %v; want complete, both held versions kept", outcome, recycled)
	}
}

func hasKey(set map[int64]bool, key int64) bool {
	_, ok := set[key]
	return ok
}
