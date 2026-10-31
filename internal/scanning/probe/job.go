// Package probe fills in ADR-39's measured half: a background sweep that
// ffprobes every file with no measured_attributes_id yet and records what it
// actually is, as opposed to what a release name claims. Deliberately a
// separate job from internal/scanning/scan rather than folded into it -- confirmed
// with the user (private/next.md, todo 4.2) -- so a scan's own write
// transaction stays bounded and probing can run at the lowest priority
// without ever blocking identification.
package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path"
	"sync/atomic"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// JobType is the registered type of the background probe sweep.
const JobType = jobs.Type("library.probe")

// ActionKey deduplicates the sweep: global, not per-root, since it walks
// every root's unmeasured files in one pass (Invariant 16).
const ActionKey = "library.probe"

// Definition declares how probing behaves: background, lowest priority,
// retried on transient failure, never blocking anything a user is waiting on.
var Definition = jobs.Definition{
	Name: "Reading audio details",
	Type: JobType, Pool: jobs.PoolFiles, Mutating: true, MaxAttempts: 3,
}

// batchSize bounds how many unmeasured files one pass asks the store for
// before probing them and checking in with ListUnmeasured again -- the same
// crash-safety reasoning as EnrichJob's own batch: each batch's writes are
// already their own transaction per file, so a shutdown mid-run loses at most
// one batch's worth of otherwise-committed progress.
const batchSize = 100

// yieldAfter bounds one run: the maintenance pool has one slot, and a first sweep of a 200,000-file
// library held it for hours while download polling and rescans waited (found 2026-09-28).
const yieldAfter = 2 * time.Minute

// Roots resolves a root folder by id. They are settings.db policy (ADR-25),
// so the job asks rather than reading the table -- the same interface
// internal/scanning/scan already asks of its own Roots dependency.
type Roots interface {
	RootFolder(id int64) (settings.RootFolder, bool)
}

// Job probes every media_object with no measured half yet, in bounded,
// resumable batches, until none remain or the context is cancelled.
type Job struct {
	physical library.Physical
	coverage library.ProbeCoverage
	roots    Roots
	tool     mediatool.Tool
	fs       fsx.FS
	clock    clock.Clock
	logger   *slog.Logger
	// resumeAt is each list's place when a run yielded, so the continuation does not re-read the files that failed first.
	resumeAt [4]atomic.Int64
}

// Config is what a Job needs. Every field is required except Logger.
type Config struct {
	Physical library.Physical
	Coverage library.ProbeCoverage
	Roots    Roots
	Tool     mediatool.Tool
	// FS lists the folder of a video for subtitle files beside it; nil leaves those out.
	FS     fsx.FS
	Clock  clock.Clock
	Logger *slog.Logger
}

// NewJob builds the handler.
func NewJob(config Config) *Job {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Job{
		physical: config.Physical, coverage: config.Coverage, roots: config.Roots,
		tool: config.Tool, fs: config.FS, clock: config.Clock, logger: logger,
	}
}

