package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
)

// JobType is the registered type of a library scan.
const JobType = jobs.Type("library.scan")

// Definition declares how a scan behaves. It is bulk work: long, resumable, and it
// must never sit in front of anything a user is waiting on.
var Definition = jobs.Definition{
	Type: JobType, Pool: jobs.PoolMaintenance, Mutating: true, MaxAttempts: 3,
}

// Payload is what a scan job carries.
type Payload struct {
	RootFolderID int64 `json:"root_folder_id"`
	// Full ignores the watermark and reads every directory, for a first scan or a
	// library whose timestamps are not to be trusted.
	Full bool `json:"full"`
	// CategoryID is which category this scan run is scoped to -- a root can in
	// principle belong to several, so the enqueuer (which already knows) states
	// it rather than the job guessing. The consumer looks the category's own
	// policy up fresh at run time rather than trusting a value baked in at
	// enqueue time, the same "ask, don't cache" discipline Roots already uses.
	CategoryID int64 `json:"category_id"`
}

// ActionKeyFor deduplicates scans of one root: at most one may be active at a time
// (Invariant 16), because two sweeps of the same root would fight over one cursor.
func ActionKeyFor(rootFolderID int64) string {
	return fmt.Sprintf("%s:%d", JobType, rootFolderID)
}

// Observed is one file the sweep found, with what probing it reported. Probe.Err is
// set for a file that could not be read, which is not a reason to stop.
type Observed struct {
	File  Found
	Probe mediatool.Probe
}

// Consumer receives what a scan found, one committed batch at a time. Recording it
// in library.db is its job, not the scan's: the scan decides what changed, and
// deciding what a file *is* is the ADR-24 chain's question.
type Consumer interface {
	Observe(ctx context.Context, root settings.RootFolder, categoryID int64, batch []Observed) error
}

// Roots resolves a root folder. They are settings.db policy (ADR-25), so the job
// asks rather than reading the table.
type Roots interface {
	RootFolder(id int64) (settings.RootFolder, bool)
}

// Job sweeps one root folder per run: the first job handler in the codebase.
type Job struct {
	sweeper  Sweeper
	scans    library.Scans
	roots    Roots
	tool     mediatool.Tool
	consumer Consumer
	physical library.Physical
	clock    clock.Clock
	logger   *slog.Logger

	// include decides which files are worth probing. Nil takes everything.
	include func(relPath string) bool
}

// Config is what a Job needs. Every field is required except Include and Logger.
type Config struct {
	FS       fsx.FS
	Scans    library.Scans
	Roots    Roots
	Tool     mediatool.Tool
	Consumer Consumer
	Physical library.Physical
	Clock    clock.Clock
	Logger   *slog.Logger
	Include  func(relPath string) bool
}

// NewJob builds the handler.
func NewJob(config Config) *Job {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Job{
		sweeper:  NewSweeper(config.FS, config.Clock),
		scans:    config.Scans,
		roots:    config.Roots,
		tool:     config.Tool,
		consumer: config.Consumer,
		physical: config.Physical,
		clock:    config.Clock,
		logger:   logger,
		include:  config.Include,
	}
}

// Run sweeps the root the payload names, probing each committed batch and handing it
// to the consumer. Progress is persisted per batch, so a restart resumes rather than
// rescanning (section 10.4, ADR-3).
func (j *Job) Run(ctx context.Context, job jobs.Job) error {
	var payload Payload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		// A payload this process cannot read will not become readable on a retry.
		return jobs.Terminalf("scan.payload", string(job.Type), err)
	}

	root, found := j.roots.RootFolder(payload.RootFolderID)
	if !found {
		return jobs.Terminalf("scan.root", string(job.Type),
			fmt.Errorf("root folder %d is not configured", payload.RootFolderID))
	}
	// An unmounted root is a reason to wait, never to conclude the library is gone
	// (section 10.4). Retrying is exactly right: the disk may come back.
	if !root.Online {
		return jobs.Retryablef("scan.root", string(job.Type),
			fmt.Errorf("root folder %d (%s) is offline", root.ID, root.Path))
	}

	progress, err := j.scans.Progress(ctx, root.ID)
	if err != nil {
		return err
	}
	if err := j.scans.Start(ctx, root.ID, j.clock.Now()); err != nil {
		return err
	}

	state := State{SweptThrough: progress.SweptThrough, Cursor: progress.Cursor}
	result, err := j.sweeper.Sweep(ctx, root.Path, state,
		Options{Full: payload.Full, Include: j.include},
		func(ctx context.Context, batch Batch) error {
			return j.batch(ctx, root, payload.CategoryID, batch)
		})
	if err != nil {
		// Nothing to record: each batch persisted its own cursor as it committed,
		// which is also what survives the process simply being killed.
		return err
	}

	finished := library.FinishedScan{
		SweptThrough:    result.State.SweptThrough,
		FinishedAt:      j.clock.Now(),
		FilesFound:      int64(result.FilesFound),
		DirectoriesRead: int64(result.DirectoriesRead),
	}
	if err := j.scans.Finish(ctx, root.ID, finished); err != nil {
		return err
	}
	j.logger.Info("library scan finished",
		slog.Int64("root_folder_id", root.ID), slog.String("path", root.Path),
		slog.Int("files_found", result.FilesFound),
		slog.Int("directories_read", result.DirectoriesRead),
		slog.Int("directories_visited", result.DirectoriesVisited))

	// Only a full sweep visited every directory, so only a full sweep is evidence
	// that a path it did not re-observe is actually gone (section 10.4). An
	// incremental sweep skips directories whose mtime did not move and would
	// wrongly mark their contents missing.
	if payload.Full {
		j.markMissing(ctx, root, result.State.SweptThrough)
	}
	return nil
}

