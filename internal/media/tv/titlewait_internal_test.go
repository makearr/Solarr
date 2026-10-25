package tv

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/renamejob"
)

// Sonarr's Episode Title Required: a TBA episode is held for 48 hours after it aired, from every release, a
// whole-season one, or none.
func TestTitleWaitHoldsATBAEpisodeForTwoDaysAfterItAired(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	aired := map[string]time.Time{"s01e01": now.Add(-time.Hour), "s01e02": now.Add(-49 * time.Hour), "s01e03": now.Add(-time.Hour)}
	file := func(ref, title string) fileEpisodes {
		return fileEpisodes{parts: []library.ResolvedPart{{MediaRef: ref, Title: title}}}
	}
	cases := []struct {
		name     string
		required settings.EpisodeTitleRequired
		files    []fileEpisodes
		waiting  []string
	}{
		{"TBA, aired an hour ago", settings.TitleRequiredAlways, []fileEpisodes{file("s01e01", "TBA")}, []string{"s01e01"}},
		{"an empty title counts as TBA", settings.TitleRequiredAlways, []fileEpisodes{file("s01e01", " ")}, []string{"s01e01"}},
		{"the stored default is always", "", []fileEpisodes{file("s01e01", "tba")}, []string{"s01e01"}},
		{"aired more than 48 hours ago", settings.TitleRequiredAlways, []fileEpisodes{file("s01e02", "TBA")}, nil},
		{"a title is known", settings.TitleRequiredAlways, []fileEpisodes{file("s01e01", "Pilot")}, nil},
		{"no air time known", settings.TitleRequiredAlways, []fileEpisodes{file("s01e09", "TBA")}, nil},
		{"never", settings.TitleRequiredNever, []fileEpisodes{file("s01e01", "TBA")}, nil},
		{"bulk only, one episode", settings.TitleRequiredBulkSeason, []fileEpisodes{file("s01e01", "TBA")}, nil},
		{"bulk only, a season of files", settings.TitleRequiredBulkSeason, []fileEpisodes{file("s01e03", "Known"), file("s01e01", "TBA")}, []string{"s01e01"}},
		{"bulk only, one file of a season release", settings.TitleRequiredBulkSeason,
			[]fileEpisodes{{info: parsetv.Info{FullSeason: true}, parts: []library.ResolvedPart{{MediaRef: "s01e01", Title: "TBA"}}}}, []string{"s01e01"}},
	}
	for _, c := range cases {
		err := titleWait(c.required, c.files, aired, now)
		var waiting *titleWaitError
		var got []string
		if errors.As(err, &waiting) {
			got = waiting.refs
		} else if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !slices.Equal(got, c.waiting) {
			t.Errorf("%s: waiting for %v, want %v", c.name, got, c.waiting)
		}
	}
}

type tbaFiles []library.RenameFile

func (f tbaFiles) RenameFilesOnRoot(context.Context, int64, int64, int64) ([]library.RenameFile, error) {
	return f, nil
}

// titledNamer gives each file its episode's name now that the title is known, except for "Still": still TBA.
type titledNamer struct{ asked *[]string }

func (n titledNamer) NameFiles(_ context.Context, _ settings.Category, _ int64, files []renamejob.File) ([]renamejob.Named, error) {
	var named []renamejob.Named
	for _, file := range files {
		*n.asked = append(*n.asked, file.RelPath)
		name := "Show (2008)/Season 01/Show - S01E01 - Pilot.mkv"
		if file.MediaObjectID == 3 {
			name = "Show (2008)/Season 01/Show - S01E03 - TBA.mkv"
		}
		named = append(named, renamejob.Named{MediaObjectID: file.MediaObjectID, RelPath: name})
	}
	return named, nil
}

// Only files named TBA are renamed, only once the new name has a title, only in name (never into another folder, which
// would split the series), and a file two series share is left alone.
func TestTitleRenameRenamesOnlyTheFilesNamedTBA(t *testing.T) {
	file := func(id int64, rel string, works ...int64) library.RenameFile {
		f := library.RenameFile{FileOnRoot: library.FileOnRoot{MediaObjectID: id, RelPath: rel}}
		for _, work := range works {
			f.Works = append(f.Works, library.RenameWork{WorkID: work})
		}
		return f
	}
	files := tbaFiles{
		file(1, "Show/Season 1/Show - S01E01 - TBA.mkv", 7),
		file(2, "Show/Season 1/Show - S01E02 - Kept As Named.mkv", 7),
		file(3, "Show/Season 1/Show - S01E03 - TBA.mkv", 7),
		file(4, "Other/Season 1/Other - S01E01 - Fine.mkv", 8),
		file(5, "Shared/Season 1/Shared - S01E01 - TBA.mkv", 9, 10),
		file(6, "Untitled/Season 1/Untitled - S01E01 - TBAR.mkv", 11),
	}
	var asked []string
	type call struct {
		work  int64
		names []renamejob.Named
	}
	var calls []call
	job := NewTitleRenameJob(TitleRenameConfig{
		Files: files, Namer: titledNamer{asked: &asked},
		Rename: func(ctx context.Context, namer renamejob.Namer, category settings.Category, _, workID int64) error {
			var all []renamejob.File
			for _, f := range files {
				if len(f.Works) == 1 && f.Works[0].WorkID == workID {
					all = append(all, renamejob.File{FileOnRoot: f.FileOnRoot, WorkID: workID})
				}
			}
			named, err := namer.NameFiles(ctx, category, workID, all)
			calls = append(calls, call{workID, named})
			return err
		},
		Libraries: func() []TitleRenameLibrary {
			return []TitleRenameLibrary{{Category: settings.Category{ID: 3}, Roots: []settings.RootFolder{{ID: 1}}}}
		},
	})
	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].work != 7 {
		t.Fatalf("renamed works %+v, want only series 7", calls)
	}
	if want := []renamejob.Named{{MediaObjectID: 1, RelPath: "Show/Season 1/Show - S01E01 - Pilot.mkv"}}; !slices.EqualFunc(calls[0].names, want, func(a, b renamejob.Named) bool {
		return a.MediaObjectID == b.MediaObjectID && a.RelPath == b.RelPath
	}) {
		t.Errorf("named %+v, want only the TBA file whose title arrived: %+v", calls[0].names, want)
	}
	if want := []string{"Show/Season 1/Show - S01E01 - TBA.mkv", "Show/Season 1/Show - S01E03 - TBA.mkv"}; !slices.Equal(asked, want) {
		t.Errorf("the namer was asked about %v, want only the TBA files %v", asked, want)
	}

	calls = nil
	off := false
	job.config.Libraries = func() []TitleRenameLibrary {
		return []TitleRenameLibrary{{Category: settings.Category{ID: 3, Naming: &settings.NamingProfile{RenameFiles: off}}, Roots: []settings.RootFolder{{ID: 1}}}}
	}
	if err := job.Run(t.Context(), jobs.Job{}); err != nil || len(calls) != 0 {
		t.Errorf("a library that keeps its files' names renames nothing: %+v, %v", calls, err)
	}
}
