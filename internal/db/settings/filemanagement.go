package settings

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/config"
)

// PropersRepacks is Sonarr's "Propers and Repacks".
type PropersRepacks string

// The three Sonarr choices.
const (
	PropersPreferAndUpgrade PropersRepacks = "prefer_and_upgrade"
	PropersDoNotUpgrade     PropersRepacks = "do_not_upgrade"
	PropersDoNotPrefer      PropersRepacks = "do_not_prefer"
)

// RescanAfterRefresh is Sonarr's "Rescan Series Folder after Refresh".
type RescanAfterRefresh string

// The three Sonarr choices.
const (
	RescanAlways      RescanAfterRefresh = "always"
	RescanAfterManual RescanAfterRefresh = "after_manual"
	RescanNever       RescanAfterRefresh = "never"
)

// ChangeFileDate is Sonarr's "Change File Date"; music has one date to offer.
type ChangeFileDate string

// The choices music can honour.
const (
	FileDateNone        ChangeFileDate = "none"
	FileDateReleaseDate ChangeFileDate = "release_date"
)

// FileManagement is Sonarr's File Management and Permissions for one library (2026-09-24).
type FileManagement struct {
	PropersRepacks     PropersRepacks
	RescanAfterRefresh RescanAfterRefresh
	ChangeFileDate     ChangeFileDate
	// RecycleRetentionDays is how long quarantine keeps what this library removes (Sonarr's bin cleanup).
	RecycleRetentionDays     int64
	DeleteEmptyFolders       bool
	CreateEmptyArtistFolders bool
	// UnmonitorDeleted is Lidarr's "Ignore Deleted Tracks": an album losing a file is unmonitored.
	UnmonitorDeleted bool
	SetPermissions   bool
	// ChmodFolder is octal, as Sonarr takes it; files get the same mode without execute bits.
	ChmodFolder string
	// ChownGroup is a group name or number; empty leaves the group alone.
	ChownGroup string
}

// DefaultFileManagement is Sonarr's defaults, with Solarr's 7-day quarantine as the bin.
var DefaultFileManagement = FileManagement{
	PropersRepacks: PropersPreferAndUpgrade, RescanAfterRefresh: RescanAlways, ChangeFileDate: FileDateNone,
	RecycleRetentionDays: 7, ChmodFolder: "755",
}

var (
	chmodPattern = regexp.MustCompile(`^0?[0-7]{3}$`)
	groupPattern = regexp.MustCompile(`^([0-9]+|[a-z_][a-z0-9_.-]{0,31})$`)
)

// FolderMode is ChmodFolder as a mode; FileMode drops the execute bits, as Sonarr does.
func (f FileManagement) FolderMode() fs.FileMode {
	mode, err := strconv.ParseUint(f.ChmodFolder, 8, 32)
	if err != nil {
		return 0o755
	}
	return fs.FileMode(mode)
}

// FileMode is the mode a file placed in this library gets when permissions are set.
func (f FileManagement) FileMode() fs.FileMode {
	return f.FolderMode() &^ 0o111
}

func validateFileManagement(f FileManagement) config.Problems {
	var problems config.Problems
	switch f.PropersRepacks {
	case PropersPreferAndUpgrade, PropersDoNotUpgrade, PropersDoNotPrefer:
	default:
		problems = append(problems, config.Problem{Path: "propers_repacks", Value: string(f.PropersRepacks),
			Expectation: "prefer_and_upgrade, do_not_upgrade or do_not_prefer"})
	}
	switch f.RescanAfterRefresh {
	case RescanAlways, RescanAfterManual, RescanNever:
	default:
		problems = append(problems, config.Problem{Path: "rescan_after_refresh", Value: string(f.RescanAfterRefresh),
			Expectation: "always, after_manual or never"})
	}
	if f.ChangeFileDate != FileDateNone && f.ChangeFileDate != FileDateReleaseDate {
		problems = append(problems, config.Problem{Path: "change_file_date", Value: string(f.ChangeFileDate),
			Expectation: "none or release_date"})
	}
	if f.RecycleRetentionDays < 1 || f.RecycleRetentionDays > 365 {
		problems = append(problems, config.Problem{Path: "recycle_retention_days",
			Value: strconv.FormatInt(f.RecycleRetentionDays, 10), Expectation: "1 to 365 days"})
	}
	if !chmodPattern.MatchString(f.ChmodFolder) {
		problems = append(problems, config.Problem{Path: "chmod_folder", Value: f.ChmodFolder,
			Expectation: "three octal digits, such as 755"})
	} else if f.FolderMode()&0o700 != 0o700 {
		problems = append(problems, config.Problem{Path: "chmod_folder", Value: f.ChmodFolder,
			Expectation: "a mode that leaves Solarr full access to its own folders (7 first)"})
	}
	if f.ChownGroup != "" && !groupPattern.MatchString(f.ChownGroup) {
		problems = append(problems, config.Problem{Path: "chown_group", Value: f.ChownGroup,
			Expectation: "a group name or number"})
	}
	return problems
}

// SetCategoryFileManagement writes one library's file management (ADR-44: validated on write).
func (c *Cache) SetCategoryFileManagement(ctx context.Context, categoryID int64, f FileManagement) error {
	if _, found := c.Snapshot().Category(categoryID); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, categoryID)
	}
	if problems := validateFileManagement(f); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return writeCategoryFileManagement(ctx, queries, categoryID, f)
	})
}

func writeCategoryFileManagement(ctx context.Context, queries *settingsdb.Queries, categoryID int64, f FileManagement) error {
	return queries.UpsertCategoryFileManagement(ctx, settingsdb.UpsertCategoryFileManagementParams{
		CategoryID: categoryID, PropersRepacks: string(f.PropersRepacks),
		RescanAfterRefresh: string(f.RescanAfterRefresh), ChangeFileDate: string(f.ChangeFileDate),
		RecycleRetentionDays: f.RecycleRetentionDays, DeleteEmptyFolders: boolToInt(f.DeleteEmptyFolders),
		CreateEmptyArtistFolders: boolToInt(f.CreateEmptyArtistFolders), UnmonitorDeleted: boolToInt(f.UnmonitorDeleted),
		SetPermissions: boolToInt(f.SetPermissions), ChmodFolder: f.ChmodFolder, ChownGroup: f.ChownGroup,
	})
}

// withFileManagement attaches each library's stored row; a library without one keeps the defaults.
func withFileManagement(categories []Category, rows []settingsdb.CategoryFileManagement) []Category {
	stored := make(map[int64]settingsdb.CategoryFileManagement, len(rows))
	for _, row := range rows {
		stored[row.CategoryID] = row
	}
	for i := range categories {
		categories[i].FileManagement = DefaultFileManagement
		row, found := stored[categories[i].ID]
		if !found {
			continue
		}
		categories[i].FileManagement = FileManagement{
			PropersRepacks: PropersRepacks(row.PropersRepacks), RescanAfterRefresh: RescanAfterRefresh(row.RescanAfterRefresh),
			ChangeFileDate: ChangeFileDate(row.ChangeFileDate), RecycleRetentionDays: row.RecycleRetentionDays,
			DeleteEmptyFolders: row.DeleteEmptyFolders != 0, CreateEmptyArtistFolders: row.CreateEmptyArtistFolders != 0,
			UnmonitorDeleted: row.UnmonitorDeleted != 0, SetPermissions: row.SetPermissions != 0,
			ChmodFolder: row.ChmodFolder, ChownGroup: row.ChownGroup,
		}
	}
	return categories
}
