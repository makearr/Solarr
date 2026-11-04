package tv_test

import (
	"context"
	"database/sql"
	"path"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// A release labeled S01E02 whose picture is the S01E01 the series holds is that episode: it replaces the file only
// when the library's cutoff lets it, and is skipped as a duplicate otherwise, leaving S01E02 missing.
func TestImportReadsAMislabeledEpisodeByItsPicture(t *testing.T) {
	service, opened := newService(t, searchSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	fsys := fsx.NewMem(fake)
	for _, dir := range []string{"/downloads", "/library"} {
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel string) inspect.File {
		abs := path.Join("/downloads", rel)
		_ = fsys.MkdirAll(path.Dir(abs), 0o755)
		file, err := fsys.Create(abs)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte("video " + rel))
		_ = file.Close()
		return inspect.File{RelPath: rel, SizeBytes: int64(len("video " + rel)), Class: inspect.ClassMedia}
	}
	reader := library.NewSeriesReader(opened)
	free := int64(1 << 40)
	importer := func(below bool, same func(context.Context, int64, mediatool.VideoHash, int64) ([]int64, error)) tv.Importer {
		return tv.NewImporter(tv.ImporterConfig{
			FS: fsys, Roots: oneRoot{settings.RootFolder{ID: 9, Path: "/library", Online: true, Writable: true, FreeBytes: &free, TotalBytes: &free}},
			Affinity: memoryAffinity{}, Physical: library.NewPhysical(opened), Series: service.Series, Reader: reader,
			Identity: library.NewIdentity(opened), Desired: library.NewDesired(opened), Acquisitions: noAcquisition{},
			Resolver:   tv.Resolver{Service: service, Reader: reader, Clock: fake, DefaultProfileID: 1, Categories: categories{}},
			Categories: categories{}, Clock: fake,
			BelowCutoff: func(int64, library.EpisodeFile) bool { return below },
			Fingerprint: func(context.Context, string) (mediatool.VideoHash, int64, bool) {
				return mediatool.VideoHash{Mean: 1, Frames: []mediatool.FrameHash{{Slot: 0, Hash: 1}}}, 1_500_000, true
			},
			SameVideos: same,
		})
	}
	none := func(context.Context, int64, mediatool.VideoHash, int64) ([]int64, error) { return nil, nil }
	run := func(imp tv.Importer, id int64, rel string) batch.Result {
		unit := inspect.Unit{Directory: "Example.Show.S01.GRP", Media: []inspect.File{write("Example.Show.S01.GRP/" + rel)}}
		result, err := imp.ImportBatch(t.Context(), batch.Request{SessionID: id, CategoryID: 3, SourcePath: "/downloads", Units: []inspect.Unit{unit}, UnitIDs: []int64{id}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	first := run(importer(true, none), 1, "Example.Show.S01E01.1080p.WEB-DL.H.264-GRP.mkv")
	if first.Placements[0] == nil {
		t.Fatalf("first episode not placed: %+v", first.Decisions[0])
	}
	held := first.Placements[0].MediaObjectIDs[0]
	sameAsHeld := func(context.Context, int64, mediatool.VideoHash, int64) ([]int64, error) { return []int64{held}, nil }

	skipped := run(importer(false, sameAsHeld), 2, "Example.Show.S01E02.1080p.WEB-DL.H.264-GRP.mkv")
	if d := skipped.Decisions[0]; skipped.Placements[0] != nil || d.Decision != library.UnitSkip || d.Reason != batch.ReasonDuplicate {
		t.Fatalf("a copy of a file at its cutoff is skipped as a duplicate: %+v", d)
	}

	replacing := run(importer(true, sameAsHeld), 3, "Example.Show.S01E02.1080p.WEB-DL.H.264-GRP.mkv")
	if replacing.Placements[0] == nil {
		t.Fatalf("a copy of a file below its cutoff is placed beside it: %+v", replacing.Decisions[0])
	}
	if _, err := fsys.Stat("/library/Example Show/Season 1/Example Show - S01E01 - Episode [upgrade].mkv"); err != nil {
		t.Errorf("the copy was filed as the episode its picture shows: %v", err)
	}
	if _, err := fsys.Stat("/library/Example Show/Season 1/Example Show - S01E02 - Episode.mkv"); err == nil {
		t.Error("the copy must not be filed under the episode its release named")
	}
}

type seasonAcquisition struct{ itemID int64 }

func (s seasonAcquisition) Get(context.Context, int64) (library.Acquisition, error) {
	return library.Acquisition{DesiredItemID: &s.itemID}, nil
}

// A download grabbed for a season whose item the cutoff job has not yet marked upgradable still replaces an episode
// that is below the cutoff, the way an interactive Send from the Cutoff unmet list needs.
func TestImportReplacesABelowCutoffEpisodeOfACompleteSeason(t *testing.T) {
	service, opened := newService(t, searchSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	fsys := fsx.NewMem(fake)
	for _, dir := range []string{"/downloads", "/library"} {
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	reader := library.NewSeriesReader(opened)
	free := int64(1 << 40)
	importer := func(acquisitions tv.AcquisitionSource) tv.Importer {
		return tv.NewImporter(tv.ImporterConfig{
			FS: fsys, Roots: oneRoot{settings.RootFolder{ID: 9, Path: "/library", Online: true, Writable: true, FreeBytes: &free, TotalBytes: &free}},
			Affinity: memoryAffinity{}, Physical: library.NewPhysical(opened), Series: service.Series, Reader: reader,
			Identity: library.NewIdentity(opened), Desired: library.NewDesired(opened), Acquisitions: acquisitions,
			Resolver:   tv.Resolver{Service: service, Reader: reader, Clock: fake, DefaultProfileID: 1, Categories: categories{}},
			Categories: categories{}, Clock: fake,
			BelowCutoff: func(int64, library.EpisodeFile) bool { return true },
		})
	}
	run := func(imp tv.Importer, id int64, files ...string) batch.Result {
		var media []inspect.File
		for _, rel := range files {
			abs := path.Join("/downloads/Example.Show.S01.GRP", rel)
			_ = fsys.MkdirAll(path.Dir(abs), 0o755)
			file, err := fsys.Create(abs)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = file.Write([]byte("video " + rel))
			_ = file.Close()
			media = append(media, inspect.File{RelPath: "Example.Show.S01.GRP/" + rel, SizeBytes: int64(len("video " + rel)), Class: inspect.ClassMedia})
		}
		unit := inspect.Unit{Directory: "Example.Show.S01.GRP", Media: media}
		result, err := imp.ImportBatch(t.Context(), batch.Request{SessionID: id, CategoryID: 3, SourcePath: "/downloads", Units: []inspect.Unit{unit}, UnitIDs: []int64{id}, AcquisitionID: ptr(id)})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	first := run(importer(noAcquisition{}), 1, "Example.Show.S01E01.720p.WEB-DL.H.264-GRP.mkv", "Example.Show.S01E02.720p.WEB-DL.H.264-GRP.mkv")
	if first.Placements[0] == nil {
		t.Fatalf("season not placed: %+v", first.Decisions[0])
	}
	detail, err := reader.Get(t.Context(), 3, *first.Decisions[0].WorkID, fake.Now())
	if err != nil {
		t.Fatal(err)
	}
	item := seasonAcquisition{itemID: detail.Summary.Seasons[0].DesiredItemID}
	if err := opened.Write(t.Context(), store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE desired_item SET state = 'COMPLETE' WHERE id = ?", item.itemID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	upgrade := run(importer(item), 2, "Example.Show.S01E01.1080p.WEB-DL.H.264-GRP.mkv")
	if upgrade.Placements[0] == nil {
		t.Fatalf("a below-cutoff episode of a complete season is replaced: %+v", upgrade.Decisions[0])
	}
}

func ptr[T any](v T) *T { return &v }
