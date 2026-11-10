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
	"github.com/makearr/Solarr/internal/platform/filekind"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/iogate"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/platform/previewstore"
)

// JobType is the registered type of the background probe sweep.
const JobType = jobs.Type("library.probe")

// ActionKey deduplicates the sweep: global, not per-root, since it walks
// every root's unmeasured files in one pass (Invariant 16).
const ActionKey = "library.probe"

// Definition declares how probing behaves: background, lowest priority,
// retried on transient failure, never blocking anything a user is waiting on.
var Definition = jobs.Definition{
	Name: "Reading file details and making video pictures",
	Type: JobType, Pool: jobs.PoolFiles, Mutating: true, MaxAttempts: 3,
}

// batchSize bounds how many unmeasured files one pass asks the store for
// before probing them and checking in with ListUnmeasured again -- the same
// crash-safety reasoning as EnrichJob's own batch: each batch's writes are
// already their own transaction per file, so a shutdown mid-run loses at most
// one batch's worth of otherwise-committed progress.
const batchSize = 100

// yieldAfter is how long a sweep runs before it gives up its slot to jobs waiting in the pool: a first sweep of a
// 200,000-file library held it for hours while download polling and rescans waited (found 2026-09-28). With nothing
// waiting it keeps going, so the run is one honest task rather than a new one every two minutes.
const yieldAfter = 2 * time.Minute

// shouldYield is whether the sweep, running since started, must make way for another job.
func (j *Job) shouldYield(ctx context.Context, started time.Time) bool {
	if j.clock.Now().Sub(started) < yieldAfter {
		return false
	}
	return j.waiting == nil || j.waiting(ctx)
}

// Roots resolves a root folder by id. They are settings.db policy (ADR-25),
// so the job asks rather than reading the table -- the same interface
// internal/scanning/scan already asks of its own Roots dependency.
type Roots interface {
	RootFolder(id int64) (settings.RootFolder, bool)
}

// Job probes every media_object with no measured half yet, in bounded,
// resumable batches, until none remain or the context is cancelled.
type Job struct {
	physical    library.Physical
	coverage    library.ProbeCoverage
	roots       Roots
	tool        mediatool.Tool
	fs          fsx.FS
	hashing     func() bool
	previews    func() bool
	previewsDir string
	clock       clock.Clock
	logger      *slog.Logger
	waiting     func(ctx context.Context) bool
	// forced is the pictures a person asked for from Settings (a PicturesMode), made whatever the settings say.
	forced atomic.Value
	// resumeAt is each list's place when a run yielded, so the continuation does not re-read the files that failed first.
	resumeAt [7]atomic.Int64
}

// Config is what a Job needs. Every field is required except Logger.
type Config struct {
	Physical library.Physical
	Coverage library.ProbeCoverage
	Roots    Roots
	Tool     mediatool.Tool
	// FS lists the folder of a video for subtitle files beside it; nil leaves those out.
	FS fsx.FS
	// PerceptualHashing says whether videos are hashed this run; nil means always.
	PerceptualHashing func() bool
	// VideoPreviews says whether the hashing also keeps a single frame and a sheet of each video in PreviewsDir; nil means never.
	VideoPreviews func() bool
	PreviewsDir   string
	Clock         clock.Clock
	Logger        *slog.Logger
	// OthersWaiting says whether other jobs of the pool are queued behind this one; nil means always, so the sweep yields as it always did.
	OthersWaiting func(ctx context.Context) bool
}

// NewJob builds the handler.
func NewJob(config Config) *Job {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Job{
		physical: config.Physical, coverage: config.Coverage, roots: config.Roots,
		tool: config.Tool, fs: config.FS, hashing: config.PerceptualHashing, previews: config.VideoPreviews, previewsDir: config.PreviewsDir, clock: config.Clock, logger: logger, waiting: config.OthersWaiting,
	}
}

