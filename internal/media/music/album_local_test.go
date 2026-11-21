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
		Disc:   1, Track: 4, TrackTotal: 13, Year: 2017, OriginalYear: 2017, Duration: 270 * time.Second,
		FolderAlbum: "Kebilegev Bibihi",
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
		Track:  4, Year: 2017, OriginalYear: 2017,
		FolderAlbum: "Kebilegev Bibihi",
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

// A value that says nothing gives way to one that does: a Windows short name had replaced
// Cibiz M. in the tags and the artist folder, but not in the file names (the main library).
func TestReadLocalTrack_AMangledShortNameGivesWayToTheFileName(t *testing.T) {
	got := music.ReadLocalTrack(mediatool.Tags{"artist": "DORA9E~V", "album": "Gold", "title": "Modamiku of Tivilute", "track": "12"},
		mediatool.Measured{}, "DORA9E~V/Gold (2019)/Cibiz M. - 12 - Modamiku of Tivilute.m4a")

	if want := (music.Claim{Value: "Cibiz M.", Provenance: music.ProvenanceParsed}); got.Artist != want {
		t.Errorf("Artist = %+v, want %+v", got.Artist, want)
	}
}

// With no artist anywhere else, a short name is still no artist.
func TestReadLocalTrack_AShortNameAloneIsAPlaceholder(t *testing.T) {
	got := music.ReadLocalTrack(mediatool.Tags{"artist": "DORA9E~V"}, mediatool.Measured{}, "Rips/12 Modamiku of Tivilute.m4a")

	if want := (music.Claim{Value: "DORA9E~V", Provenance: music.ProvenancePlaceholder}); got.Artist != want {
		t.Errorf("Artist = %+v, want %+v", got.Artist, want)
	}
}

func TestReadLocalTrack_ReadsWhatIsPrintedOnTheRelease(t *testing.T) {
	for name, tc := range map[string]struct {
		tags    mediatool.Tags
		barcode string
		catalog string
		total   int
	}{
		"spaced upc":     {mediatool.Tags{"barcode": "0 12345 67890 5"}, "012345678905", "", 0},
		"upc tag":        {mediatool.Tags{"UPC": "012345678905"}, "012345678905", "", 0},
		"too short":      {mediatool.Tags{"barcode": "1234"}, "", "", 0},
		"too long":       {mediatool.Tags{"barcode": "123456789012345"}, "", "", 0},
		"catalogue":      {mediatool.Tags{"CATALOGNUMBER": " XYZ-123 "}, "", "XYZ-123", 0},
		"total separate": {mediatool.Tags{"tracknumber": "3", "tracktotal": "12"}, "", "", 12},
		"total slashed":  {mediatool.Tags{"tracknumber": "3/12"}, "", "", 12},
	} {
		got := music.ReadLocalTrack(tc.tags, mediatool.Measured{}, "a/b.flac")
		if got.Barcode != tc.barcode || got.CatalogNumber != tc.catalog || got.TrackTotal != tc.total {
			t.Errorf("%s: barcode %q catalogue %q total %d, want %q %q %d", name, got.Barcode, got.CatalogNumber, got.TrackTotal, tc.barcode, tc.catalog, tc.total)
		}
	}
}

func TestReadLocalTrack_KeepsTheOriginalDateApartFromTheEditions(t *testing.T) {
	both := music.ReadLocalTrack(mediatool.Tags{"originaldate": "1973-03-01", "date": "2011-06-01"}, mediatool.Measured{}, "a/b.flac")
	if both.Year != 2011 || both.OriginalYear != 1973 {
		t.Errorf("both tags: year %d original %d, want 2011 and 1973", both.Year, both.OriginalYear)
	}
	only := music.ReadLocalTrack(mediatool.Tags{"date": "1973"}, mediatool.Measured{}, "a/b.flac")
	if only.Year != 1973 || only.OriginalYear != 1973 {
		t.Errorf("one tag: year %d original %d, want 1973 for both", only.Year, only.OriginalYear)
	}
}