// Run implements jobs.Handler. There is no persisted cursor across runs --
// only within one, so a file that fails its probe (a missing root, a
// corrupted file) does not spin the batch forever; the next scheduled run
// (there is no scheduler yet, so today that means the next process restart)
// simply asks the same question again.
func (j *Job) Run(ctx context.Context, _ jobs.Job) error {
	started := j.clock.Now()
	total, failed := 0, 0
	// Unmeasured files first; then those measured before the probe read lyrics languages.
	for index, list := range []func(context.Context, int64, int64) ([]library.UnmeasuredFile, error){
		j.physical.ListUnmeasured, j.physical.ListStaleProbes, j.physical.ListStaleVideoProbes,
	} {
		afterID := j.resumeAt[index].Swap(0)
		for {
			if err := ctx.Err(); err != nil {
				return jobs.Retryablef("probe.context", string(JobType), err)
			}

			batch, err := list(ctx, afterID, batchSize)
			if err != nil {
				return fmt.Errorf("probe: %w", err)
			}
			if len(batch) == 0 {
				break
			}

			afterID = batch[len(batch)-1].MediaObjectID
			total += len(batch)
			for file, err := range j.probeBatch(ctx, batch) {
				failed++
				// One file failing (an unmounted root, a corrupted file, a
				// stalled network mount) is not a reason to stop the sweep --
				// it simply stays unmeasured and is retried next run.
				j.logger.WarnContext(ctx, "probe failed", slog.Int64("media_object_id", file.MediaObjectID), slog.Any("error", err))
			}
			if len(batch) < batchSize {
				break
			}
			if j.clock.Now().Sub(started) >= yieldAfter {
				j.resumeAt[index].Store(afterID)
				j.logger.Info("probe sweep yielding", slog.Int("processed", total), slog.Int("failed", failed))
				return fmt.Errorf("probe: %d files in %s: %w", total, yieldAfter, jobs.ErrYield)
			}
		}
	}
	if err := j.backfillOshash(ctx, started); err != nil {
		return err
	}
	j.logger.Info("probe sweep finished", slog.Int("processed", total), slog.Int("failed", failed))

	// Best-effort: a coverage recompute failing is not a reason to fail a
	// sweep that otherwise measured real files -- the next run's own
	// reconcile tries again, the same drift-and-repair shape it is built on.
	if corrections, err := j.coverage.Reconcile(ctx); err != nil {
		j.logger.WarnContext(ctx, "probe coverage reconcile failed", slog.Any("error", err))
	} else if len(corrections) > 0 {
		j.logger.Info("probe coverage reconciled", slog.Int("corrections", len(corrections)))
	}
	return nil
}

// oshashPass indexes resumeAt for the hash backfill, after the three probe lists.
const oshashPass = 3

// backfillOshash hashes the video files scanned before the hash was written at scan time (ADR-35); a file that
// cannot be read stays unhashed and is asked again next run.
func (j *Job) backfillOshash(ctx context.Context, started time.Time) error {
	if j.fs == nil {
		return nil
	}
	afterID := j.resumeAt[oshashPass].Swap(0)
	hashed := 0
	for {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("probe.context", string(JobType), err)
		}
		batch, err := j.physical.ListVideoWithoutOshash(ctx, afterID, batchSize)
		if err != nil {
			return fmt.Errorf("probe: %w", err)
		}
		for _, file := range batch {
			if err := j.hashFile(ctx, file); err != nil {
				j.logger.WarnContext(ctx, "oshash failed", slog.Int64("media_object_id", file.MediaObjectID), slog.Any("error", err))
			} else {
				hashed++
			}
		}
		if len(batch) < batchSize {
			return nil
		}
		afterID = batch[len(batch)-1].MediaObjectID
		if j.clock.Now().Sub(started) >= yieldAfter {
			j.resumeAt[oshashPass].Store(afterID)
			return fmt.Errorf("probe: %d hashes in %s: %w", hashed, yieldAfter, jobs.ErrYield)
		}
	}
}

func (j *Job) hashFile(ctx context.Context, file library.UnmeasuredFile) error {
	root, found := j.roots.RootFolder(file.RootFolderID)
	if !found {
		return fmt.Errorf("root folder %d is not configured", file.RootFolderID)
	}
	absolute := path.Join(root.Path, file.RelPath)
	info, err := j.fs.Stat(absolute)
	if err != nil {
		return err
	}
	oshash, err := library.OSHash(j.fs, absolute, info.Size)
	if err != nil || oshash == "" {
		return err
	}
	return j.physical.SetOshash(ctx, file.MediaObjectID, oshash)
}

