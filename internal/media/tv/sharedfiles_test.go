package tv_test

import (
	"context"
	"log/slog"
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

// twoSkyhook serves each series by its TheTVDB id.
type twoSkyhook map[int64]skyhook.Series

func (s twoSkyhook) Series(_ context.Context, id int64) (skyhook.Series, error) { return s[id], nil }
func (s twoSkyhook) Search(context.Context, string) ([]skyhook.Series, error)   { return nil, nil }

type tvLibrary struct{ fileManagement settings.FileManagement }

func (l tvLibrary) Category(id int64) (settings.Category, bool) {
	return settings.Category{ID: id, MediaType: tv.MediaType, FileManagement: l.fileManagement}, true
}

// One broadcast listed under two series is held once: the second series gets a link to the first one's file in its
// own folder, recorded as a further path of the same file, and a second pass has nothing left to do.
func TestSharedFilesLinksTheFileIntoTheSecondSeries(t *testing.T) {
	cache := settingsCache(t)
	other := fixtureSkyhook()
	other.TVDBID, other.TVMazeID, other.Title = 5678, 43, "Other Show"
	service, opened := newService(t, twoSkyhook{1234: fixtureSkyhook(), 5678: other}, fakeTVmaze{err: tvmaze.ErrNotFound})
	profile := profileID(t, cache, "Any")
	first, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: profile, Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 5678}, CategoryID: 7, ProfileID: profile, Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	fsys := fsx.NewMem(fake)
	fsys.SetReflinkSupported(false)
	series := library.NewSeries(opened)
	attach := func(workID int64, rel, hash string) int64 {
		t.Helper()
		writeFile(t, fsys, "/library/"+rel, "video "+hash)
		registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 9, []library.PhysicalFile{{RelPath: rel, SizeBytes: 7, QuickHash: hash, FirstSeenAt: addNow}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := series.AttachFiles(t.Context(), addNow, workID, 9, []library.SeriesFileLink{{
			MediaObjectID: registered[0].MediaObjectID, Parts: []library.PartKey{{MediaRef: tv.MediaRef(1, 2)}},
		}}); err != nil {
			t.Fatal(err)
		}
		return registered[0].MediaObjectID
	}
	shared := attach(first.WorkID, "Example Show/Season 1/Example Show - S01E02 - Episode.mkv", "q1")
	writeFile(t, fsys, "/library/Other Show/Season 1/Other Show - S01E01 - Own.mkv", "own")
	own, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 9, []library.PhysicalFile{{RelPath: "Other Show/Season 1/Other Show - S01E01 - Own.mkv", SizeBytes: 3, QuickHash: "q2", FirstSeenAt: addNow}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := series.AttachFiles(t.Context(), addNow, second.WorkID, 9, []library.SeriesFileLink{{MediaObjectID: own[0].MediaObjectID, Parts: []library.PartKey{{MediaRef: tv.MediaRef(1, 1)}}}}); err != nil {
		t.Fatal(err)
	}
	partOf := func(workID int64) int64 {
		t.Helper()
		parts, _, err := series.ResolveParts(t.Context(), workID, []library.PartKey{{MediaRef: tv.MediaRef(1, 2)}})
		if err != nil || len(parts) != 1 {
			t.Fatalf("part s01:e02 of %d: %v, %v", workID, parts, err)
		}
		return parts[0].PartID
	}
	crossovers := library.NewCrossovers(opened)
	if err := crossovers.LinkSameContent(t.Context(), partOf(first.WorkID), partOf(second.WorkID)); err != nil {
		t.Fatal(err)
	}
	root := settings.RootFolder{ID: 9, Path: "/library", Online: true, Writable: true}
	placer := tv.SharedFiles{
		FS: fsys, Crossovers: crossovers, Reader: library.NewSeriesReader(opened), Links: library.NewObjectLinks(opened), Series: series,
		Categories: tvLibrary{settings.DefaultFileManagement}, Roots: oneRoot{root},
		RootsOf: func(int64) []settings.RootFolder { return []settings.RootFolder{root} }, Clock: fake, Logger: slog.New(slog.DiscardHandler),
	}

	if placed, err := placer.PlaceAll(t.Context()); err != nil || placed != 1 {
		t.Fatalf("placed %d, %v; want the shared episode in the second series", placed, err)
	}
	paths, err := library.NewPhysical(opened).PathsForObject(t.Context(), shared)
	if err != nil || len(paths) != 2 {
		t.Fatalf("paths of the shared file = %+v, %v; want two", paths, err)
	}
	placed := paths[1]
	if !strings.HasPrefix(placed.RelPath, "Other Show/Season 1/Other Show - S01E02") || placed.LinkKind != library.LinkHardlink || placed.IsPrimary != 0 {
		t.Errorf("placed path = %+v; want a hardlink in the second series' season folder", placed)
	}
	if body := readFile(t, fsys, "/library/"+placed.RelPath); body != "video q1" {
		t.Errorf("placed file holds %q, want the shared bytes", body)
	}
	detail, err := library.NewWorks(opened).Parts(t.Context(), second.WorkID, "", library.PartCursor{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range detail {
		if part.Part.MediaRef == tv.MediaRef(1, 2) && (part.File == nil || part.File.RelPath != placed.RelPath) {
			t.Errorf("the second series shows the shared episode at %+v, want its own %s", part.File, placed.RelPath)
		}
	}
	if again, err := placer.PlaceAll(t.Context()); err != nil || again != 0 {
		t.Errorf("second pass placed %d, %v; want 0", again, err)
	}

	writer := tv.ServerFiles{FS: fsys, Reader: library.NewSeriesReader(opened), Crossovers: crossovers, Roots: oneRoot{root}, Clock: fake}
	if _, err := writer.WriteSeries(t.Context(), 7, second.WorkID); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.Stat("/library/Example Show/Season 1/Example Show - S01E02 - Episode.nfo"); err == nil {
		t.Error("the second series wrote its episode nfo into the first series' folder")
	}
}
