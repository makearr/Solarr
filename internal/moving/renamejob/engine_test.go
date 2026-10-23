package renamejob_test

import (
	"context"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/moverjob"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/migrations"
)

const rootPath = "/media/tv"

type listedFiles struct {
	files    []library.RenameFile
	askedFor []int64
}

func (l *listedFiles) RenameFilesOnRoot(_ context.Context, _, _, workID int64) ([]library.RenameFile, error) {
	l.askedFor = append(l.askedFor, workID)
	return l.files, nil
}

// namesByID is a media type that names each file as the test says, and says which works it was asked about.
type namesByID struct {
	names map[int64]string
	works []int64
}

func (n *namesByID) NameFiles(_ context.Context, _ settings.Category, workID int64, files []renamejob.File) ([]renamejob.Named, error) {
	n.works = append(n.works, workID)
	var named []renamejob.Named
	for _, file := range files {
		if name, ok := n.names[file.MediaObjectID]; ok {
			named = append(named, renamejob.Named{MediaObjectID: file.MediaObjectID, RelPath: name})
		}
	}
	return named, nil
}

type recordedJobs struct {
	created int
	items   []library.NewMoveItem
}

func (r *recordedJobs) Create(context.Context, library.NewMoveJob) (int64, error) {
	r.created++
	return 7, nil
}

func (r *recordedJobs) AddItems(_ context.Context, _ int64, items []library.NewMoveItem) error {
	r.items = append(r.items, items...)
	return nil
}

