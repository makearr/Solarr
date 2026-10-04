// Package linkjob plans and applies the linking of identical copies of one media object within a library
// (ADR-11, Invariant 92): the plan is a preview that changes nothing, the job re-plans and then applies it.
package linkjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/linker"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// JobType is the registered type of one linking run over a library.
const JobType = jobs.Type("link.identical")

// Definition declares bulk work in maintenance, never in front of anything interactive.
var Definition = jobs.Definition{
	Name: "Linking identical files",
	Type: JobType, Pool: jobs.PoolMove, Mutating: true, MaxAttempts: 1,
}

// ActionKeyFor deduplicates concurrent runs over one library.
func ActionKeyFor(categoryID int64) string { return fmt.Sprintf("%s:%d", JobType, categoryID) }

// Payload is what a run carries.
type Payload struct {
	CategoryID int64 `json:"category_id"`
}

// Root is a root folder a library's files may be linked on: online, writable and not read-only.
type Root struct {
	ID   int64
	Path string
}

// Roots answers a library's linkable roots.
type Roots func(categoryID int64) []Root

// Config is what a Planner and a Job need; every field is required except Logger.
type Config struct {
	FS     fsx.FS
	Links  library.ObjectLinks
	Roots  Roots
	Logger *slog.Logger
}

// Preview is what linking one library would do.
type Preview struct {
	Links            []linker.Link
	Skipped          []linker.Skipped
	ReclaimableBytes int64
}

// PlanFor works out the links of one library without touching a file.
func (c Config) PlanFor(ctx context.Context, categoryID int64) (Preview, error) {
	roots := map[int64]string{}
	for _, root := range c.Roots(categoryID) {
		roots[root.ID] = root.Path
	}
	shared, err := c.Links.Shared(ctx)
	if err != nil {
		return Preview{}, err
	}
	var objects []linker.Object
	for _, one := range shared {
		rootPath, inLibrary := roots[one.RootFolderID]
		// A path already linked is settled; a reflink is a different inode, so it is told apart by its kind.
		if !inLibrary || one.LinkKind != library.LinkPrimary {
			continue
		}
		if len(objects) == 0 || objects[len(objects)-1].MediaObjectID != one.MediaObjectID {
			objects = append(objects, linker.Object{MediaObjectID: one.MediaObjectID, SizeBytes: one.SizeBytes})
		}
		last := &objects[len(objects)-1]
		last.Paths = append(last.Paths, linker.Path{PathID: one.PathID, RootFolderID: one.RootFolderID, Abs: path.Join(rootPath, one.RelPath)})
	}
	links, skipped, err := linker.Plan(c.FS, objects)
	if err != nil {
		return Preview{}, err
	}
	preview := Preview{Links: links, Skipped: skipped}
	for _, link := range links {
		preview.ReclaimableBytes += link.SizeBytes
	}
	return preview, nil
}

// Job applies a fresh plan, one path at a time; a pair that fails is logged and the rest go on.
type Job struct{ config Config }

// NewJob builds the handler.
func NewJob(config Config) *Job {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Job{config: config}
}

// Run links one library.
func (j *Job) Run(ctx context.Context, job jobs.Job) error {
	var payload Payload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return jobs.Terminalf("linkjob.payload", string(job.Type), err)
	}
	preview, err := j.config.PlanFor(ctx, payload.CategoryID)
	if err != nil {
		return err
	}
	job.Progress.SetTotal(int64(len(preview.Links)))
	linked, failed := 0, 0
	var freed int64
	for _, link := range preview.Links {
		if err := ctx.Err(); err != nil {
			return err
		}
		method, err := linker.Apply(j.config.FS, link)
		if err == nil {
			err = j.config.Links.MarkLinked(ctx, link.Replace.PathID, string(method))
		}
		job.Progress.Add(1)
		if err != nil {
			failed++
			level := slog.LevelWarn
			if errors.Is(err, linker.ErrContentDiffers) {
				level = slog.LevelInfo
			}
			j.config.Logger.Log(ctx, level, "identical copies not linked",
				slog.String("path", link.Replace.Abs), slog.Any("error", err))
			continue
		}
		linked++
		freed += link.SizeBytes
	}
	j.config.Logger.Info("identical copies linked",
		slog.Int64("category_id", payload.CategoryID), slog.Int("linked", linked), slog.Int("failed", failed), slog.Int64("bytes_shared", freed))
	return nil
}

// Enqueue submits a linking run over one library, deduplicated by ActionKeyFor.
func Enqueue(ctx context.Context, queue *jobs.Queue, categoryID int64) (jobs.EnqueueResult, error) {
	data, err := json.Marshal(Payload{CategoryID: categoryID})
	if err != nil {
		return jobs.EnqueueResult{}, fmt.Errorf("linkjob: encode payload: %w", err)
	}
	return queue.Enqueue(ctx, jobs.Request{
		Type: JobType, ActionKey: ActionKeyFor(categoryID), Priority: jobs.PriorityLow, Payload: data,
	})
}