// Run implements jobs.Handler. There is no persisted cursor across runs --
// only within one, so a file that fails its probe (a missing root, a
// corrupted file) does not spin the batch forever; the next scheduled run
// (there is no scheduler yet, so today that means the next process restart)
// simply asks the same question again.
func (j *Job) Run(ctx context.Context, request jobs.Job) error {
	started := j.clock.Now()
	total, failed := 0, 0
	forced, _ := j.forced.Load().(PicturesMode)
	previews := (j.previews != nil && j.previews() && j.previewsDir != "") || (forced == PicturesWithPreviews && j.previewsDir != "")
	hashing := j.hashing == nil || j.hashing() || previews || forced != ""
	hasher, canHash := j.tool.(PerceptualHasher)
	hashPictures := canHash && hashing
	if remaining, err := j.physical.CountProbeWork(ctx, hashPictures, previews); err == nil {
		request.Progress.Set(0, remaining)
	} else {
		j.logger.WarnContext(ctx, "count the probe work failed", slog.Any("error", err))
	}
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
			request.Progress.Add(int64(len(batch)))
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
			if j.shouldYield(ctx, started) {
				j.resumeAt[index].Store(afterID)
				j.logger.Info("probe sweep yielding", slog.Int("processed", total), slog.Int("failed", failed))
				return fmt.Errorf("probe: %d files in %s: %w", total, yieldAfter, jobs.ErrYield)
			}
		}
	}
	if err := j.backfill(ctx, started, oshashPass, "oshash", j.physical.ListVideoWithoutOshash, j.hashFile, request.Progress); err != nil {
		return err
	}
	if hashPictures {
		list := func(ctx context.Context, afterID, pageSize int64) ([]library.UnmeasuredFile, error) {
			return j.physical.ListVideoWithoutHashOrPreview(ctx, afterID, pageSize, previews)
		}
		err := j.backfill(ctx, started, phashPass, "phash", list,
			func(ctx context.Context, file library.UnmeasuredFile) error {
				return j.perceptualHash(ctx, hasher, file, previews)
			}, request.Progress)
		if err != nil {
			return err
		}
		j.forced.Store(PicturesMode(""))
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

// oshashPass and phashPass index resumeAt for the video hash backfills, after the three probe lists.
const (
	oshashPass = 3
	phashPass  = 4
)

// PerceptualHasher is the tool's frame hash (ADR-35); a Tool without it leaves video unhashed.
type PerceptualHasher interface {
	PerceptualHash(ctx context.Context, path string) (mediatool.VideoHash, error)
}

// PreviewMaker is a hasher that can also make the preview pictures from the frames it reads.
type PreviewMaker interface {
	HashAndPreview(ctx context.Context, path string, previews bool) (mediatool.VideoHash, mediatool.Preview, error)
}

// backfill hashes the video files a pass still lists (the hash was not written at scan time); a file that cannot
// be read stays unhashed and is asked again next run.
func (j *Job) backfill(
	ctx context.Context, started time.Time, pass int, name string,
	list func(context.Context, int64, int64) ([]library.UnmeasuredFile, error),
	hash func(context.Context, library.UnmeasuredFile) error, progress *jobs.Progress,
) error {
	if j.fs == nil {
		return nil
	}
	afterID := j.resumeAt[pass].Swap(0)
	hashed := 0
	for {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("probe.context", string(JobType), err)
		}
		batch, err := list(ctx, afterID, batchSize)
		if err != nil {
			return fmt.Errorf("probe: %w", err)
		}
		for _, file := range batch {
			// A shutdown must release the job, not finish it with the files it cut short still unhashed.
			if err := ctx.Err(); err != nil {
				return jobs.Retryablef("probe.context", string(JobType), err)
			}
			progress.Add(1)
			if err := hash(ctx, file); err != nil {
				j.logger.WarnContext(ctx, name+" failed", slog.Int64("media_object_id", file.MediaObjectID), slog.Any("error", err))
			} else {
				hashed++
			}
			if j.shouldYield(ctx, started) {
				j.resumeAt[pass].Store(file.MediaObjectID)
				return fmt.Errorf("probe: %d %s hashes in %s: %w", hashed, name, yieldAfter, jobs.ErrYield)
			}
		}
		if len(batch) < batchSize {
			return nil
		}
		afterID = batch[len(batch)-1].MediaObjectID
	}
}

func (j *Job) perceptualHash(ctx context.Context, hasher PerceptualHasher, file library.UnmeasuredFile, previews bool) error {
	root, found := j.roots.RootFolder(file.RootFolderID)
	if !found {
		return fmt.Errorf("root folder %d is not configured", file.RootFolderID)
	}
	absolute := path.Join(root.Path, file.RelPath)
	var hash mediatool.VideoHash
	var preview mediatool.Preview
	err := iogate.OnDisk(ctx, "", []string{absolute}, func() (readErr error) {
		if maker, ok := hasher.(PreviewMaker); ok && previews {
			hash, preview, readErr = maker.HashAndPreview(ctx, absolute, true)
		} else {
			hash, readErr = hasher.PerceptualHash(ctx, absolute)
		}
		return readErr
	})
	if err != nil {
		return err
	}
	frames := make([]library.FrameHash, len(hash.Frames))
	for index, frame := range hash.Frames {
		frames[index] = library.FrameHash{Slot: frame.Slot, Hash: frame.Hash}
	}
	if err := j.physical.SetPerceptualHash(ctx, file.MediaObjectID, hash.Mean, frames); err != nil {
		return err
	}
	if !previews {
		return nil
	}
	return j.keepPreview(ctx, file.MediaObjectID, preview)
}

