package adapters

import (
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/importing/arrimport"
)

func TestRewritePath_TheFirstMatchingPrefixApplies(t *testing.T) {
	rewrites := []apiserver.PathRewrite{{From: "/media/music/", To: "/mnt/media/music"}, {From: "/media", To: "/other"}}
	for path, want := range map[string]string{
		"/media/music/Katupoken": "/mnt/media/music/Katupoken",
		"/media/music":           "/mnt/media/music",
		"/media/musicals/Heka":   "/other/musicals/Heka",
		"/elsewhere/Heka":        "/elsewhere/Heka",
	} {
		if got := rewritePath(path, rewrites); got != want {
			t.Errorf("rewritePath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestAlbumWanted_ByWhatTheArrHolds(t *testing.T) {
	album := func(files, total int64) arrimport.LidarrAlbum {
		var a arrimport.LidarrAlbum
		a.Statistics.TrackFileCount, a.Statistics.TotalTrackCount = files, total
		return a
	}
	whole, partial, unknown := album(10, 10), album(3, 10), album(0, 0)
	for _, tc := range []struct {
		which string
		album arrimport.LidarrAlbum
		want  bool
	}{
		{"all", partial, true}, {"missing", partial, true}, {"missing", whole, false},
		{"complete", whole, true}, {"complete", partial, false}, {"complete", unknown, false}, {"missing", unknown, true},
	} {
		if got := albumWanted(tc.album, tc.which); got != tc.want {
			t.Errorf("albumWanted(%+v, %q) = %v, want %v", tc.album.Statistics, tc.which, got, tc.want)
		}
	}
}
