package music

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// The default preset keeps the library's own track shapes: "06" on one disc, "1-06" on several.
func TestNamingFor_DefaultTrackShapes(t *testing.T) {
	track := TrackNaming{ArtistName: "Heka", AlbumTitle: "Memory of Trees", ReleaseYear: 1995, Track: 6, TrackTitle: "Anywhere Is"}
	for _, tc := range []struct {
		name          string
		medium, discs int
		want          string
	}{
		{"single disc", 1, 1, "Heka/Memory of Trees (1995)/Heka - 06 - Anywhere Is.flac"},
		{"multi-disc", 2, 3, "Heka/Memory of Trees (1995)/Heka - 2-06 - Anywhere Is.flac"},
		{"no disc tag on a multi-disc release is disc 1", 0, 3, "Heka/Memory of Trees (1995)/Heka - 1-06 - Anywhere Is.flac"},
	} {
		track.Medium = tc.medium
		got, _, err := NamingFor(nil).RenderTrack(track, tc.discs, "source.flac")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Rename Tracks off keeps the file's own name inside the rendered folders.
func TestNamingFor_RenameFilesOffKeepsTheName(t *testing.T) {
	names := NamingFor(nil)
	names.RenameFiles = false
	track := TrackNaming{ArtistName: "Heka", AlbumTitle: "Watermark", ReleaseYear: 1988, Track: 3, TrackTitle: "Milaru Tidasola"}
	got, _, err := names.RenderTrack(track, 1, "downloads/03-heka-milaru_tidasola.flac")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Heka/Watermark (1988)/03-heka-milaru_tidasola.flac"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNamingScheme_EveryPresetValidates(t *testing.T) {
	for _, preset := range NamingScheme.Presets {
		if err := NamingScheme.Validate(preset.Formats); err != nil {
			t.Errorf("%s: %v", preset.Name, err)
		}
	}
}

// Every token the screen explains must actually render from a full set of facts.
func TestNamingScheme_EveryTokenRenders(t *testing.T) {
	ctx := singleDiscExample.Context()
	for name := range NamingScheme.Known() {
		if _, ok := ctx[name]; !ok {
			t.Errorf("token %q is offered but the example facts never fill it", name)
		}
	}
}

func TestValidateNaming(t *testing.T) {
	good := settings.NamingProfile{ColonReplacement: "smart", Formats: NamingScheme.Default().Formats}
	if err := ValidateNaming(good); err != nil {
		t.Errorf("the default preset: %v", err)
	}
	bad := settings.NamingProfile{ColonReplacement: "underscore", Formats: map[string]string{
		SlotArtistFolder: "{Artist Name}", SlotStandardTrack: "{Episode Title}", "season_folder": "x",
	}}
	err := ValidateNaming(bad)
	if err == nil {
		t.Fatal("a profile with an unknown token, a missing slot and an unknown slot validated")
	}
	for _, want := range []string{"Episode Title", "Multi Disc Track Format is empty", `"season_folder"`, `"underscore"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestDiscCount(t *testing.T) {
	parts := func(refs ...string) []library.PartDetail {
		details := make([]library.PartDetail, len(refs))
		for i, ref := range refs {
			details[i] = library.PartDetail{Part: librarydb.WorkPart{MediaRef: sql.NullString{String: ref, Valid: true}.String}}
		}
		return details
	}
	if got := DiscCount(parts("disc1:track1", "disc1:track2", "disc2:track1")); got != 2 {
		t.Errorf("DiscCount = %d, want 2", got)
	}
	if got := DiscCount(parts("disc1:track1", "not-a-ref")); got != 1 {
		t.Errorf("DiscCount with an unparsable ref = %d, want 1", got)
	}
}
