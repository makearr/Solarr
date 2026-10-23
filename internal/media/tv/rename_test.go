package tv_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type oneSeries library.SeriesDetailRow

func (s oneSeries) Get(context.Context, int64, int64, time.Time) (library.SeriesDetailRow, error) {
	return library.SeriesDetailRow(s), nil
}

// TV names a file by every episode it holds, takes what its old name says of the release, and what the name no longer
// says from the probe; a file of no listed episode keeps its name.
func TestNameFiles_NamesEachEpisodeFile(t *testing.T) {
	probed := &library.EpisodeFile{MediaObjectID: 2, Resolution: "1080p", VideoCodec: "hevc"}
	series := oneSeries{
		Summary: library.SeriesSummary{Title: "Vemari Coast", Year: 2005},
		Episodes: []library.SeriesEpisode{
			{PartID: 1, Season: 1, Number: 1, Title: "Arrival"},
			{PartID: 2, Season: 1, Number: 2, Title: "Departure"},
			{PartID: 3, Season: 1, Number: 3, Title: "Tide", File: probed},
		},
	}
	cfg := tv.RenameConfig{Series: series, Clock: clock.NewFakeAtEpoch()}
	file := func(id int64, rel string, refs ...string) renamejob.File {
		return renamejob.File{FileOnRoot: library.FileOnRoot{MediaObjectID: id, RelPath: rel}, WorkID: 9, MediaRefs: refs}
	}
	profile := tv.DefaultNamingProfile()
	profile.Formats[tv.SlotStandardEpisode] = "{Series Title} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}"
	named, err := cfg.NameFiles(t.Context(), settings.Category{ID: 1, MediaType: "tv", Naming: &profile}, 9, []renamejob.File{
		file(1, "old/Vemari.Coast.S01E01E02.720p.HDTV.x264-GRP.mkv", "s01:e01", "s01:e02"),
		file(2, "old/whatever.mkv", "s01:e03"),
		file(3, "old/stray.mkv", "s04:e09"),
	})
	if err != nil {
		t.Fatalf("NameFiles: %v", err)
	}
	var paths []string
	for _, one := range named {
		paths = append(paths, one.RelPath)
	}
	want := []string{
		"Vemari Coast/Season 1/Vemari Coast - S01E01-E02 - Arrival + Departure HDTV-720p.mkv",
		"Vemari Coast/Season 1/Vemari Coast - S01E03 - Tide 1080p.mkv",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %q\nwant %q", paths, want)
	}
}