// keepPreview writes the pictures into the previews folder, then records which exist.
func (j *Job) keepPreview(ctx context.Context, mediaObjectID int64, preview mediatool.Preview) error {
	if err := j.fs.MkdirAll(previewstore.Folder(j.previewsDir, mediaObjectID), 0o755); err != nil {
		return err
	}
	for kind, data := range map[string][]byte{previewstore.Frame: preview.Frame, previewstore.Sheet: preview.Sheet} {
		if len(data) == 0 {
			continue
		}
		if err := j.writePreview(previewstore.Path(j.previewsDir, mediaObjectID, kind), data); err != nil {
			return err
		}
	}
	return j.physical.SetVideoPreview(ctx, mediaObjectID, len(preview.Frame) > 0, len(preview.Sheet) > 0, j.clock.Now().Unix())
}

// writePreview lands a picture whole or not at all, so a reader never sees half of one.
func (j *Job) writePreview(target string, data []byte) error {
	partial := target + ".partial"
	out, err := j.fs.Create(partial)
	if err != nil {
		return err
	}
	if _, err := out.Write(data); err != nil {
		_ = out.Close()
		_ = j.fs.Remove(partial)
		return err
	}
	if err := out.Close(); err != nil {
		_ = j.fs.Remove(partial)
		return err
	}
	return j.fs.Rename(partial, target)
}

func (j *Job) hashFile(ctx context.Context, file library.UnmeasuredFile) error {
	root, found := j.roots.RootFolder(file.RootFolderID)
	if !found {
		return fmt.Errorf("root folder %d is not configured", file.RootFolderID)
	}
	absolute := path.Join(root.Path, file.RelPath)
	var oshash string
	err := iogate.OnDisk(ctx, "", []string{absolute}, func() error {
		info, statErr := j.fs.Stat(absolute)
		if statErr != nil {
			return statErr
		}
		var hashErr error
		oshash, hashErr = library.OSHash(j.fs, absolute, info.Size)
		return hashErr
	})
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
		// ffprobe cannot read an ebook, and asking again every run only fills the log.
		if filekind.Of(file.RelPath) == filekind.Book {
			continue
		}
		root, found := j.roots.RootFolder(file.RootFolderID)
		if !found {
			failures = append(failures, failedFile{file, fmt.Errorf("root folder %d is not configured", file.RootFolderID)})
			continue
		}
		paths = append(paths, path.Join(root.Path, file.RelPath))
		asked = append(asked, file)
	}
	var probes []mediatool.Probe
	err := iogate.OnDisk(ctx, "", paths, func() (readErr error) {
		probes, readErr = j.tool.ReadTags(ctx, paths)
		return readErr
	})
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

// PicturesJobType is the old on-demand sweep behind Settings, General's "generate" buttons. Those buttons now ask the
// probe sweep to make the pictures (RequestPictures), so reading file details and making pictures are one task and never
// run side by side; the type stays registered for jobs queued before.
const PicturesJobType = jobs.Type("library.video_pictures")

// PicturesDefinition declares the old sweep: the same pool as the probe.
var PicturesDefinition = jobs.Definition{
	Type: PicturesJobType, Pool: jobs.PoolFiles, Mutating: true, MaxAttempts: 3,
}

// PicturesMode is what an on-demand request makes; both hash, whatever the settings say.
type PicturesMode string

// The two on-demand requests.
const (
	PicturesWithPreviews PicturesMode = "previews"
	PicturesHashesOnly   PicturesMode = "hashes"
)

// PicturesJob hands a job queued under the old type over to the one sweep, which runs it instead; running it here too
// would be a second copy of the sweep beside the first.
type PicturesJob struct {
	job   *Job
	queue *jobs.Queue
}

// NewPicturesJob wraps a probe job and the queue its sweep is submitted to.
func NewPicturesJob(job *Job, queue *jobs.Queue) *PicturesJob {
	return &PicturesJob{job: job, queue: queue}
}

// Run implements jobs.Handler.
func (p *PicturesJob) Run(ctx context.Context, request jobs.Job) error {
	var payload struct {
		Mode PicturesMode `json:"mode"`
	}
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		return fmt.Errorf("probe: pictures payload: %w", err)
	}
	_, err := p.job.RequestPictures(ctx, p.queue, payload.Mode)
	return err
}

// RequestPictures makes the next (or the running) sweep hash and, for previews, picture every video still lacking them,
// whatever the settings say, and queues the sweep; one already queued or running takes the request up itself.
func (j *Job) RequestPictures(ctx context.Context, queue *jobs.Queue, mode PicturesMode) (jobs.EnqueueResult, error) {
	if err := j.Force(mode); err != nil {
		return jobs.EnqueueResult{}, err
	}
	return Enqueue(ctx, queue)
}

// Force makes the next sweep make the pictures of mode, whatever the settings say.
func (j *Job) Force(mode PicturesMode) error {
	if mode == PicturesWithPreviews && j.previewsDir == "" {
		return fmt.Errorf("probe: no previews folder is configured")
	}
	j.forced.Store(mode)
	return nil
}
