package adapters

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media/music"
)

func lidarrDefaultNaming() arrimport.NamingConfig {
	preset := music.NamingScheme.Presets[1].Formats
	return arrimport.NamingConfig{
		RenameTracks: true, ReplaceIllegalCharacters: true, ColonReplacementFormat: 4,
		ArtistFolderFormat: preset[music.SlotArtistFolder], StandardTrackFormat: preset[music.SlotStandardTrack],
		MultiDiscTrackFormat: preset[music.SlotMultiDiscTrack],
	}
}

func TestArrNaming_LidarrsDefaultsBecomeTheLidarrDefaultPreset(t *testing.T) {
	next, changes, unsupported := arrNaming(music.Module{}, music.DefaultNamingProfile(), lidarrDefaultNaming())

	if want := music.NamingScheme.Presets[1].Formats; !reflect.DeepEqual(next.Formats, want) {
		t.Errorf("formats = %v, want the Lidarr default preset %v", next.Formats, want)
	}
	if len(unsupported) != 0 || next.ColonReplacement != "smart" || !next.RenameFiles {
		t.Errorf("unsupported = %v, colon = %q, rename = %v, want none, smart, on", unsupported, next.ColonReplacement, next.RenameFiles)
	}
	if len(changes) == 0 || changes[0].Key != "naming_standard_track" {
		t.Errorf("changes = %+v, want the standard track format first", changes)
	}
	if _, again, _ := arrNaming(music.Module{}, next, lidarrDefaultNaming()); len(again) != 0 {
		t.Errorf("a second preview lists %+v, want nothing left to change", again)
	}
}

func TestArrNaming_ATrackArtistTokenIsCarried(t *testing.T) {
	config := lidarrDefaultNaming()
	config.StandardTrackFormat = "{Album Title}/{Track ArtistName} - {track:00} - {Track Title}"

	next, _, unsupported := arrNaming(music.Module{}, music.DefaultNamingProfile(), config)

	if next.Formats[music.SlotStandardTrack] != config.StandardTrackFormat || len(unsupported) != 0 {
		t.Errorf("standard = %q, unsupported = %v, want the format carried", next.Formats[music.SlotStandardTrack], unsupported)
	}
}

func TestArrNaming_AnUnknownTokenKeepsTheLibrarysFormatAndIsNamed(t *testing.T) {
	config := lidarrDefaultNaming()
	config.StandardTrackFormat = "{Album Title}/{Quality Full} {Track Title}"
	config.ColonReplacementFormat = 9
	current := music.DefaultNamingProfile()

	next, changes, unsupported := arrNaming(music.Module{}, current, config)

	if next.Formats[music.SlotStandardTrack] != current.Formats[music.SlotStandardTrack] {
		t.Errorf("standard = %q, want the library's own kept", next.Formats[music.SlotStandardTrack])
	}
	if want := []string{"colon replacement format 9", "standard_track: {Quality Full}"}; !reflect.DeepEqual(unsupported, want) {
		t.Errorf("unsupported = %v, want %v", unsupported, want)
	}
	if last := changes[len(changes)-1]; last.Key != "naming_unsupported" {
		t.Errorf("last change = %+v, want the note on what was not taken over", last)
	}
}