// probeBatch reads a whole batch at once (the tool runs its files side by side up to its own limit) and records what
// each answered; it returns the files that failed, each with why.
func (j *Job) probeBatch(ctx context.Context, files []library.UnmeasuredFile) func(yield func(library.UnmeasuredFile, error) bool) {
	paths := make([]string, 0, len(files))
	asked := make([]library.UnmeasuredFile, 0, len(files))
	var failures []failedFile
	for _, file := range files {
		root, found := j.roots.RootFolder(file.RootFolderID)
		if !found {
			failures = append(failures, failedFile{file, fmt.Errorf("root folder %d is not configured", file.RootFolderID)})
			continue
		}
		paths = append(paths, path.Join(root.Path, file.RelPath))
		asked = append(asked, file)
	}
	probes, err := j.tool.ReadTags(ctx, paths)
	for index, file := range asked {
		switch {
		case err != nil:
			failures = append(failures, failedFile{file, fmt.Errorf("ffprobe: %w", err)})
		case index >= len(probes):
			failures = append(failures, failedFile{file, fmt.Errorf("ffprobe returned %d results for %d files", len(probes), len(asked))})
		case probes[index].Err != nil:
			failures = append(failures, failedFile{file, probes[index].Err})
		default:
			if recordErr := j.record(ctx, file, paths[index], probes[index]); recordErr != nil {
				failures = append(failures, failedFile{file, recordErr})
			}
		}
	}
	return func(yield func(library.UnmeasuredFile, error) bool) {
		for _, failure := range failures {
			if !yield(failure.file, failure.err) {
				return
			}
		}
	}
}

type failedFile struct {
	file library.UnmeasuredFile
	err  error
}

func (j *Job) record(ctx context.Context, file library.UnmeasuredFile, absolute string, probe mediatool.Probe) error {
	measured := probe.Measured
	measured.CRF = j.encoderCRF(absolute, measured)
	claim := attributeClaimFromMeasured(measured, sidecarSubtitleLanguages(j.fs, absolute))
	probeJSON, err := json.Marshal(measured)
	if err != nil {
		return fmt.Errorf("marshal probe json: %w", err)
	}

	// ADR-39: a claim that fails its probe is a signal. claimedFound is false
	// for every file today -- the claimed side has nowhere in this codebase
	// that writes it yet -- but the comparison runs for real the moment it
	// does, rather than needing this job changed too.
	mismatchJSON := ""
	if claimed, claimedFound, err := j.physical.ClaimedAttributesFor(ctx, file.MediaObjectID); err != nil {
		j.logger.WarnContext(ctx, "read claimed attributes failed",
			slog.Int64("media_object_id", file.MediaObjectID), slog.Any("error", err))
	} else if claimedFound {
		mismatchJSON = library.MismatchJSON(library.CompareAttributes(claimed, claim))
	}

	return j.physical.RecordMeasured(ctx, file.MediaObjectID, claim, library.MeasuredScalars{
		DurationMs:     measured.Duration.Milliseconds(),
		Width:          measured.Width,
		Height:         measured.Height,
		VideoBitrate:   measured.VideoBitrate,
		AudioBitrate:   measured.AudioBitrate,
		OverallBitrate: measured.OverallBitrate,
		ProbeJSON:      string(probeJSON),
		ProbeVersion:   library.ProbeVersionCurrent,
	}, mismatchJSON, j.clock.Now().Unix())
}

// encoderCRF reads the CRF an x264 or x265 encode states in its settings from the start of the file; 0 for any other
// picture, or when the file cannot be read (the probe already succeeded, so this one fact is simply absent).
func (j *Job) encoderCRF(absolute string, measured mediatool.Measured) float64 {
	if measured.Height == 0 || (measured.VideoCodec != "h264" && measured.VideoCodec != "hevc") {
		return 0
	}
	file, err := j.fs.Open(absolute)
	if err != nil {
		return 0
	}
	defer func() { _ = file.Close() }()
	header := make([]byte, mediatool.EncoderHeaderBytes)
	read, _ := io.ReadFull(file, header)
	return mediatool.EncoderCRF(header[:read])
}

// Enqueue submits one probe sweep, deduplicated by ActionKey.
func Enqueue(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{
		Type: JobType, ActionKey: ActionKey, Priority: jobs.PriorityLow,
		Payload: []byte("{}"),
	})
}
