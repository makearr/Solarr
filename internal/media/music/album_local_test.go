package music_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

func TestReadLocalTrack_TagsWin(t *testing.T) {
	got := music.ReadLocalTrack(mediatool.Tags{
		"title": "Tusa Behif Us", "album_artist": "P!po", "album": "Kebilegev Bibihi",
		"track": "4/13", "disc": "1/1", "date": "2017-10-13",
	}, mediatool.Measured{Duration: 270 * time.Second}, "P!po/Kebilegev Bibihi (2017)/P!po - 04 - Tusa Behif Us.m4a")

	want := music.LocalTrack{
		Title:  music.Claim{Value: "Tusa Behif Us", Provenance: music.ProvenanceTag},
		Artist: music.Claim{Value: "P!po", Provenance: music.ProvenanceTag},
		Album:  music.Claim{Value: "Kebilegev Bibihi", Provenance: music.ProvenanceTag},
		Disc:   1, Track: 4, Year: 2017, Duration: 270 * time.Second,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadLocalTrack = %+v\nwant %+v", got, want)
	}
}

// With no tags, the file name and folders still say what the file is, at parsed strength.
func TestReadLocalTrack_FallsBackToThePath(t *testing.T) {
	got := music.ReadLocalTrack(mediatool.Tags{}, mediatool.Measured{}, "P!po/Kebilegev Bibihi (2017)/P!po - 04 - Tusa Behif Us.m4a")

	want := music.LocalTrack{
		Title:  music.Claim{Value: "Tusa Behif Us", Provenance: music.ProvenanceParsed},
		Artist: music.Claim{Value: "P!po", Provenance: music.ProvenanceParsed},
		Album:  music.Claim{Value: "Kebilegev Bibihi", Provenance: music.ProvenanceParsed},
		Track:  4, Year: 2017,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadLocalTrack = %+v\nwant %+v", got, want)
	}
}

func TestReadLocalTrack_APlaceholderAbstains(t *testing.T) {
	got := music.ReadLocalTrack(mediatool.Tags{"artist": "Unknown Artist", "title": "103 Intro"}, mediatool.Measured{}, "Rips/103 Intro.flac")
	if got.Artist.Provenance != music.ProvenancePlaceholder {
		t.Errorf("Artist = %+v, want a placeholder", got.Artist)
	}
	if file := music.ReadLocalTrack(mediatool.Tags{}, mediatool.Measured{}, "Rips/103 Intro.flac"); file.Disc != 1 || file.Track != 3 || file.Title.Value != "Intro" {
		t.Errorf("from %q got disc %d track %d title %+v, want disc 1 track 3 \"Intro\"", "103 Intro.flac", file.Disc, file.Track, file.Title)
	}
}