func onDisk(t *testing.T, fsys *fsx.Mem, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		abs := path.Join(rootPath, rel)
		if err := fsys.MkdirAll(path.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		writer, err := fsys.Create(abs)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func listed(id, workID int64, rel string) library.RenameFile {
	return library.RenameFile{
		FileOnRoot: library.FileOnRoot{MediaObjectID: id, RelPath: rel, SizeBytes: 10},
		Works:      []library.RenameWork{{WorkID: workID, MediaRefs: []string{"s01:e01"}}},
	}
}

func newQueue(t *testing.T) *jobs.Queue {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{Name: "jobs", Path: filepath.Join(directory, "jobs.db"), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry()
	registry.MustRegister(moverjob.Definition)
	return jobs.NewQueue(opened, fake, registry, jobs.NoJitter)
}

func engine(t *testing.T, fsys *fsx.Mem, files *listedFiles, recorded *recordedJobs) renamejob.Engine {
	return renamejob.Engine{
		Config: renamejob.Config{
			Roots:    roots{1: {ID: 1, Path: rootPath, Online: true, Writable: true, FSID: "fs"}},
			MoveJobs: recorded, Queue: newQueue(t), Clock: clock.NewFakeAtEpoch(),
		},
		Files: files, FS: fsys,
	}
}

// One series' episode, its subtitle, nfo and thumbnail beside it, and the season and series folders' own files:
// renaming the series folder moves all of them, and only the episode is a move of its own (user, 2026-10-23).
func TestEngine_MovesWhatIsBesideAFileAndItsFoldersFiles(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	onDisk(t, fsys,
		"Show/Season 1/Show - S01E01.mkv", "Show/Season 1/Show - S01E01.en.srt", "Show/Season 1/Show - S01E01.nfo",
		"Show/Season 1/Show - S01E01-thumb.jpg", "Show/Season 1/folder.jpg", "Show/tvshow.nfo", "Show/poster.jpg",
	)
	files := &listedFiles{files: []library.RenameFile{listed(1, 5, "Show/Season 1/Show - S01E01.mkv")}}
	namer := &namesByID{names: map[int64]string{1: "Show (2005)/Season 01/Show (2005) - S01E01 - Pilot.mkv"}}
	recorded := &recordedJobs{}

	plan, jobID, _, err := engine(t, fsys, files, recorded).Start(t.Context(), namer, settings.Category{ID: 3}, 1, 0, false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if jobID != 7 || len(plan.Moves) != 1 || len(recorded.items) != 1 {
		t.Fatalf("job %d, %d moves, %d items; want one item in job 7", jobID, len(plan.Moves), len(recorded.items))
	}
	want := []library.MoveCompanion{
		{SourceRelPath: "Show/Season 1/Show - S01E01-thumb.jpg", DestRelPath: "Show (2005)/Season 01/Show (2005) - S01E01 - Pilot-thumb.jpg"},
		{SourceRelPath: "Show/Season 1/Show - S01E01.en.srt", DestRelPath: "Show (2005)/Season 01/Show (2005) - S01E01 - Pilot.en.srt"},
		{SourceRelPath: "Show/Season 1/Show - S01E01.nfo", DestRelPath: "Show (2005)/Season 01/Show (2005) - S01E01 - Pilot.nfo"},
		{SourceRelPath: "Show/Season 1/folder.jpg", DestRelPath: "Show (2005)/Season 01/folder.jpg"},
		{SourceRelPath: "Show/poster.jpg", DestRelPath: "Show (2005)/poster.jpg"},
		{SourceRelPath: "Show/tvshow.nfo", DestRelPath: "Show (2005)/tvshow.nfo"},
	}
	got := recorded.items[0].Companions
	slices.SortFunc(got, func(a, b library.MoveCompanion) int { return compare(a.SourceRelPath, b.SourceRelPath) })
	if !reflect.DeepEqual(got, want) {
		t.Errorf("companions = %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(namer.works, []int64{5}) || !reflect.DeepEqual(files.askedFor, []int64{0}) {
		t.Errorf("namer asked about %v, files listed for %v; want work 5, the whole root", namer.works, files.askedFor)
	}
}

// A folder that keeps a media file the rename does not move keeps its own files too; another media file is never a
// companion, and a companion named like a longer name belongs to that file.
func TestEngine_LeavesAFoldersFilesWhereMediaStays(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	onDisk(t, fsys,
		"Album/Song.flac", "Album/Song.lrc", "Album/Song-remix.flac", "Album/Song-remix.lrc", "Album/cover.jpg",
	)
	files := &listedFiles{files: []library.RenameFile{listed(1, 5, "Album/Song.flac")}}
	namer := &namesByID{names: map[int64]string{1: "Artist/Album (2001)/01 - Song.flac"}}

	plan, _, _, err := engine(t, fsys, files, &recordedJobs{}).Start(t.Context(), namer, settings.Category{ID: 3}, 1, 0, true)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := []mover.Companion{{SourceRelPath: "Album/Song.lrc", DestRelPath: "Artist/Album (2001)/01 - Song.lrc"}}
	if len(plan.Moves) != 1 || !reflect.DeepEqual(plan.Moves[0].Companions, want) {
		t.Errorf("moves = %+v, want the song with its lyrics only", plan.Moves)
	}
}

// A file two works hold is named by neither, and the plan says why; nothing else changing means no job.
func TestEngine_RefusesAFileTwoWorksHold(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	shared := listed(1, 5, "Show/Season 1/Crossover.mkv")
	shared.Works = append(shared.Works, library.RenameWork{WorkID: 6, MediaRefs: []string{"s02:e03"}})
	files := &listedFiles{files: []library.RenameFile{shared}}
	namer := &namesByID{names: map[int64]string{1: "Elsewhere.mkv"}}
	recorded := &recordedJobs{}

	plan, jobID, _, err := engine(t, fsys, files, recorded).Start(t.Context(), namer, settings.Category{ID: 3}, 1, 0, false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := []mover.RefusedFile{{MediaObjectID: 1, Reason: "held by two works; left where it is"}}
	if jobID != 0 || recorded.created != 0 || len(namer.works) != 0 || !reflect.DeepEqual(plan.Refused, want) {
		t.Errorf("job %d, %d created, namer asked %v, refused %+v; want nothing started and the shared file named", jobID, recorded.created, namer.works, plan.Refused)
	}
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
