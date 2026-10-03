package tv

import (
	"context"
	"log/slog"
	"path"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/platform/paginate"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// RetryJobType is the registered type of the sweep that asks again about files a scan could not place.
const RetryJobType = jobs.Type("tv.retry_unidentified")

// RetryActionKey deduplicates the sweep: one at a time (Invariant 16).
const RetryActionKey = "tv.retry_unidentified"

// RetryDefinition declares the sweep: background metadata work, retried on transient failure.
var RetryDefinition = jobs.Definition{
	Name: "Identifying files that waited for review again",
	Type: RetryJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 2,
}

const (
	retryPageSize = 500
	// retryBatch is about how many files one pass hands the consumer; a folder is never split across batches.
	retryBatch = 300
)

// RetryLibrary is a TV library with the roots its files live under.
type RetryLibrary struct {
	CategoryID int64
	Roots      []settings.RootFolder
}

// RetryConfig is what a RetryJob needs; Logger is optional.
type RetryConfig struct {
	Unidentified library.Unidentified
	Consumer     Consumer
	FS           fsx.FS
	// Libraries lists the TV libraries as they are now.
	Libraries func() []RetryLibrary
	Logger    *slog.Logger
}

// RetryJob runs the scan's own identification again over the files waiting for review: a scan only offers new or
// changed files, so without it a file the providers could not name at first is never looked at again.
type RetryJob struct {
	config RetryConfig
	logger *slog.Logger
}

// NewRetryJob builds the handler.
func NewRetryJob(config RetryConfig) *RetryJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &RetryJob{config: config, logger: logger}
}

// Run walks every TV library's waiting files once.
func (j *RetryJob) Run(ctx context.Context, _ jobs.Job) error {
	before, _ := j.config.Unidentified.Count(ctx)
	for _, lib := range j.config.Libraries() {
		for _, root := range lib.Roots {
			if err := j.retryRoot(ctx, root, lib.CategoryID); err != nil {
				return jobs.Retryablef("tv.retry", string(RetryJobType), err)
			}
		}
	}
	after, _ := j.config.Unidentified.Count(ctx)
	j.logger.Info("tv retry of unidentified files finished", slog.Int64("waiting_before", before), slog.Int64("waiting_after", after))
	return nil
}

func (j *RetryJob) retryRoot(ctx context.Context, root settings.RootFolder, categoryID int64) error {
	var rows []library.Row
	cursor := paginate.Cursor{}
	for {
		page, err := j.config.Unidentified.List(ctx, []int64{root.ID}, cursor, retryPageSize)
		if err != nil {
			return err
		}
		rows = append(rows, page...)
		if len(page) < retryPageSize {
			break
		}
		last := page[len(page)-1]
		cursor = paginate.Cursor{Sort: last.FirstSeenAt, ID: last.MediaObjectID}
	}
	slices.SortStableFunc(rows, func(a, b library.Row) int { return strings.Compare(a.RelPath, b.RelPath) })
	for start := 0; start < len(rows); {
		end := min(start+retryBatch, len(rows))
		for end < len(rows) && topFolder(rows[end].RelPath) == topFolder(rows[end-1].RelPath) {
			end++
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := j.config.Consumer.Observe(ctx, root, categoryID, j.observed(root, rows[start:end])); err != nil {
			return err
		}
		start = end
	}
	return nil
}

func topFolder(relPath string) string {
	folder, _, _ := strings.Cut(relPath, "/")
	return folder
}

// observed rebuilds what a scan would have handed over; a file that is gone is left out, the next scan marks it missing.
func (j *RetryJob) observed(root settings.RootFolder, rows []library.Row) []scan.Observed {
	out := make([]scan.Observed, 0, len(rows))
	for _, row := range rows {
		absolute := path.Join(root.Path, row.RelPath)
		info, err := j.config.FS.Stat(absolute)
		if err != nil {
			continue
		}
		out = append(out, scan.Observed{
			File:  scan.Found{RelPath: row.RelPath, SizeBytes: info.Size, ModTime: info.ModTime},
			Probe: mediatool.Probe{Path: absolute},
		})
	}
	return out
}

// EnqueueRetry submits one retry sweep, deduplicated by RetryActionKey.
func EnqueueRetry(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: RetryJobType, ActionKey: RetryActionKey, Priority: jobs.PriorityNormal})
}
