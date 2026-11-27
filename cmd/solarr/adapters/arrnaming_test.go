package adapters

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
)

func lidarrDefaultNaming() arrimport.Naming {
	preset := music.NamingScheme.Presets[1].Formats
	return arrimport.Naming{
		RenameFiles: true, ReplaceIllegalCharacters: true, ColonReplacement: 4,
		Formats: map[string]string{
			music.SlotArtistFolder: preset[music.SlotArtistFolder], music.SlotStandardTrack: preset[music.SlotStandardTrack],
			music.SlotMultiDiscTrack: preset[music.SlotMultiDiscTrack],
		},
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
	config.Formats[music.SlotStandardTrack] = "{Album Title}/{Track ArtistName} - {track:00} - {Track Title}"

	next, _, unsupported := arrNaming(music.Module{}, music.DefaultNamingProfile(), config)

	if next.Formats[music.SlotStandardTrack] != config.Formats[music.SlotStandardTrack] || len(unsupported) != 0 {
		t.Errorf("standard = %q, unsupported = %v, want the format carried", next.Formats[music.SlotStandardTrack], unsupported)
	}
}

func TestArrNaming_AnUnknownTokenIsDroppedAndNamedNotTheWholeFormat(t *testing.T) {
	config := lidarrDefaultNaming()
	config.Formats[music.SlotStandardTrack] = "{Album Title}/{Quality Full} {Track Title}"
	config.ColonReplacement = 9

	next, changes, unsupported := arrNaming(music.Module{}, music.DefaultNamingProfile(), config)

	if got, want := next.Formats[music.SlotStandardTrack], "{Album Title}/{Track Title}"; got != want {
		t.Errorf("standard = %q, want %q: the token dropped, the rest taken", got, want)
	}
	if want := []string{"colon replacement format 9", "standard_track: {Quality Full}"}; !reflect.DeepEqual(unsupported, want) {
		t.Errorf("unsupported = %v, want %v", unsupported, want)
	}
	if last := changes[len(changes)-1]; last.Key != "naming_unsupported" {
		t.Errorf("last change = %+v, want the note on what was not taken over", last)
	}
}

func TestArrNaming_SonarrsDefaultsCarryOverAndItsEditionTokenIsDropped(t *testing.T) {
	from := arrimport.Naming{
		RenameFiles: true, ReplaceIllegalCharacters: true, ColonReplacement: 4,
		Formats: map[string]string{
			tv.SlotStandardEpisode: "{Series TitleYear} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}{-Release Group}",
			tv.SlotDailyEpisode:    "{Series Title} - {Air-Date} - {Episode Title} {Custom Formats}",
			tv.SlotSeasonFolder:    "Season {season:00}",
		},
	}
	next, _, unsupported := arrNaming(tv.Module{}, tv.Module{}.DefaultNamingProfile(), from)

	want := map[string]string{
		tv.SlotStandardEpisode: "{Series TitleYear} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}[-{Release Group}]",
		tv.SlotDailyEpisode:    "{Series Title} - {Air-Date} - {Episode Title}",
		tv.SlotSeasonFolder:    "Season {season:00}",
	}
	for slot, format := range want {
		if next.Formats[slot] != format {
			t.Errorf("%s = %q, want %q", slot, next.Formats[slot], format)
		}
	}
	if !reflect.DeepEqual(unsupported, []string{"daily_episode: {Custom Formats}"}) {
		t.Errorf("unsupported = %v", unsupported)
	}
}

func TestArrNaming_RadarrsMovieFormatsFillTheMovieSlots(t *testing.T) {
	from := arrimport.Naming{
		RenameFiles: true, ColonReplacement: 3,
		Formats: map[string]string{movie.SlotStandardMovie: "{Movie CleanTitle} ({Release Year}) {Quality Full}", movie.SlotMovieFolder: "{Movie Title} ({Release Year})"},
	}
	next, changes, unsupported := arrNaming(movie.Module{}, movie.Module{}.DefaultNamingProfile(), from)

	if next.Formats[movie.SlotStandardMovie] != "{Movie CleanTitle} ({Release Year}) {Quality Full}" || next.ColonReplacement != "space_dash_space" || len(unsupported) != 0 {
		t.Errorf("formats = %v, colon = %q, unsupported = %v", next.Formats, next.ColonReplacement, unsupported)
	}
	if len(changes) == 0 {
		t.Error("no changes listed for a naming that differs from the default")
	}
}
