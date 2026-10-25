package adapters

import (
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media"
)

// arrPropers, arrRescan and arrTitleRequired read the *Arr's words as File Management's; an unknown word is left out.
var (
	arrPropers = map[string]settings.PropersRepacks{
		"preferAndUpgrade": settings.PropersPreferAndUpgrade, "doNotUpgrade": settings.PropersDoNotUpgrade, "doNotPrefer": settings.PropersDoNotPrefer,
	}
	arrRescan = map[string]settings.RescanAfterRefresh{
		"always": settings.RescanAlways, "afterManual": settings.RescanAfterManual, "never": settings.RescanNever,
	}
	arrTitleRequired = map[string]settings.EpisodeTitleRequired{
		"always": settings.TitleRequiredAlways, "bulkSeasonReleases": settings.TitleRequiredBulkSeason, "never": settings.TitleRequiredNever,
	}
)

// settingWords are File Management's values as the File Management page words them.
var settingWords = map[string]string{
	string(settings.PropersPreferAndUpgrade): "Prefer and upgrade", string(settings.PropersDoNotUpgrade): "Do not upgrade automatically",
	string(settings.PropersDoNotPrefer): "Do not prefer", string(settings.RescanAlways): "Always",
	string(settings.RescanAfterManual): "After manual refresh", string(settings.RescanNever): "Never",
	string(settings.FileDateNone): "None", string(settings.FileDateReleaseDate): "Release or air date",
	string(settings.TitleRequiredBulkSeason): "Only for bulk season releases",
}

// arrFileManagement is a library's File Management with the *Arr's Media Management taken over, and each setting it
// changes in words. What Solarr has no setting for (hardlinks, the recycle bin's path, free space) is left out.
func arrFileManagement(
	current settings.FileManagement, from arrimport.MediaManagement, app settings.ArrApp, extras media.ExtraFilesDefault,
) (settings.FileManagement, []apiserver.ArrSettingChange) {
	next := current
	if value, known := arrPropers[from.DownloadPropersAndRepacks]; known {
		next.PropersRepacks = value
	}
	if value, known := arrRescan[from.RescanAfterRefresh]; known {
		next.RescanAfterRefresh = value
	}
	switch from.FileDate {
	case "":
	case "none":
		next.ChangeFileDate = settings.FileDateNone
	default:
		next.ChangeFileDate = settings.FileDateReleaseDate
	}
	next.DeleteEmptyFolders = from.DeleteEmptyFolders
	switch app {
	case settings.ArrAppSonarr:
		next.UnmonitorDeleted = from.UnmonitorEpisodes
		if value, known := arrTitleRequired[from.EpisodeTitleRequired]; known {
			next.EpisodeTitleRequired = value
		}
	case settings.ArrAppRadarr:
		next.UnmonitorDeleted = from.UnmonitorMovies
	default:
		next.UnmonitorDeleted = from.UnmonitorTracks
		next.CreateEmptyArtistFolders = from.CreateEmptyArtistFolders
	}
	importExtras := from.ImportExtraFiles
	extensions := splitExtensions(from.ExtraFileExtensions)
	next.ExtraFiles = settings.ExtraFiles{Import: &importExtras, Extensions: extensions}
	if from.RecycleBinCleanupDays >= 1 && from.RecycleBinCleanupDays <= 365 {
		next.RecycleRetentionDays = from.RecycleBinCleanupDays
	}
	next.SetPermissions = from.SetPermissionsLinux
	if from.SetPermissionsLinux && from.ChmodFolder != "" {
		next.ChmodFolder = from.ChmodFolder
	}
	next.ChownGroup = from.ChownGroup

	var changes []apiserver.ArrSettingChange
	add := func(key, label, was, now string) {
		if was != now {
			changes = append(changes, apiserver.ArrSettingChange{Key: key, Label: label, Current: words(was), Proposed: words(now)})
		}
	}
	add("propers_repacks", "Propers and repacks", string(current.PropersRepacks), string(next.PropersRepacks))
	add("rescan_after_refresh", "Rescan after refresh", string(current.RescanAfterRefresh), string(next.RescanAfterRefresh))
	add("change_file_date", "Change file date", string(current.ChangeFileDate), string(next.ChangeFileDate))
	add("delete_empty_folders", "Delete empty folders", onOff(current.DeleteEmptyFolders), onOff(next.DeleteEmptyFolders))
	add("unmonitor_deleted", "Unmonitor deleted", onOff(current.UnmonitorDeleted), onOff(next.UnmonitorDeleted))
	if app == settings.ArrAppLidarr {
		add("create_empty_artist_folders", "Create empty artist folders", onOff(current.CreateEmptyArtistFolders), onOff(next.CreateEmptyArtistFolders))
	}
	if app == settings.ArrAppSonarr {
		add("episode_title_required", "Episode title required", titleRequired(current.EpisodeTitleRequired), titleRequired(next.EpisodeTitleRequired))
	}
	wasOn, wasExtensions := current.ExtraFiles.Resolve(extras.Import, extras.Extensions)
	add("import_extra_files", "Import extra files", onOff(wasOn), onOff(importExtras))
	if importExtras && !slices.Equal(wasExtensions, extensions) {
		add("extra_file_extensions", "Extra file extensions", strings.Join(wasExtensions, ","), strings.Join(extensions, ","))
	}
	add("recycle_retention_days", "Recycle bin cleanup (days)", strconv.FormatInt(current.RecycleRetentionDays, 10), strconv.FormatInt(next.RecycleRetentionDays, 10))
	add("set_permissions", "Set permissions", onOff(current.SetPermissions), onOff(next.SetPermissions))
	if next.SetPermissions {
		add("chmod_folder", "Folder mode", current.ChmodFolder, next.ChmodFolder)
	}
	add("chown_group", "Group", current.ChownGroup, next.ChownGroup)
	// Unchanged extras keep what the library stored, so a type's default stays a default.
	if wasOn == importExtras && (!importExtras || slices.Equal(wasExtensions, extensions)) {
		next.ExtraFiles = current.ExtraFiles
	}
	return next, changes
}

// splitExtensions reads "srt, .ass" as srt and ass; nothing is no extensions.
func splitExtensions(list string) []string {
	var out []string
	for _, part := range strings.Split(list, ",") {
		if part = strings.TrimPrefix(strings.TrimSpace(part), "."); part != "" {
			out = append(out, strings.ToLower(part))
		}
	}
	return out
}

func words(value string) string {
	if word, known := settingWords[value]; known {
		return word
	}
	if value == "" {
		return "(none)"
	}
	return value
}

func onOff(on bool) string {
	if on {
		return "On"
	}
	return "Off"
}

// titleRequired reads the stored default, empty, as always.
func titleRequired(value settings.EpisodeTitleRequired) string {
	if value == "" {
		return string(settings.TitleRequiredAlways)
	}
	return string(value)
}
