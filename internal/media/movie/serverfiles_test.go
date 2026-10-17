package movie_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/moving/sidecar"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func TestServerFilesWritesTheNFOAndPicturesOnceAndOnlyOnWritableRoots(t *testing.T) {
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	added, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: 1, Monitor: movie.MonitorMovieOnly})
	if err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	fsys := fsx.NewMem(fake)
	rel := "The Example Film (1999)/The Example Film (1999).mkv"
	writeText(t, fsys, rel, "video")
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 9, []library.PhysicalFile{{RelPath: rel, SizeBytes: 5, QuickHash: "q", FirstSeenAt: addNow}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewSeries(opened).AttachWholeFiles(t.Context(), addNow, added.WorkID, 9, []int64{registered[0].MediaObjectID}); err != nil {
		t.Fatal(err)
	}
	writable := settings.RootFolder{ID: 9, Path: "/library", Online: true, Writable: true}
	writer := func(root settings.RootFolder) movie.ServerFiles {
		return movie.ServerFiles{
			Sidecar: sidecar.Writer{FS: fsys, Picture: func(context.Context, string) ([]byte, error) { return []byte("picture"), nil }},
			Reader:  library.NewSeriesReader(opened), Physical: library.NewPhysical(opened), Identity: library.NewIdentity(opened),
			Roots: oneRoot{root}, Clock: fake,
		}
	}

	readOnly := writable
	readOnly.ReadOnly = true
	if made, err := writer(readOnly).WriteMovie(t.Context(), 5, added.WorkID); err != nil || made != 0 {
		t.Fatalf("a read-only root got %d files, %v", made, err)
	}
	made, err := writer(writable).WriteMovie(t.Context(), 5, added.WorkID)
	if err != nil || made < 2 {
		t.Fatalf("wrote %d files, %v; want the nfo and at least the poster", made, err)
	}
	file, err := fsys.Open("/library/The Example Film (1999)/The Example Film (1999).nfo")
	if err != nil {
		t.Fatalf("no nfo beside the film: %v", err)
	}
	body, _ := io.ReadAll(file)
	_ = file.Close()
	for _, want := range []string{"<movie>", "<title>The Example Film</title>", `<uniqueid type="tmdb" default="true">603</uniqueid>`, `<uniqueid type="imdb">tt0000001</uniqueid>`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("nfo lacks %q:\n%s", want, body)
		}
	}
	if _, err := fsys.Stat("/library/The Example Film (1999)/poster.jpg"); err != nil {
		t.Errorf("no poster in the movie folder: %v", err)
	}
	if again, err := writer(writable).WriteMovie(t.Context(), 5, added.WorkID); err != nil || again != 0 {
		t.Errorf("a second pass wrote %d files, %v; nothing is replaced", again, err)
	}
}
