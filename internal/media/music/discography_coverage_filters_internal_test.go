package music

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// META-20: what counts toward an artist's discography is what their follow, else their library, asks for.
func TestDiscographyCoverage_CountsWhatTheFollowAndTheLibraryAskFor(t *testing.T) {
	groups := []musicbrainz.ReleaseGroup{
		{MBID: "a", PrimaryType: "Album"}, {MBID: "s1", PrimaryType: "Single"}, {MBID: "s2", PrimaryType: "Single"}, {MBID: "e", PrimaryType: "EP"},
	}
	count := func(job *DiscographyCoverageJob, follows map[followKey]string, person, category int64) int {
		filters := job.filtersFor(follows, person, category)
		total := 0
		for _, group := range groups {
			if filters.Admits(group) {
				total++
			}
		}
		return total
	}
	albumsOnly := []string{"Album"}
	job := &DiscographyCoverageJob{config: DiscographyCoverageConfig{
		Category: func(id int64) (settings.Category, bool) {
			if id == 1 {
				return settings.Category{MonitorReleaseTypes: albumsOnly}, true
			}
			return settings.Category{}, true
		},
	}}
	follows := map[followKey]string{{personID: 7, categoryID: 2}: `{"include_primary_types":["Single"]}`}
	for _, c := range []struct {
		name             string
		person, category int64
		want             int
	}{
		{"a library monitoring albums only counts the album", 5, 1, 1},
		{"a library with no release types counts the defaults", 5, 2, 4},
		{"a follow's own types win over the library's", 7, 2, 2},
		{"a follow in another library does not apply", 7, 1, 1},
	} {
		if got := count(job, follows, c.person, c.category); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}
