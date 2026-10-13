package tv_test

import (
	"context"
	"errors"
	"path"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

type oneRoot struct{ root settings.RootFolder }

func (o oneRoot) RootFolder(id int64) (settings.RootFolder, bool) { return o.root, id == o.root.ID }
func (o oneRoot) CategoryRoots() []settings.CategoryRoot {
	return []settings.CategoryRoot{{CategoryID: 3, RootFolderID: o.root.ID, AcceptsNew: true}}
}

type memoryAffinity map[string]int64

func (m memoryAffinity) PlacementAffinity(_ context.Context, _ int64, key string) (int64, bool, error) {
	id, ok := m[key]
	return id, ok, nil
}
func (m memoryAffinity) SetPlacementAffinity(_ context.Context, _ int64, key string, root, _ int64) error {
	m[key] = root
	return nil
}

type acquisitionFor struct{ itemID int64 }

func (a acquisitionFor) Get(context.Context, int64) (library.Acquisition, error) {
	return library.Acquisition{DesiredItemID: &a.itemID}, nil
}

type noAcquisition struct{}

func (noAcquisition) Get(context.Context, int64) (library.Acquisition, error) {
	return library.Acquisition{}, nil
}

func TestImportPlacesAFolderOfEpisodesUnderTheSeriesAndCoversThem(t *testing.T) {
	sky := fixtureSkyhook()
	service, opened := newService(t, searchSkyhook{series: sky}, fakeTVmaze{err: tvmaze.ErrNotFound})
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
		content := []byte("episode " + rel)
		_, _ = file.Write(content)
		_ = file.Close()
		return inspect.File{RelPath: rel, SizeBytes: int64(len(content)), Class: inspect.ClassMedia}
	}
	unit := inspect.Unit{Directory: "Example.Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP", Media: []inspect.File{
		write("Example.Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP/Example.Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv"),
		write("Example.Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP/Example.Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv"),
		write("Example.Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP/sample.mkv"),
	}}

	free := int64(1 << 40)
	reader := library.NewSeriesReader(opened)
	importer := tv.NewImporter(tv.ImporterConfig{
		FS: fsys, Roots: oneRoot{settings.RootFolder{ID: 9, Path: "/library", Online: true, Writable: true, FreeBytes: &free, TotalBytes: &free}},
		Affinity: memoryAffinity{}, Physical: library.NewPhysical(opened), Series: service.Series, Reader: reader,
		Identity: library.NewIdentity(opened), Desired: library.NewDesired(opened), Acquisitions: noAcquisition{},
		Resolver:   tv.Resolver{Service: service, Reader: reader, Clock: fake, DefaultProfileID: 1, Categories: categories{}},
		Categories: categories{}, Clock: fake,
	})

	result, err := importer.ImportBatch(t.Context(), batch.Request{
		SessionID: 1, CategoryID: 3, SourcePath: "/downloads", Units: []inspect.Unit{unit}, UnitIDs: []int64{77},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := result.Decisions[0]
	if decision.Decision != library.UnitCommit || decision.UnitID != 77 || decision.WorkID == nil {
		t.Fatalf("decision = %+v", decision)
	}
	placement := result.Placements[0]
	if placement == nil || len(placement.MediaObjectIDs) != 2 || len(placement.LibraryPathIDs) != 2 {
		t.Fatalf("placement = %+v: the sample is not an episode and is left alone", placement)
	}
	for _, want := range []string{
		"/library/Example Show/Season 1/Example Show - S01E01 - Episode.mkv",
		"/library/Example Show/Season 1/Example Show - S01E02 - Episode.mkv",
	} {
		if _, err := fsys.Stat(want); err != nil {
			t.Errorf("%s not placed: %v", want, err)
		}
	}
	list, _ := seriesIn(t.Context(), opened, 3)
	if len(list) != 1 || list[0].Counts.Present != 2 {
		t.Errorf("both episodes of the season are on disk: %+v", list)
	}

	again, err := importer.ImportBatch(t.Context(), batch.Request{
		SessionID: 1, CategoryID: 3, SourcePath: "/downloads", Units: []inspect.Unit{unit}, UnitIDs: []int64{78},
	})
	if err != nil || again.Placements[0] != nil || again.Decisions[0].Decision == library.UnitCommit {
		t.Errorf("a second import of the same episodes places nothing: %+v, %v", again.Decisions[0], err)
	}

	// A download grabbed for a season below its cutoff brings a second file per episode instead.
	items, err := library.NewDesired(opened).AlbumItemsForWork(t.Context(), *decision.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	var season1 int64
	for _, item := range items {
		if item.MediaRef == "s01" {
			season1 = item.ID
		}
	}
	if err := library.NewDesired(opened).SetAlbumState(t.Context(), season1, tv.ItemStateUpgradable, 2); err != nil {
		t.Fatal(err)
	}
	upgrading := tv.NewImporter(tv.ImporterConfig{
		FS: fsys, Roots: oneRoot{settings.RootFolder{ID: 9, Path: "/library", Online: true, Writable: true, FreeBytes: &free, TotalBytes: &free}},
		Affinity: memoryAffinity{}, Physical: library.NewPhysical(opened), Series: service.Series, Reader: reader,
		Identity: library.NewIdentity(opened), Desired: library.NewDesired(opened), Acquisitions: acquisitionFor{season1},
		Resolver:   tv.Resolver{Service: service, Reader: reader, Clock: fake, DefaultProfileID: 1, Categories: categories{}},
		Categories: categories{}, Clock: fake,
	})
	acquisitionID := int64(5)
	replaced, err := upgrading.ImportBatch(t.Context(), batch.Request{
		SessionID: 2, CategoryID: 3, SourcePath: "/downloads", Units: []inspect.Unit{unit}, UnitIDs: []int64{79}, AcquisitionID: &acquisitionID,
	})
	if err != nil || replaced.Placements[0] == nil || len(replaced.Placements[0].MediaObjectIDs) != 2 {
		t.Fatalf("an upgrade places its files beside the owned ones: %+v, %v", replaced.Decisions[0], err)
	}
	for _, want := range []string{
		"/library/Example Show/Season 1/Example Show - S01E01 - Episode [upgrade].mkv",
		"/library/Example Show/Season 1/Example Show - S01E01 - Episode.mkv",
	} {
		if _, err := fsys.Stat(want); err != nil {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestPreviewHoldsTheUnitForAPersonWithoutPlacingIt(t *testing.T) {
	service, opened := newService(t, searchSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	fsys := fsx.NewMem(fake)
	_ = fsys.MkdirAll("/downloads/Example Show", 0o755)
	reader := library.NewSeriesReader(opened)
	importer := tv.NewImporter(tv.ImporterConfig{
		FS: fsys, Roots: oneRoot{}, Affinity: memoryAffinity{}, Physical: library.NewPhysical(opened), Series: service.Series, Reader: reader,
		Identity: library.NewIdentity(opened), Desired: library.NewDesired(opened), Acquisitions: noAcquisition{},
		Resolver:   tv.Resolver{Service: service, Reader: reader, Clock: fake, Categories: categories{}},
		Categories: categories{}, Clock: fake,
	})
	unit := inspect.Unit{Directory: "Example Show", Media: []inspect.File{{RelPath: "Example Show/Example Show S01E01.mkv", SizeBytes: 5, Class: inspect.ClassMedia}}}
	result, err := importer.ImportBatch(t.Context(), batch.Request{CategoryID: 3, SourcePath: "/downloads", Units: []inspect.Unit{unit}, UnitIDs: []int64{1}, Preview: true})
	if err != nil {
		t.Fatal(err)
	}
	if d := result.Decisions[0]; d.Decision != library.UnitQueue || d.Reason != tv.ReasonPreview || result.Placements[0] != nil {
		t.Errorf("decision = %+v", d)
	}
}

// A unit nothing recognised (a folder named by hand) is placed as the series a person names, by Sonarr's Manual Import.
func TestCommitHeldPlacesAUnitAsTheSeriesNamedAndRefusesWhatAddsNothing(t *testing.T) {
	service, opened := newService(t, searchSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	fsys := fsx.NewMem(fake)
	for _, dir := range []string{"/downloads/keep", "/library"} {
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	file, err := fsys.Create("/downloads/keep/S01E02.mkv")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("episode"))
	_ = file.Close()
	unit := inspect.Unit{Directory: "keep", Media: []inspect.File{{RelPath: "keep/S01E02.mkv", SizeBytes: 7, Class: inspect.ClassMedia}}}

	free := int64(1 << 40)
	reader := library.NewSeriesReader(opened)
	importer := tv.NewImporter(tv.ImporterConfig{
		FS: fsys, Roots: oneRoot{settings.RootFolder{ID: 9, Path: "/library", Online: true, Writable: true, FreeBytes: &free, TotalBytes: &free}},
		Affinity: memoryAffinity{}, Physical: library.NewPhysical(opened), Series: service.Series, Reader: reader,
		Identity: library.NewIdentity(opened), Desired: library.NewDesired(opened), Acquisitions: noAcquisition{},
		Resolver:   tv.Resolver{Service: service, Reader: reader, Clock: fake, DefaultProfileID: 1, Categories: categories{}},
		Categories: categories{}, Clock: fake,
	})
	held := batch.HeldRequest{SessionID: 1, CategoryID: 3, SourcePath: "/downloads", Unit: unit}

	_, unresolved := importer.CommitHeld(t.Context(), held)
	named := held
	named.As = &batch.Named{Provider: tv.ProviderTVDB, ExternalID: "not-a-number"}
	_, notAnID := importer.CommitHeld(t.Context(), named)
	if !errors.Is(unresolved, batch.ErrUnresolved) || !errors.Is(notAnID, tv.ErrNotASeriesID) {
		t.Fatalf("unresolved = %v, bad id = %v; want batch.ErrUnresolved and tv.ErrNotASeriesID", unresolved, notAnID)
	}

	named.As = &batch.Named{Provider: tv.ProviderTVDB, ExternalID: "1234"}
	placement, err := importer.CommitHeld(t.Context(), named)
	if err != nil || placement.WorkID == 0 || len(placement.MediaObjectIDs) != 1 {
		t.Fatalf("placement = %+v, %v; want the one episode placed in the series added for it", placement, err)
	}
	if _, err := fsys.Stat("/library/Example Show/Season 1/Example Show - S01E02 - Episode.mkv"); err != nil {
		t.Errorf("the episode was not placed under the series: %v", err)
	}

	// The same unit again resolves to the series it now belongs to, and brings nothing new.
	held.WorkID = placement.WorkID
	if _, err := importer.CommitHeld(t.Context(), held); !errors.Is(err, batch.ErrNothingToPlace) {
		t.Errorf("a second commit: %v, want batch.ErrNothingToPlace", err)
	}
}
