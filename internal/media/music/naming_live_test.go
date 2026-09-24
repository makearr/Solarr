package music_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/media/music"
)

// The default naming must reproduce the test library byte for byte (Invariant 73): every
// "Artist/Album (Year)/Artist - NN - Title" file, fed back in as facts, renders to itself.
// Skipped where the library is not mounted; run it by mounting /storage/media/music.
func TestDefaultNaming_ReproducesTheTestLibrary(t *testing.T) {
	root := os.Getenv("SOLARR_TEST_MUSIC_LIBRARY")
	if root == "" {
		root = "/storage/media/music"
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("test library not mounted at %s", root)
	}

	names := music.NamingFor(nil)
	total, skipped := 0, 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		facts, ok := factsFromPath(rel)
		if !ok {
			skipped++
			return nil
		}
		total++
		got, _, renderErr := names.RenderTrack(facts, 1, rel)
		if renderErr != nil || got != rel {
			t.Errorf("%s renders as %q (%v)", rel, got, renderErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 {
		t.Skip("no file in the library has the default shape")
	}
	t.Logf("%d files reproduce byte for byte; %d are not the default shape", total, skipped)
}

var defaultShape = regexp.MustCompile(`^([^/]+)/(.+) \(((?:19|20)\d{2})\)/([^/]+) - (\d{2}) - ([^/]+)\.[^./]+$`)

func factsFromPath(rel string) (music.TrackNaming, bool) {
	match := defaultShape.FindStringSubmatch(rel)
	if match == nil || match[1] != match[4] {
		return music.TrackNaming{}, false
	}
	year, _ := strconv.Atoi(match[3])
	track, _ := strconv.Atoi(match[5])
	return music.TrackNaming{
		ArtistName: match[1], AlbumTitle: match[2], ReleaseYear: year,
		Track: track, TrackTitle: strings.TrimSpace(match[6]),
	}, true
}
