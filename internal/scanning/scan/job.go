package scan

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// JobType is the registered type of a library scan.
const JobType = jobs.Type("library.scan")

// Definition declares how a scan behaves. It is bulk work: long, resumable, and it
// must never sit in front of anything a user is waiting on.
var Definition = jobs.Definition{
	Name: "Scanning the library",
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
	// Directories, when set, rescans only these directories (relative to the
	// root) and leaves the root's watermark and cursor alone. Used once an
	// album's tracklist exists, so files seen before it can be matched to tracks.
	Directories []string `json:"directories,omitempty"`
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

// Housekeeping is a library's File Management at disk scan (Sonarr's), implemented by the media
// type (ADR-23). It is best effort: a failure is logged and never fails the scan.
type Housekeeping interface {
	AfterSweep(ctx context.Context, root settings.RootFolder, categoryID int64, outcome SweepOutcome) error
}

// SweepOutcome is what a finished sweep hands to Housekeeping.
type SweepOutcome struct {
	// MissingSince is when a full sweep marked its missing paths; zero after an incremental one.
	MissingSince     time.Time
	EmptyDirectories []string
}

// Sweep is what a media type brings to the sweep of its libraries (ADR-23).
type Sweep struct {
	Consumer Consumer
	// Include decides which files are worth probing. Nil takes everything.
	Include func(relPath string) bool
	// Housekeeping is optional.
	Housekeeping Housekeeping
	// OnSwept is optional: told after every sweep of a root, so derived counts catch up at once.
	OnSwept func(context.Context)
}

// Sweeps answers the sweep of the media type a category holds; false when no media type scans it.
type Sweeps interface {
	SweepFor(categoryID int64) (Sweep, bool)
}

// Always is the Sweeps of a process with a single sweep, whatever the category.
type Always Sweep

// SweepFor implements Sweeps.
func (a Always) SweepFor(int64) (Sweep, bool) { return Sweep(a), true }

// Job sweeps one root folder per run: the first job handler in the codebase.
type Job struct {
	sweeper  Sweeper
	scans    library.Scans
	roots    Roots
	tool     mediatool.Tool
	sweeps   Sweeps
	physical library.Physical
	clock    clock.Clock
	logger   *slog.Logger
}

// Config is what a Job needs. Every field is required except Logger.
type Config struct {
	FS       fsx.FS
	Scans    library.Scans
	Roots    Roots
	Tool     mediatool.Tool
	Sweeps   Sweeps
	Physical library.Physical
	Clock    clock.Clock
	Logger   *slog.Logger
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
		sweeps:   config.Sweeps,
		physical: config.Physical,
		clock:    config.Clock,
		logger:   logger,
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

	sweep, found := j.sweeps.SweepFor(payload.CategoryID)
	if !found {
		// A library whose media type has no module will not gain one on a retry.
		return jobs.Terminalf("scan.media_type", string(job.Type),
			fmt.Errorf("no media type scans category %d", payload.CategoryID))
	}

	if len(payload.Directories) > 0 {
		return j.rescanDirectories(ctx, root, sweep, payload, job.Progress)
	}

	progress, err := j.scans.Progress(ctx, root.ID)
	if err != nil {
		return err
	}
	if err := j.scans.Start(ctx, root.ID, j.clock.Now()); err != nil {
		return err
	}

	state := State{SweptThrough: progress.SweptThrough, Cursor: progress.Cursor}
	if payload.Full {
		// The last sweep's count is the best estimate of this one's, for the status bar's ETA.
		job.Progress.SetTotal(progress.FilesFound)
	}
	flow := pipeline{
		observe: func(ctx context.Context, batch Batch) error {
			if err := j.observe(ctx, root, sweep.Consumer, payload.CategoryID, batch); err != nil {
				return err
			}
			job.Progress.Add(int64(len(batch.Files)))
			return nil
		},
		advance: func(ctx context.Context, directory string) error { return j.scans.Advance(ctx, root.ID, directory) },
	}
	result, err := j.sweeper.Sweep(ctx, root.Path, state,
		Options{Full: payload.Full, Include: sweep.Include}, flow.submit)
	if waited := flow.wait(ctx); err == nil {
		err = waited
	}
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
	outcome := SweepOutcome{EmptyDirectories: result.EmptyDirectories}
	if payload.Full {
		j.markMissing(ctx, root, result.State.SweptThrough)
		outcome.MissingSince = result.State.SweptThrough
	}
	if sweep.Housekeeping != nil {
		if err := sweep.Housekeeping.AfterSweep(ctx, root, payload.CategoryID, outcome); err != nil {
			j.logger.Warn("scan housekeeping failed", slog.Int64("root_folder_id", root.ID), slog.Any("error", err))
		}
	}
	if sweep.OnSwept != nil {
		sweep.OnSwept(ctx)
	}
	return nil
}

// rescanDirectories reads and observes only the named directories. It records
// no scan progress: it did not walk the root, so it is no evidence about the
// rest of it, and marks nothing missing.
func (j *Job) rescanDirectories(
	ctx context.Context, root settings.RootFolder, sweep Sweep, payload Payload, progress *jobs.Progress,
) error {
	files := 0
	progress.SetTotal(int64(len(payload.Directories)))
	for _, directory := range payload.Directories {
		progress.Add(1)
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("scan.context", string(JobType), err)
		}
		batch, err := j.sweeper.ReadDirectory(root.Path, directory, sweep.Include)
		if err != nil {
			return err
		}
		if len(batch.Files) == 0 {
			continue
		}
		if err := j.observe(ctx, root, sweep.Consumer, payload.CategoryID, batch); err != nil {
			return err
		}
		files += len(batch.Files)
	}
	j.logger.Info("library directories rescanned",
		slog.Int64("root_folder_id", root.ID), slog.Int("directories", len(payload.Directories)),
		slog.Int("files", files))
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

// observe probes one batch in a single mediatool call and hands it to the consumer.
func (j *Job) observe(ctx context.Context, root settings.RootFolder, consumer Consumer, categoryID int64, batch Batch) error {
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

	return consumer.Observe(ctx, root, categoryID, observed)
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

// EnqueueDirectories submits a rescan of only these directories of one root.
// Its action key names the directories rather than the root: it never touches
// the root's cursor, which is what one-scan-per-root protects (Invariant 16), and
// sharing the root's key would drop it whenever an ordinary scan was active.
func EnqueueDirectories(
	ctx context.Context, queue *jobs.Queue, rootFolderID, categoryID int64, directories []string,
) (jobs.EnqueueResult, error) {
	sorted := slices.Clone(directories)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	payload, err := json.Marshal(Payload{RootFolderID: rootFolderID, CategoryID: categoryID, Directories: sorted})
	if err != nil {
		return jobs.EnqueueResult{}, fmt.Errorf("scan: encode payload: %w", err)
	}
	digest := sha256.Sum256([]byte(strings.Join(sorted, "\x00")))
	return queue.Enqueue(ctx, jobs.Request{
		Type:      JobType,
		ActionKey: fmt.Sprintf("%s:directories:%x", ActionKeyFor(rootFolderID), digest[:8]),
		Priority:  jobs.PriorityLow,
		Payload:   payload,
	})
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
