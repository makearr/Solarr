package adapters

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media"
)

// A Sonarr's Media Management as a real one answers it, taken over by a TV library on Solarr's defaults: every
// setting Solarr has one for, and only the changed ones listed.
func TestArrFileManagementTakesSonarrsMediaManagement(t *testing.T) {
	sonarr := arrimport.MediaManagement{
		DownloadPropersAndRepacks: "doNotUpgrade", RescanAfterRefresh: "afterManual", FileDate: "utcAirDate",
		DeleteEmptyFolders: true, ImportExtraFiles: true, ExtraFileExtensions: "srt", RecycleBinCleanupDays: 7,
		ChmodFolder: "755", EpisodeTitleRequired: "bulkSeasonReleases", UnmonitorEpisodes: true,
	}
	tvExtras := media.ExtraFilesDefault{Import: true, Extensions: []string{"srt"}}
	got, changes := arrFileManagement(settings.DefaultFileManagement, sonarr, settings.ArrAppSonarr, tvExtras)

	want := settings.DefaultFileManagement
	want.PropersRepacks = settings.PropersDoNotUpgrade
	want.RescanAfterRefresh = settings.RescanAfterManual
	want.ChangeFileDate = settings.FileDateReleaseDate
	want.DeleteEmptyFolders = true
	want.UnmonitorDeleted = true
	want.EpisodeTitleRequired = settings.TitleRequiredBulkSeason
	if !reflect.DeepEqual(got, want) {
		t.Errorf("file management = %+v\nwant %+v (extras equal to the type's stay the type's)", got, want)
	}
	wantChanges := []apiserver.ArrSettingChange{
		{Key: "propers_repacks", Label: "Propers and repacks", Current: "Prefer and upgrade", Proposed: "Do not upgrade automatically"},
		{Key: "rescan_after_refresh", Label: "Rescan after refresh", Current: "Always", Proposed: "After manual refresh"},
		{Key: "change_file_date", Label: "Change file date", Current: "None", Proposed: "Release or air date"},
		{Key: "delete_empty_folders", Label: "Delete empty folders", Current: "Off", Proposed: "On"},
		{Key: "unmonitor_deleted", Label: "Unmonitor deleted", Current: "Off", Proposed: "On"},
		{Key: "episode_title_required", Label: "Episode title required", Current: "Always", Proposed: "Only for bulk season releases"},
	}
	if !reflect.DeepEqual(changes, wantChanges) {
		t.Errorf("changes = %+v\nwant %+v", changes, wantChanges)
	}

	// Taken over again, nothing is left to change.
	if _, again := arrFileManagement(got, sonarr, settings.ArrAppSonarr, tvExtras); len(again) != 0 {
		t.Errorf("a second take-over still changes %+v", again)
	}
}

// Radarr's and Lidarr's own unmonitor flags and file dates, extras that differ from the type's, and a cleanup of
// 0 days (never), which Solarr's bin cannot hold, kept as it was.
func TestArrFileManagementReadsEachAppsOwnWords(t *testing.T) {
	radarr := arrimport.MediaManagement{FileDate: "cinemas", UnmonitorMovies: true, UnmonitorEpisodes: false, ImportExtraFiles: false, RecycleBinCleanupDays: 0}
	got, _ := arrFileManagement(settings.DefaultFileManagement, radarr, settings.ArrAppRadarr, media.ExtraFilesDefault{Import: true, Extensions: []string{"srt"}})
	off := false
	if got.ChangeFileDate != settings.FileDateReleaseDate || !got.UnmonitorDeleted || got.RecycleRetentionDays != 7 ||
		got.ExtraFiles.Import == nil || *got.ExtraFiles.Import != off {
		t.Errorf("radarr: %+v", got)
	}

	lidarr := arrimport.MediaManagement{FileDate: "none", UnmonitorTracks: true, CreateEmptyArtistFolders: true, ImportExtraFiles: true, ExtraFileExtensions: "lrc, .CUE"}
	got, _ = arrFileManagement(settings.DefaultFileManagement, lidarr, settings.ArrAppLidarr, media.ExtraFilesDefault{Extensions: []string{"lrc"}})
	if got.ChangeFileDate != settings.FileDateNone || !got.UnmonitorDeleted || !got.CreateEmptyArtistFolders ||
		!reflect.DeepEqual(got.ExtraFiles.Extensions, []string{"lrc", "cue"}) || got.EpisodeTitleRequired != settings.TitleRequiredAlways {
		t.Errorf("lidarr: %+v", got)
	}
}
