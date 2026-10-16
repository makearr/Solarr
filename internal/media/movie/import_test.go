package movie_test

import (
	"context"
	"path"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/placer"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
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

type noAcquisition struct{}

func (noAcquisition) Get(context.Context, int64) (library.Acquisition, error) {
	return library.Acquisition{}, nil
}

func newImporter(t *testing.T) (movie.Importer, *fsx.Mem, func(rel string) inspect.File) {
	t.Helper()
	service, opened := newService(t, byTitle{movies: []tmdb.Movie{simpleMovie(603, "The Tonhut", "1999-03-30")}}, nil)
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
		content := []byte("film " + rel)
		_, _ = file.Write(content)
		_ = file.Close()
		return inspect.File{RelPath: rel, SizeBytes: int64(len(content)), Class: inspect.ClassMedia}
	}
	free := int64(1 << 40)
	identity := library.NewIdentity(opened)
	reader := library.NewSeriesReader(opened)
	return movie.NewImporter(movie.ImporterConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Series: service.Series, Reader: reader, Identity: identity,
		Desired: library.NewDesired(opened), Acquisitions: noAcquisition{},
		Placer: placer.New(placer.Config{
			FS: fsys, Roots: oneRoot{settings.RootFolder{ID: 9, Path: "/library", Online: true, Writable: true, FreeBytes: &free, TotalBytes: &free}},
			Affinity: memoryAffinity{}, Identity: identity, Clock: fake,
		}),
		Resolver:   movie.Resolver{Service: service, Reader: reader, DefaultProfileID: 1, Categories: categories{}},
		Categories: categories{}, Clock: fake,
	}), fsys, write
}

func TestImportPlacesTheBiggestFileUnderTheMovieFolderAndLeavesTheSample(t *testing.T) {
	importer, fsys, write := newImporter(t)
	unit := inspect.Unit{Directory: "The.Tonhut.1999.1080p.BluRay.x264-GRP", Media: []inspect.File{
		write("The.Tonhut.1999.1080p.BluRay.x264-GRP/The.Tonhut.1999.1080p.BluRay.x264-GRP.mkv"),
		write("The.Tonhut.1999.1080p.BluRay.x264-GRP/sample.mkv"),
	}}
	result, err := importer.ImportBatch(t.Context(), batch.Request{
		SessionID: 1, CategoryID: 3, SourcePath: "/downloads", Units: []inspect.Unit{unit}, UnitIDs: []int64{5},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := result.Decisions[0]
	if decision.Decision != library.UnitCommit || decision.WorkID == nil {
		t.Fatalf("decision = %+v, want a commit to The Tonhut", decision)
	}
	if placement := result.Placements[0]; placement == nil || len(placement.MediaObjectIDs) != 1 {
		t.Fatalf("placement = %+v, want the one film file", result.Placements[0])
	}
	if _, err := fsys.Stat("/library/The Tonhut (1999)/The Tonhut (1999).mkv"); err != nil {
		t.Errorf("film not placed under its movie folder: %v", err)
	}

	again, err := importer.ImportBatch(t.Context(), batch.Request{
		SessionID: 1, CategoryID: 3, SourcePath: "/downloads", Units: []inspect.Unit{unit}, UnitIDs: []int64{6},
	})
	if err != nil || again.Placements[0] != nil || again.Decisions[0].Decision == library.UnitCommit {
		t.Errorf("a second import of a film already held places nothing: %+v, %v", again.Decisions[0], err)
	}
}

func TestImportQueuesAFolderItCannotNameAFilm(t *testing.T) {
	importer, _, write := newImporter(t)
	unit := inspect.Unit{Directory: "holiday", Media: []inspect.File{write("holiday/clip.mkv")}}
	result, err := importer.ImportBatch(t.Context(), batch.Request{
		SessionID: 1, CategoryID: 3, SourcePath: "/downloads", Units: []inspect.Unit{unit}, UnitIDs: []int64{7},
	})
	if err != nil || result.Decisions[0].Decision != library.UnitQueue || result.Placements[0] != nil {
		t.Errorf("decision = %+v, %v; want the unit held for a person", result.Decisions[0], err)
	}
}
