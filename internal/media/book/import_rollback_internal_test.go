package book

import (
	"context"
	"path"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/placer"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

type rollbackRoot struct{ root settings.RootFolder }

func (o rollbackRoot) RootFolder(id int64) (settings.RootFolder, bool) {
	return o.root, id == o.root.ID
}
func (o rollbackRoot) CategoryRoots() []settings.CategoryRoot {
	return []settings.CategoryRoot{{CategoryID: 3, RootFolderID: o.root.ID, AcceptsNew: true}}
}

type rollbackAffinity map[string]int64

func (m rollbackAffinity) PlacementAffinity(_ context.Context, _ int64, key string) (int64, bool, error) {
	id, ok := m[key]
	return id, ok, nil
}

func (m rollbackAffinity) SetPlacementAffinity(_ context.Context, _ int64, key string, root, _ int64) error {
	m[key] = root
	return nil
}

type rollbackCategories struct{}

func (rollbackCategories) Category(int64) (settings.Category, bool) { return settings.Category{}, true }

// filesUnder lists every file below a directory of the in-memory file system.
func filesUnder(t *testing.T, fsys fsx.FS, directory string) []string {
	t.Helper()
	entries, err := fsys.ReadDir(directory)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir {
			files = append(files, filesUnder(t, fsys, path.Join(directory, entry.Name))...)
			continue
		}
		files = append(files, path.Join(directory, entry.Name))
	}
	return files
}

// A store error after the book's files were placed takes back the files and the rows registered for them, for an ebook and for an audiobook's chapters.
func TestPlaceTakesBackFilesAndRowsWhenALaterStepFails(t *testing.T) {
	for _, format := range []Format{Ebook, Audiobook} {
		for _, step := range []struct{ name, table string }{
			{"registering the files", "library_path"},
			{"attaching the files to the book", "work_file"},
		} {
			t.Run(format.MediaType+" "+step.name, func(t *testing.T) {
				opened := openFollowLibrary(t)
				fake := clock.NewFakeAtEpoch()
				fsys := fsx.NewMem(fake)
				for _, directory := range []string{"/downloads", "/library"} {
					if err := fsys.MkdirAll(directory, 0o755); err != nil {
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
					content := []byte("book " + rel)
					_, _ = file.Write(content)
					_ = file.Close()
					return inspect.File{RelPath: rel, SizeBytes: int64(len(content)), Class: inspect.ClassMedia}
				}
				work, err := library.NewSeries(opened).Upsert(t.Context(), fake.Now(), library.SeriesWrite{
					Work: library.WorkIdentity{MediaType: format.MediaType, CanonicalTitle: "The Lantern Keeper", SortTitle: "lantern keeper"},
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := library.NewDesired(opened).EnsureAlbumItem(t.Context(), library.AlbumRequest{
					WorkID: work.WorkID, CategoryID: 3, ProfileID: 1, Monitored: true, RequiredWithoutParts: 1,
				}); err != nil {
					t.Fatal(err)
				}
				var unit inspect.Unit
				if format.ByFolder {
					unit = inspect.Unit{Directory: "The Lantern Keeper", Media: []inspect.File{write("The Lantern Keeper/01.m4b"), write("The Lantern Keeper/02.m4b")}}
				} else {
					unit = inspect.Unit{Directory: "The Lantern Keeper", Media: []inspect.File{write("The Lantern Keeper/The Lantern Keeper.epub")}}
				}
				free, total := int64(1<<40), int64(1<<40)
				identity := library.NewIdentity(opened)
				placing := placing{config: ImporterConfig{
					ImportDeps: works.ImportDeps{
						FS: fsys, Physical: library.NewPhysical(opened), Series: library.NewSeries(opened), Reader: library.NewSeriesReader(opened),
						Identity: identity, Desired: library.NewDesired(opened), Categories: rollbackCategories{}, Clock: fake,
						Placer: placer.New(placer.Config{
							FS: fsys, Roots: rollbackRoot{settings.RootFolder{ID: 9, Path: "/library", Online: true, Writable: true, FreeBytes: &free, TotalBytes: &total}},
							Affinity: rollbackAffinity{}, Identity: identity, Clock: fake,
						}),
					},
					Resolver: Resolver{Service: Service{Format: format}},
				}}
				if _, err := opened.WriteDB().ExecContext(t.Context(),
					`CREATE TRIGGER refuse BEFORE INSERT ON `+step.table+` BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
					t.Fatal(err)
				}

				placement, err := placing.Place(t.Context(), batch.Request{SessionID: 1, CategoryID: 3, SourcePath: "/downloads"}, unit, work.WorkID, false)
				if err == nil || placement != nil {
					t.Fatalf("Place = %+v, %v; want the injected failure", placement, err)
				}
				for _, table := range []string{"library_path", "media_object"} {
					var rows int
					if err := opened.Read().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&rows); err != nil || rows != 0 {
						t.Errorf("%s rows = %d (%v), want 0 after the take-back", table, rows, err)
					}
				}
				if left := filesUnder(t, fsys, "/library"); len(left) != 0 {
					t.Errorf("library holds %v, want no placed file", left)
				}
			})
		}
	}
}