// markMissing is best-effort cleanup, not the sweep's core job: a failure here does
// not undo an otherwise-successful sweep, so it is logged rather than returned.
func (j *Job) markMissing(ctx context.Context, root settings.RootFolder, sweptThrough time.Time) {
	marked, err := j.physical.MarkMissing(ctx, root.ID, sweptThrough)
	if err != nil {
		j.logger.Warn("mark missing failed",
			slog.Int64("root_folder_id", root.ID), slog.String("path", root.Path),
			slog.Any("error", err))
		return
	}
	j.logger.Info("library paths marked missing",
		slog.Int64("root_folder_id", root.ID), slog.String("path", root.Path),
		slog.Int("marked", marked))
}

// batch probes one directory's files in a single mediatool call and hands the result
// on. The cursor advances only after the consumer has committed, which is what makes
// a restart resume rather than lose a batch.
func (j *Job) batch(ctx context.Context, root settings.RootFolder, categoryID int64, batch Batch) error {
	paths := make([]string, len(batch.Files))
	for index, file := range batch.Files {
		paths[index] = path.Join(root.Path, file.RelPath)
	}

	probes, err := j.tool.ReadTags(ctx, paths)
	if err != nil {
		return fmt.Errorf("scan: probe %s: %w", batch.Directory, err)
	}
	if len(probes) != len(batch.Files) {
		return fmt.Errorf("scan: probed %d of %d files in %s",
			len(probes), len(batch.Files), batch.Directory)
	}

	observed := make([]Observed, len(batch.Files))
	unreadable := 0
	for index, file := range batch.Files {
		observed[index] = Observed{File: file, Probe: probes[index]}
		if probes[index].Err != nil {
			unreadable++
		}
	}
	if unreadable > 0 {
		// Named, never silent: a directory of files nothing can read is how a
		// misconfigured library looks.
		j.logger.Warn("files could not be probed",
			slog.Int64("root_folder_id", root.ID),
			slog.String("directory", batch.Directory),
			slog.Int("unreadable", unreadable), slog.Int("of", len(batch.Files)))
	}

	if err := j.consumer.Observe(ctx, root, categoryID, observed); err != nil {
		return err
	}
	return j.scans.Advance(ctx, root.ID, batch.Directory)
}

// AudioExtensions are the containers stage 3 reads. It is an Include function rather
// than a constant list in the sweep, because a media type decides what its own files
// look like (ADR-23) and the sweep has no opinion.
var AudioExtensions = []string{".flac", ".mp3", ".m4a", ".ogg", ".opus", ".wav", ".aiff", ".wma", ".ape"}

// IsAudio reports a path worth probing as music.
func IsAudio(relPath string) bool {
	lowered := strings.ToLower(relPath)
	for _, extension := range AudioExtensions {
		if strings.HasSuffix(lowered, extension) {
			return true
		}
	}
	return false
}

// Enqueue submits a scan of one root, scoped to categoryID, deduplicated by
// ActionKeyFor.
func Enqueue(ctx context.Context, queue *jobs.Queue, rootFolderID, categoryID int64, full bool) (jobs.EnqueueResult, error) {
	payload, err := json.Marshal(Payload{RootFolderID: rootFolderID, CategoryID: categoryID, Full: full})
	if err != nil {
		return jobs.EnqueueResult{}, fmt.Errorf("scan: encode payload: %w", err)
	}
	return queue.Enqueue(ctx, jobs.Request{
		Type:      JobType,
		ActionKey: ActionKeyFor(rootFolderID),
		Priority:  jobs.PriorityLow,
		Payload:   payload,
	})
}
