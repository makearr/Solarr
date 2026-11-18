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
	for name, test := range map[string]struct {
		tags []mediatool.Tags
		want []int
	}{
		"a compilation numbered 1..n without album artists is one album": {compilation, []int{12}},
		"two album artists under one title are two albums":               {twoAlbums, []int{6, 6}},
		"a pile of singles under one word stays per artist":              {singles, slicesOf(10, 1)},
		"feat. credits do not split an album":                            {featuring, []int{4}},
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
