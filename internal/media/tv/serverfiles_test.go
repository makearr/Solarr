package tv_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Jellyfin and Kodi read what is beside the files: the show's, the season's and the episode's nfo, and the pictures.
// What is there already stays, and a read-only root gets nothing.
func TestServerFilesWritesWhatIsMissingAndNothingElse(t *testing.T) {
	cache := settingsCache(t)
	series := fixtureSkyhook()
	series.Images = []skyhook.Image{{CoverType: "Poster", URL: "https://example.test/p.jpg"}, {CoverType: "Fanart", URL: "https://example.test/f.jpg"}}
	service, opened := newService(t, searchSkyhook{series: series}, fakeTVmaze{err: tvmaze.ErrNotFound})
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: profileID(t, cache, "Any"), Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	fsys := fsx.NewMem(fake)
	episode := "/library/Example Show/Season 1/Example Show - S01E02 - Episode.mkv"
	writeFile(t, fsys, episode, "video")
	writeFile(t, fsys, "/library/Example Show/fanart.png", "a person's own")
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 9, []library.PhysicalFile{{
		RelPath: strings.TrimPrefix(episode, "/library/"), SizeBytes: 5, QuickHash: "q", FirstSeenAt: addNow,
	}})
	if err != nil {
		t.Fatal(err)
	}
	links := []library.SeriesFileLink{{MediaObjectID: registered[0].MediaObjectID, Parts: []library.PartKey{{MediaRef: tv.MediaRef(1, 2)}}}}
	if _, err := library.NewSeries(opened).AttachFiles(t.Context(), addNow, added.WorkID, 9, links); err != nil {
		t.Fatal(err)
	}
	root := settings.RootFolder{ID: 9, Path: "/library", Online: true, Writable: true}
	var asked []string
	writer := tv.ServerFiles{
		FS: fsys, Reader: library.NewSeriesReader(opened), Roots: oneRoot{root}, Clock: fake,
		Picture: func(_ context.Context, address string) ([]byte, error) {
			asked = append(asked, address)
			return []byte("picture"), nil
		},
	}

	written, err := writer.WriteSeries(t.Context(), 7, added.WorkID)
	if err != nil || written != 4 {
		t.Fatalf("written %d, %v; want the episode, season and show nfo and the poster", written, err)
	}
	for file, wants := range map[string][]string{
		"/library/Example Show/tvshow.nfo":                                   {"<tvshow>", "<title>Example Show</title>", `<uniqueid type="tvdb" default="true">1234</uniqueid>`},
		"/library/Example Show/Season 1/season.nfo":                          {"<seasonnumber>1</seasonnumber>"},
		"/library/Example Show/Season 1/Example Show - S01E02 - Episode.nfo": {"<episodedetails>", "<season>1</season>", "<episode>2</episode>", `<uniqueid type="tvdb" default="true">3</uniqueid>`},
		"/library/Example Show/poster.jpg":                                   {"picture"},
	} {
		body := readFile(t, fsys, file)
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %s:\n%s", file, want, body)
			}
		}
	}
	if len(asked) != 1 || readFile(t, fsys, "/library/Example Show/fanart.png") != "a person's own" {
		t.Errorf("pictures asked for %v; the fanart a person put there must stay and not be fetched", asked)
	}
	if again, err := writer.WriteSeries(t.Context(), 7, added.WorkID); err != nil || again != 0 {
		t.Errorf("second pass wrote %d, %v; want 0", again, err)
	}
	readOnly := writer
	root.ReadOnly = true
	readOnly.Roots = oneRoot{root}
	if err := fsys.Remove("/library/Example Show/tvshow.nfo"); err != nil {
		t.Fatal(err)
	}
	if written, err := readOnly.WriteSeries(t.Context(), 7, added.WorkID); err != nil || written != 0 {
		t.Errorf("a read-only root got %d files, %v; want none", written, err)
	}
}

func writeFile(t *testing.T, fsys *fsx.Mem, name, body string) {
	t.Helper()
	if err := fsys.MkdirAll(name[:strings.LastIndex(name, "/")], 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte(body))
	_ = file.Close()
}

func readFile(t *testing.T, fsys *fsx.Mem, name string) string {
	t.Helper()
	file, err := fsys.Open(name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
