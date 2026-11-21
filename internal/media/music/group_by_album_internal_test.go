package music

import (
	"fmt"
	"testing"

	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// folder builds pending files for tag sets, in order.
func folder(tags ...mediatool.Tags) ([]scan.Observed, []pendingFile) {
	observed := make([]scan.Observed, len(tags))
	files := make([]pendingFile, len(tags))
	for i, one := range tags {
		observed[i].Probe.Tags = one
		files[i] = pendingFile{index: i}
	}
	return observed, files
}

func groupSizes(groups [][]pendingFile) []int {
	sizes := make([]int, len(groups))
	for i, group := range groups {
		sizes[i] = len(group)
	}
	return sizes
}

func TestGroupByAlbum(t *testing.T) {
	compilation := make([]mediatool.Tags, 12)
	for i := range compilation {
		compilation[i] = mediatool.Tags{"album": "Invented Hits", "artist": fmt.Sprintf("Artist %d", i), "track": fmt.Sprint(i + 1)}
	}
	twoAlbums := make([]mediatool.Tags, 12)
	for i := range twoAlbums {
		owner := "A"
		if i >= 6 {
			owner = "B"
		}
		twoAlbums[i] = mediatool.Tags{"album": "Greatest Hits", "albumartist": owner, "artist": owner, "track": fmt.Sprint(i%6 + 1)}
	}
	singles := make([]mediatool.Tags, 10)
	for i := range singles {
		singles[i] = mediatool.Tags{"album": "Singles", "artist": fmt.Sprintf("Artist %d", i)}
	}
	featuring := []mediatool.Tags{
		{"album": "Invented Record", "artist": "Main Name", "track": "1"},
		{"album": "Invented Record", "artist": "Main Name feat. Guest", "track": "2"},
		{"album": "Invented Record", "artist": "Main Name", "track": "3"},
		{"album": "Invented Record", "artist": "Main Name & Other", "track": "4"},
	}
	discSet := []mediatool.Tags{
		{"album": "Invented Set (Disc 1)", "albumartist": "Ixu Band", "track": "1"},
		{"album": "Invented Set (Disc 1)", "albumartist": "Ixu Band", "track": "2"},
		{"album": "Invented Set (Disc 2)", "albumartist": "Ixu Band", "track": "1"},
		{"album": "Invented Set [CD 2]", "albumartist": "Ixu Band", "track": "2"},
	}
	boxSet := make([]mediatool.Tags, 12)
	for i := range boxSet {
		artist := "Main Name"
		if i%4 == 0 {
			artist = fmt.Sprintf("Main Name feat. Guest %d", i)
		}
		boxSet[i] = mediatool.Tags{"album": "Invented Box", "artist": artist, "musicbrainz_albumid": "rel-box", "track": fmt.Sprint(i%3 + 1)}
	}
	editions := []mediatool.Tags{
		{"album": "Invented Record", "albumartist": "Ixu Band", "musicbrainz_albumid": "rel-1", "track": "1"},
		{"album": "Invented Record", "albumartist": "Ixu Band", "musicbrainz_albumid": "rel-2", "track": "1"},
	}
	lostID := []mediatool.Tags{
		{"album": "Invented Record", "artist": "Ixu Band", "musicbrainz_albumid": "rel-1", "track": "1"},
		{"album": "Invented Record", "artist": "Ixu Band", "musicbrainz_albumid": "rel-1", "track": "2"},
		{"album": "invented  record", "albumartist": "Ixu Band", "track": "3"},
	}
	placeTaken := []mediatool.Tags{lostID[0], lostID[1], {"album": "Invented Record", "albumartist": "Ixu Band", "track": "2/3"}}
	twoHomes := append(append([]mediatool.Tags{}, editions...), mediatool.Tags{"album": "Invented Record", "albumartist": "Ixu Band", "track": "2"})
	otherTitle := []mediatool.Tags{
		{"album": "Other Record", "albumartist": "Ixu Band", "musicbrainz_albumid": "rel-9", "track": "1"},
		{"album": "Invented Record", "albumartist": "Ixu Band", "track": "2"},
	}
	otherArtist := []mediatool.Tags{lostID[0], {"album": "Invented Record", "albumartist": "Other Band", "track": "2"}}
	namedArtist := []mediatool.Tags{lostID[0], lostID[1], {"album": "Invented Record", "albumartist": "Ixu Band", "track": "3"}}
	strayTrackArtist := append(append([]mediatool.Tags{}, twoAlbums...), mediatool.Tags{"album": "Greatest Hits", "artist": "Z", "track": "7"})
	oneNamedAmongSingles := []mediatool.Tags{
		{"album": "Invented Mix", "albumartist": "Ixu Band"}, {"album": "Invented Mix", "artist": "Artist 1"},
		{"album": "Invented Mix", "artist": "Artist 2"}, {"album": "Invented Mix", "artist": "Artist 3"},
	}
	oneCollision := []mediatool.Tags{
		{"album": "Invented Hits", "artist": "Artist 1", "track": "1"}, {"album": "Invented Hits", "artist": "Artist 2", "track": "2"},
		{"album": "Invented Hits", "artist": "Artist 3", "track": "3"}, {"album": "Invented Hits", "artist": "Artist 4", "track": "4"},
		{"album": "Invented Hits", "artist": "Artist 5", "track": "5"}, {"album": "Invented Hits", "artist": "Artist 6", "track": "5"},
	}
	lostIDOtherArtist := []mediatool.Tags{lostID[0], {"album": "Invented Record", "artist": "Other Band", "track": "2"}}
	idCase := []mediatool.Tags{
		{"album": "Invented Record", "albumartist": "Ixu Band", "musicbrainz_albumid": "REL-1", "track": "1"},
		{"album": "Invented Record", "albumartist": "Ixu Band", "musicbrainz_albumid": "rel-1", "track": "2"},
	}
	untagged := []mediatool.Tags{
		{"album": "Invented Record", "albumartist": "Ixu Band"}, {"artist": "Ixu Band"}, {},
	}
	notADisc := []mediatool.Tags{{"album": "Abcd 2", "albumartist": "Ixu Band"}, {"album": "Abcd 3", "albumartist": "Ixu Band"}}
	for name, test := range map[string]struct {
		tags []mediatool.Tags
		want []int
	}{
		"a compilation numbered 1..n without album artists is one album": {compilation, []int{12}},
		"two album artists under one title are two albums":               {twoAlbums, []int{6, 6}},
		"a pile of singles under one word stays per artist":              {singles, slicesOf(10, 1)},
		"feat. credits do not split an album":                            {featuring, []int{4}},
		"a disc marker in the album tag does not split a set":            {discSet, []int{4}},
		"one release id is one album, whatever its numbers and artists":  {boxSet, []int{12}},
		"two release ids under one title are two editions":               {editions, []int{1, 1}},
		"a file that lost its release id joins it":                       {lostID, []int{3}},
		"a second copy that lost its release id joins it":                {placeTaken, []int{3}},
		"a file that lost its id beside two editions joins neither":      {twoHomes, []int{1, 1, 1}},
		"an album artist naming the release's track artist joins it":     {namedArtist, []int{3}},
		"a file of another title does not join a release":                {otherTitle, []int{1, 1}},
		"a file of another album artist does not join a release":         {otherArtist, []int{1, 1}},
		"a file naming no album artist follows the largest album":        {strayTrackArtist, []int{7, 6}},
		"one named album artist among singles is not a pile":             {oneNamedAmongSingles, []int{4}},
		"one repeated track number does not make singles":                {oneCollision, []int{6}},
		"a file with no album artists beside a release joins it":         {lostIDOtherArtist, []int{2}},
		"a release id is compared without its case":                      {idCase, []int{2}},
		"files with no album tag join the only album":                    {untagged, []int{3}},
		"a title merely ending in cd and a number is not a disc marker":  {notADisc, []int{1, 1}},
	} {
		t.Run(name, func(t *testing.T) {
			observed, files := folder(test.tags...)
			got := groupSizes(groupByAlbum(observed, files))
			if fmt.Sprint(got) != fmt.Sprint(test.want) {
				t.Errorf("group sizes = %v, want %v", got, test.want)
			}
		})
	}
}

func slicesOf(count, value int) []int {
	sizes := make([]int, count)
	for i := range sizes {
		sizes[i] = value
	}
	return sizes
}
