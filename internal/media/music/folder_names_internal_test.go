package music

import "testing"

// The folder a unit sits in names it when its tags do not: the folder itself, whether it is the
// imported folder (".") or nested under an artist folder (found 2026-09-30).
func TestFolderNames_NameTheUnitsOwnFolder(t *testing.T) {
	for name, test := range map[string]struct {
		root, directory, artist, album string
	}{
		"imported folder is the album": {"/incoming/Qozuvek Tarimo-Ilvaran Sepho-2020-WEB", ".", "Qozuvek Tarimo", "Ilvaran Sepho"},
		"nested artist/album":          {"/incoming", "Qozuvek Tarimo/Ilvaran Sepho", "", "Ilvaran Sepho"},
		"release-named subfolder":      {"/incoming", "Qozuvek Tarimo-Ilvaran Sepho-2020-WEB", "Qozuvek Tarimo", "Ilvaran Sepho"},
	} {
		artist, album := folderNames(test.root, test.directory)
		if artist != test.artist || album != test.album {
			t.Errorf("%s: folderNames = %q, %q; want %q, %q", name, artist, album, test.artist, test.album)
		}
	}
}
