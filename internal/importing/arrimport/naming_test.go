package arrimport_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/importing/arrimport"
)

func TestNamingIsReadIntoTheSolarrSlotsOfEveryApp(t *testing.T) {
	transport := &recordingTransport{responses: map[string]string{
		"/api/v3/config/naming": `{"renameEpisodes":true,"replaceIllegalCharacters":true,"colonReplacementFormat":4,"standardEpisodeFormat":"{Series Title} - S{season:00}E{episode:00}","dailyEpisodeFormat":"{Series Title} - {Air-Date}","animeEpisodeFormat":"{Series Title} - {absolute:000}","seriesFolderFormat":"{Series Title}","seasonFolderFormat":"Season {season}","specialsFolderFormat":"Specials","multiEpisodeStyle":5}`,
		"/api/v1/config/naming": `{"renameBooks":false,"replaceIllegalCharacters":false,"colonReplacementFormat":0,"standardBookFormat":"{Book Title}","authorFolderFormat":"{Author Name}"}`,
	}, puts: map[string]string{}}
	lidarr := arrimport.NewLidarrClient(newTestClient(t, transport), "http://arr.local", "key")

	sonarr, err := lidarr.Sonarr().Naming(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wantSonarr := arrimport.Naming{
		RenameFiles: true, ReplaceIllegalCharacters: true, ColonReplacement: 4,
		Formats: map[string]string{
			"standard_episode": "{Series Title} - S{season:00}E{episode:00}", "daily_episode": "{Series Title} - {Air-Date}", "anime_episode": "{Series Title} - {absolute:000}",
			"series_folder": "{Series Title}", "season_folder": "Season {season}", "specials_folder": "Specials",
		},
	}
	if !reflect.DeepEqual(sonarr, wantSonarr) {
		t.Errorf("sonarr = %+v, want %+v", sonarr, wantSonarr)
	}

	readarr, err := lidarr.Readarr().Naming(t.Context())
	wantReadarr := arrimport.Naming{Formats: map[string]string{"standard_book": "{Book Title}", "author_folder": "{Author Name}"}}
	if err != nil || !reflect.DeepEqual(readarr, wantReadarr) {
		t.Errorf("readarr = %+v, %v, want %+v", readarr, err, wantReadarr)
	}
}

func TestNamingReadsRadarrsWordedColonReplacementAndWhisparrsEitherShape(t *testing.T) {
	transport := &recordingTransport{responses: map[string]string{
		"/api/v3/config/naming": `{"renameMovies":true,"replaceIllegalCharacters":true,"colonReplacementFormat":"spaceDashSpace","standardMovieFormat":"{Movie Title} ({Release Year})","movieFolderFormat":"{Movie Title}"}`,
	}, puts: map[string]string{}}
	lidarr := arrimport.NewLidarrClient(newTestClient(t, transport), "http://arr.local", "key")
	radarr, err := lidarr.Radarr().Naming(t.Context())
	wantRadarr := arrimport.Naming{
		RenameFiles: true, ReplaceIllegalCharacters: true, ColonReplacement: 3,
		Formats: map[string]string{"standard_movie": "{Movie Title} ({Release Year})", "movie_folder": "{Movie Title}"},
	}
	if err != nil || !reflect.DeepEqual(radarr, wantRadarr) {
		t.Errorf("radarr = %+v, %v, want %+v", radarr, err, wantRadarr)
	}
	whisparr, err := lidarr.Whisparr().Naming(t.Context())
	wantWhisparr := arrimport.Naming{
		RenameFiles: true, ReplaceIllegalCharacters: true, ColonReplacement: 3,
		Formats: map[string]string{"standard_scene": "{Movie Title} ({Release Year})", "studio_folder": "{Movie Title}"},
	}
	if err != nil || !reflect.DeepEqual(whisparr, wantWhisparr) {
		t.Errorf("whisparr v3 = %+v, %v, want %+v", whisparr, err, wantWhisparr)
	}
}

func TestLanguagesAreReadFromRadarrProfilesAndSonarrLanguageProfiles(t *testing.T) {
	transport := &recordingTransport{responses: map[string]string{
		"/api/v3/qualityprofile":  `[{"id":1,"name":"HD","language":{"id":1,"name":"English"}},{"id":2,"name":"Any","language":{"id":-1,"name":"Any"}},{"id":3,"name":"JP","language":{"id":8,"name":"Japanese"}},{"id":4,"name":"HD2","language":{"id":1,"name":"English"}}]`,
		"/api/v3/languageprofile": `[{"id":1,"name":"German","cutoff":{"id":4,"name":"German"}},{"id":2,"name":"Deep","cutoff":{"id":0,"name":"Unknown"}}]`,
	}, puts: map[string]string{}}
	lidarr := arrimport.NewLidarrClient(newTestClient(t, transport), "http://arr.local", "key")
	radarr, err := lidarr.Radarr().Languages(t.Context())
	if err != nil || !reflect.DeepEqual(radarr, []string{"en", "ja"}) {
		t.Errorf("radarr languages = %v, %v, want en and ja once each, Any left out", radarr, err)
	}
	sonarr, err := lidarr.Sonarr().Languages(t.Context())
	if err != nil || !reflect.DeepEqual(sonarr, []string{"de"}) {
		t.Errorf("sonarr languages = %v, %v, want de", sonarr, err)
	}
}
