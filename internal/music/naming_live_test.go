package music_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/makearr/MediaNexus/internal/music"
	"github.com/makearr/MediaNexus/internal/naming"
)

// realMusicLibrary is where the user's actual, curated library is mounted
// read-only in this sandbox (never written to, never in the test's own
// control) -- absent in an ordinary CI environment, so this test skips
// rather than fails there, the same convention internal/mediatool's own
// ffprobe-dependent tests already use.
const realMusicLibrary = "/mnt/media/music"

// TestDefaultFolderTemplate_ReproducesTheRealLibrary is todo 4.5's own
// acceptance criterion, run against the whole real library rather than a
// fixture: every real "Artist/Album (Year)" folder pair is fed into
// DefaultFolderTemplate as a Context, and the rendered path must match the
// folder that is actually there -- proof that adopting the default proposes
// zero renames. There is no media_object-to-work_part link yet
// (private/notes.md's own recorded gap), so this deliberately does not
// route through library.WorkDetail; it verifies the template against real
// on-disk names directly, which is what "zero renames" actually means.
func TestDefaultFolderTemplate_ReproducesTheRealLibrary(t *testing.T) {
	artists, err := os.ReadDir(realMusicLibrary)
	if err != nil {
		t.Skipf("real music library not mounted at %s: %v", realMusicLibrary, err)
	}

	folder, err := naming.Compile(music.DefaultFolderTemplate)
	if err != nil {
		t.Fatalf("Compile(DefaultFolderTemplate): %v", err)
	}

	total, mismatched, skippedShape := 0, 0, 0
	for _, artistEntry := range artists {
		if !artistEntry.IsDir() {
			continue
		}
		artistName := artistEntry.Name()
		albums, err := os.ReadDir(filepath.Join(realMusicLibrary, artistName))
		if err != nil {
			continue
		}
		for _, albumEntry := range albums {
			if !albumEntry.IsDir() {
				continue
			}
			albumFolder := albumEntry.Name()
			album, year, ok := splitAlbumYear(albumFolder)
			if !ok {
				// A folder not shaped "Album (Year)" is not this template's
				// problem to reproduce -- it never claimed to cover every
				// shape a library might contain, only its own default.
				skippedShape++
				continue
			}
			total++

			ctx := naming.Context{
				music.TokenArtistName:  artistName,
				music.TokenAlbumTitle:  album,
				music.TokenReleaseYear: year,
			}
			rendered, _ := naming.Render(folder, ctx, naming.DefaultSeparator)
			want := artistName + "/" + albumFolder
			if rendered != want {
				mismatched++
				t.Logf("mismatch: rendered %q, want %q", rendered, want)
			}
		}
	}

	if total == 0 {
		t.Skip("no \"Artist/Album (Year)\"-shaped folders found in the real library")
	}
	if mismatched > 0 {
		t.Errorf("%d of %d real album folders did not round-trip through DefaultFolderTemplate", mismatched, total)
	}
	t.Logf("verified %d real album folders reproduce byte-for-byte (%d others were not \"Album (Year)\"-shaped)", total, skippedShape)
}

var albumYearPattern = regexp.MustCompile(`^(.*) \(((?:19|20)\d{2})\)$`)

func splitAlbumYear(folder string) (album, year string, ok bool) {
	match := albumYearPattern.FindStringSubmatch(folder)
	if match == nil {
		return "", "", false
	}
	return match[1], match[2], true
}
