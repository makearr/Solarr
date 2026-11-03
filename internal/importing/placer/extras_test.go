package placer_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/placer"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func TestExtrasFollowTheirVideoOutOfASubsFolder(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	for _, rel := range []string{"/in/Rel/Show.S01E01.en.srt", "/in/Rel/Subs/Show.S01E01.German.forced.srt", "/in/Rel/Subs/Other.srt"} {
		if err := fsys.MkdirAll(rel[:len(rel)-len(baseOf(rel))-1], 0o755); err != nil {
			t.Fatal(err)
		}
		w, err := fsys.Create(rel)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("x"))
		_ = w.Close()
	}
	if err := fsys.MkdirAll("/lib/Show/Season 1", 0o755); err != nil {
		t.Fatal(err)
	}
	p := placer.New(placer.Config{FS: fsys})
	others := []inspect.File{
		{RelPath: "Rel/Show.S01E01.en.srt", SizeBytes: 1},
		{RelPath: "Rel/Subs/Show.S01E01.German.forced.srt", SizeBytes: 1},
		{RelPath: "Rel/Subs/Other.srt", SizeBytes: 1},
	}
	owners := []placer.Owner{{SourceRel: "Rel/Show.S01E01.mkv", DestRel: "Show/Season 1/Show - S01E01.mkv"}}
	got := p.Extras([]string{"srt"}, "/in", settings.RootFolder{ID: 7, Path: "/lib"}, others, owners, []mover.PlaceMethod{mover.PlaceCopy})
	want := []library.UnitExtra{
		{RootFolderID: 7, RelPath: "Show/Season 1/Show - S01E01.en.srt"},
		{RootFolderID: 7, RelPath: "Show/Season 1/Show - S01E01.German.forced.srt"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extras = %+v, want %+v", got, want)
	}
}

func baseOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
