package transcode_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/platform/transcode"
)

func TestKeepTag(t *testing.T) {
	cases := []struct {
		key         string
		normalising bool
		want        bool
	}{
		{"TITLE", false, true}, {"Album Artist", false, true}, {"album_artist", false, true}, {"MUSICBRAINZ_TRACKID", false, true},
		{"MusicBrainz Album Id", false, true}, {"ACOUSTID_ID", false, true}, {"ARTISTSORT", false, true}, {"sort_name", false, true},
		{"SOLARR_TRANSCODED", false, true}, {"season_number", false, true}, {"REPLAYGAIN_TRACK_GAIN", false, true}, {"REPLAYGAIN_TRACK_GAIN", true, false},
		{"encoder", false, false}, {"creation_time", false, false}, {"handler_name", false, false}, {"BPS-eng", false, false},
		{"major_brand", false, false}, {"compatible_brands", false, false}, {"NUMBER_OF_FRAMES", false, false}, {"iTunSMPB", false, false},
	}
	for _, c := range cases {
		if got := transcode.KeepTag(c.key, c.normalising); got != c.want {
			t.Errorf("KeepTag(%q, %v) = %v, want %v", c.key, c.normalising, got, c.want)
		}
	}
}

func TestMarker(t *testing.T) {
	if got := transcode.MarkerValue("Anime 1080p", 4, 2, "2026-11-24"); got != "Anime 1080p;4;2;2026-11-24" {
		t.Fatalf("MarkerValue = %q", got)
	}
	if !transcode.HasMarker(map[string]string{"solarr_transcoded": "x;1;1;d"}) || transcode.HasMarker(map[string]string{"solarr_transcoded": ""}) || transcode.HasMarker(nil) {
		t.Fatal("HasMarker misreads the tags")
	}
}
