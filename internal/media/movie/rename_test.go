package movie_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/moving/renamejob"
)

type oneMovie library.WorkBasics

func (m oneMovie) Basics(context.Context, int64) (library.WorkBasics, error) {
	return library.WorkBasics(m), nil
}
func (oneMovie) ProviderIDs(context.Context, int64) (map[string]string, error) {
	return map[string]string{movie.ProviderTMDB: "603"}, nil
}

// A movie's file is named from the movie's facts under the library's naming.
func TestNameFiles_NamesTheMoviesFile(t *testing.T) {
	cfg := movie.RenameConfig{Movies: oneMovie{Title: "Kelvaro Run", Year: 2001}}
	named, err := cfg.NameFiles(t.Context(), settings.Category{ID: 1, MediaType: "movie"}, 4, []renamejob.File{{
		FileOnRoot: library.FileOnRoot{MediaObjectID: 8, RelPath: "incoming/Kelvaro.Run.2001.Directors.Cut.1080p.BluRay.x264-GRP.mkv"}, WorkID: 4,
	}})
	if err != nil {
		t.Fatalf("NameFiles: %v", err)
	}
	if len(named) != 1 || named[0].MediaObjectID != 8 || named[0].RelPath != "Kelvaro Run (2001)/Kelvaro Run (2001).mkv" {
		t.Errorf("named = %+v, want the movie's folder and name", named)
	}
}
