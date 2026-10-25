package tv

import (
	"context"
	"log/slog"
	"path"
	"regexp"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/renamejob"
)

// TitleRenameJobType is the registered type of the sweep that renames episode files named while their title was TBA.
const TitleRenameJobType = jobs.Type("tv.rename_titled")

// TitleRenameScheduleKey is the schedule that runs the sweep.
const TitleRenameScheduleKey = "tv_title_rename"

// TitleRenameActionKey deduplicates the sweep: one at a time (Invariant 16).
const TitleRenameActionKey = "tv.rename_titled"

// TitleRenameDefinition declares the sweep; the renames themselves are move jobs.
var TitleRenameDefinition = jobs.Definition{
	Name: "Renaming episodes whose title arrived",
	Type: TitleRenameJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 1,
}

// untitledName finds "TBA" as a word in a file's name: what naming wrote for an episode with no title yet.
var untitledName = regexp.MustCompile(`(^|[^A-Za-z])TBA([^A-Za-z]|$)`)

// TitleRenameLibrary is a TV library with the roots its files live under.
type TitleRenameLibrary struct {
	Category settings.Category
	Roots    []settings.RootFolder
}

// TitleRenameConfig is what a TitleRenameJob needs; Logger is optional.
type TitleRenameConfig struct {
	Files renamejob.Files
	// Namer names a series' files, as a rename of the library would.
	Namer renamejob.Namer
	// Rename starts the rename of one work's files on one root with namer.
	Rename func(ctx context.Context, namer renamejob.Namer, category settings.Category, rootFolderID, workID int64) error
	// Libraries lists the TV libraries as they are now.
	Libraries func() []TitleRenameLibrary
	Logger    *slog.Logger
}

// TitleRenameJob renames the episode files named "TBA" once a refresh has brought their title: an import named them
// before the title was known (Episode Title Required off, or 48 hours passed). Only those files move.
type TitleRenameJob struct {
	config TitleRenameConfig
	logger *slog.Logger
}

// NewTitleRenameJob builds the handler.
func NewTitleRenameJob(config TitleRenameConfig) *TitleRenameJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &TitleRenameJob{config: config, logger: logger}
}

// Run looks at every TV library that renames its files, one root at a time.
func (j *TitleRenameJob) Run(ctx context.Context, _ jobs.Job) error {
	namer := untitledOnly{inner: j.config.Namer}
	for _, library := range j.config.Libraries() {
		if !NamingFor(library.Category.Naming).RenameFiles {
			continue
		}
		for _, root := range library.Roots {
			works, err := j.untitledWorks(ctx, library.Category.ID, root.ID)
			if err != nil {
				return jobs.Retryablef("tv.rename_titled", string(TitleRenameJobType), err)
			}
			for _, workID := range works {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := j.config.Rename(ctx, namer, library.Category, root.ID, workID); err != nil {
					j.logger.Warn("tv: rename of episodes named TBA", slog.Int64("work_id", workID), slog.Any("error", err))
				}
			}
		}
	}
	return nil
}

// untitledWorks are the series on a root with a file named TBA, each once; a file two series share is left alone.
func (j *TitleRenameJob) untitledWorks(ctx context.Context, categoryID, rootFolderID int64) ([]int64, error) {
	files, err := j.config.Files.RenameFilesOnRoot(ctx, categoryID, rootFolderID, 0)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	var works []int64
	for _, file := range files {
		if len(file.Works) != 1 || !untitledName.MatchString(path.Base(file.RelPath)) || seen[file.Works[0].WorkID] {
			continue
		}
		seen[file.Works[0].WorkID] = true
		works = append(works, file.Works[0].WorkID)
	}
	return works, nil
}

// untitledOnly names only a series' files named TBA, and only those whose new name has a title: every other file stays.
// Only the file's name changes; its folder is kept, so a series is never split between its old folder and a new one.
type untitledOnly struct{ inner renamejob.Namer }

func (n untitledOnly) NameFiles(ctx context.Context, category settings.Category, workID int64, files []renamejob.File) ([]renamejob.Named, error) {
	var untitled []renamejob.File
	for _, file := range files {
		if untitledName.MatchString(path.Base(file.RelPath)) {
			untitled = append(untitled, file)
		}
	}
	if len(untitled) == 0 {
		return nil, nil
	}
	named, err := n.inner.NameFiles(ctx, category, workID, untitled)
	if err != nil {
		return nil, err
	}
	folders := make(map[int64]string, len(untitled))
	for _, file := range untitled {
		folders[file.MediaObjectID] = path.Dir(file.RelPath)
	}
	titled := named[:0]
	for _, name := range named {
		if !untitledName.MatchString(path.Base(name.RelPath)) {
			name.RelPath = path.Join(folders[name.MediaObjectID], path.Base(name.RelPath))
			titled = append(titled, name)
		}
	}
	return titled, nil
}

// EnqueueTitleRename submits one sweep, deduplicated by TitleRenameActionKey.
func EnqueueTitleRename(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: TitleRenameJobType, ActionKey: TitleRenameActionKey, Priority: jobs.PriorityNormal})
}
