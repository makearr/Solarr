package movie_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/media/movie"
)

func TestTheDefaultNamingNamesAMovieAsItsLibraryFolderDoes(t *testing.T) {
	names := movie.NamingFor(nil)
	for _, c := range []struct {
		movie movie.FileNaming
		file  string
		want  string
	}{
		{movie.FileNaming{Title: "The Tonhut", Year: 1999}, "x.mkv", "The Tonhut (1999)/The Tonhut (1999).mkv"},
		{movie.FileNaming{Title: "Wjibxajmu's Leb", Year: 2006}, "x.mp4", "Wjibxajmu's Leb (2006)/Wjibxajmu's Leb (2006).mp4"},
		{movie.FileNaming{Title: "Fojgouk: Omjetzojco", Year: 1996}, "x.mkv", "Fojgouk Omjetzojco (1996)/Fojgouk Omjetzojco (1996).mkv"},
		{movie.FileNaming{Title: "Heat", Year: 1995}, "x.mkv", "Heat (1995)/Heat (1995).mkv"},
		{movie.FileNaming{Title: "Digvus & Bejic", Year: 1997}, "x.mp4", "Digvus & Bejic (1997)/Digvus & Bejic (1997).mp4"},
		{movie.FileNaming{Title: "Thunderbolts*", Year: 2025}, "x.mkv", "Thunderbolts- (2025)/Thunderbolts- (2025).mkv"},
	} {
		got, _, err := names.RenderMovie(c.movie, c.file)
		if err != nil || got != c.want {
			t.Errorf("%q: %q, %v; want %q", c.movie.Title, got, err, c.want)
		}
	}
}

func TestPresetsRenderEditionsAndIDsOnlyWhenThereAreSome(t *testing.T) {
	plain := movie.FileNaming{Title: "Nhasi Huvcem", Year: 1982, IDs: movie.IDs{IMDb: "tt0083658", TMDB: 78}}
	cut := plain
	cut.Edition = "Final Cut"
	for preset, want := range map[string][2]string{
		"Plex":     {"Nhasi Huvcem (1982) {imdb-tt0083658}/Nhasi Huvcem (1982).mkv", "Nhasi Huvcem (1982) {imdb-tt0083658}/Nhasi Huvcem (1982) {edition-Final Cut}.mkv"},
		"Jellyfin": {"Nhasi Huvcem (1982) [tmdbid-78]/Nhasi Huvcem (1982).mkv", "Nhasi Huvcem (1982) [tmdbid-78]/Nhasi Huvcem (1982) - Final Cut.mkv"},
	} {
		var formats map[string]string
		for _, candidate := range movie.NamingScheme.Presets {
			if candidate.Name == preset {
				formats = candidate.Formats
			}
		}
		profile := movie.DefaultNamingProfile()
		profile.Formats = formats
		names := movie.NamingFor(&profile)
		if got, _, err := names.RenderMovie(plain, "x.mkv"); err != nil || got != want[0] {
			t.Errorf("%s plain: %q, %v; want %q", preset, got, err, want[0])
		}
		if got, _, err := names.RenderMovie(cut, "x.mkv"); err != nil || got != want[1] {
			t.Errorf("%s edition: %q, %v; want %q", preset, got, err, want[1])
		}
	}
}

func TestNamingValidatesItsFormats(t *testing.T) {
	good := movie.DefaultNamingProfile()
	if err := movie.ValidateNaming(good); err != nil {
		t.Errorf("the default is invalid: %v", err)
	}
	bad := good
	bad.Formats = map[string]string{movie.SlotStandardMovie: "{Movie Titel}", movie.SlotMovieFolder: "{Movie Title}"}
	if movie.ValidateNaming(bad) == nil {
		t.Error("an unknown token must be refused")
	}
}

func TestRenameFilesOffKeepsTheFilesOwnName(t *testing.T) {
	profile := movie.DefaultNamingProfile()
	profile.RenameFiles = false
	got, _, err := movie.NamingFor(&profile).RenderMovie(movie.FileNaming{Title: "Heat", Year: 1995}, "Heat.1995.1080p.mkv")
	if err != nil || got != "Heat (1995)/Heat.1995.1080p.mkv" {
		t.Errorf("got %q, %v", got, err)
	}
}
