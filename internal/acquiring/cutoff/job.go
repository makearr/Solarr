// Package cutoff is the cutoff check of every media type: wanted items whose files fall short of their library's
// cutoff become UPGRADABLE, and a second file a download brought is settled against the one it was meant to beat.
package cutoff

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
)

// JobType is the *Arrs' Cutoff Unmet, computed.
const JobType = jobs.Type("acquire.cutoff_check")

// Definition runs one check at a time; the next scheduled run is its retry.
var Definition = jobs.Definition{
	Name: "Checking the library against the quality cutoff", Type: JobType, Pool: jobs.PoolFiles, Mutating: true, MaxAttempts: 1,
}

// PageSize is how many items a media type hands over at a time.
const PageSize = 200

// The states the check moves an item between.
const (
	StateUpgradable = "UPGRADABLE"
	StateComplete   = "COMPLETE"
)

// Payload limits a run to one work, the one a download was just imported into; empty is every library.
type Payload struct {
	WorkID int64 `json:"work_id,omitempty"`
	// CategoryID is the work's library, which says whose check it is.
	CategoryID int64 `json:"category_id,omitempty"`
}

// Outcome is what checking one item came to.
type Outcome struct {
	// Replaced counts files recycled because another file of the same part was strictly better.
	Replaced int
	// State is what the item should be, when Decided; an item with a file not yet measured is not decided.
	State   string
	Decided bool
	// Rewrite says the item's counts were recomputed, so State is written even when it reads the same.
	Rewrite bool
}

// Check is a media type's part: which items it checks, and how one is settled and judged.
type Check interface {
	// CutoffPage is the next items to check after afterID, at most limit; workID narrows it to one work, all at once.
	CutoffPage(ctx context.Context, workID, afterID, limit int64) ([]library.CutoffItem, error)
	CheckCutoff(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (Outcome, error)
}

// Source is one media type's check, named for the log.
type Source struct {
	Name  string
	Check Check
}

// Config is what a Job needs; Logger is optional.
type Config struct {
	Desired  library.Desired
	Settings *settings.Cache
	// Sources lists the media types to check; a category narrows it to that library's type, zero is every type.
	Sources func(categoryID int64) []Source
	Logger  *slog.Logger
}

// Job walks every media type's checkable items once.
type Job struct{ config Config }

// NewJob builds the check.
func NewJob(config Config) Job {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return Job{config: config}
}

// Run checks each media type's items page by page. An item with a file not yet measured keeps its state.
func (j Job) Run(ctx context.Context, job jobs.Job) error {
	var payload Payload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return jobs.Terminalf("cutoff.payload", string(JobType), err)
		}
	}
	snapshot := j.config.Settings.Snapshot()
	for _, source := range j.config.Sources(payload.CategoryID) {
		if err := j.runSource(ctx, job, source, snapshot, payload.WorkID); err != nil {
			return err
		}
	}
	return nil
}

func (j Job) runSource(ctx context.Context, job jobs.Job, source Source, snapshot *settings.Snapshot, workID int64) error {
	var afterID int64
	var checked, upgradable, changed, replaced int
	for {
		items, err := source.Check.CutoffPage(ctx, workID, afterID, PageSize)
		if err != nil {
			return err
		}
		for _, item := range items {
			afterID = item.ID
			if err := ctx.Err(); err != nil {
				return err
			}
			outcome, err := source.Check.CheckCutoff(ctx, snapshot, item)
			if err != nil {
				return err
			}
			replaced += outcome.Replaced
			job.Progress.Add(1)
			if !outcome.Decided {
				continue
			}
			checked++
			if outcome.State == StateUpgradable {
				upgradable++
			}
			if outcome.Rewrite || outcome.State != item.State {
				if err := j.config.Desired.SetAlbumState(ctx, item.ID, outcome.State, item.PresentCount); err != nil {
					return err
				}
				changed++
			}
		}
		if len(items) < PageSize || workID != 0 {
			break
		}
	}
	j.config.Logger.Info("cutoff check finished", slog.String("media_type", source.Name), slog.Int("items", checked),
		slog.Int("cutoff_unmet", upgradable), slog.Int("changed", changed), slog.Int("files_replaced", replaced))
	return nil
}
