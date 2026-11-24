package transcoder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/iogate"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// FileJobType converts one file.
const FileJobType = jobs.Type("transcode.file")

// FileDefinition declares the conversion of one file; the pipeline owns its retries, so the queue never repeats it.
var FileDefinition = jobs.Definition{Name: "Converting a file", Type: FileJobType, Pool: jobs.PoolTranscode, Mutating: true, MaxAttempts: 1}

// FilePayload names the file.
type FilePayload struct {
	MediaObjectID int64 `json:"media_object_id"`
}

// EnqueueFile submits the conversion of one file.
func EnqueueFile(ctx context.Context, queue Queue, mediaObjectID, categoryID int64) (jobs.EnqueueResult, error) {
	payload, err := json.Marshal(FilePayload{MediaObjectID: mediaObjectID})
	if err != nil {
		return jobs.EnqueueResult{}, describe("file payload", err)
	}
	return queue.Enqueue(ctx, jobs.Request{
		Type: FileJobType, ActionKey: "transcode:" + strconv.FormatInt(mediaObjectID, 10), Priority: jobs.PriorityLow, Payload: payload, CategoryID: categoryID,
	})
}

// FileHandler adapts the conversion of one file to the job queue.
type FileHandler struct{ Service *Service }

// Run converts the file the payload names, then asks for the next ones.
func (h FileHandler) Run(ctx context.Context, job jobs.Job) error {
	var payload FilePayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return describe("file payload", err)
	}
	err := h.Service.RunFile(ctx, payload.MediaObjectID, job.Progress)
	if h.Service.Queue != nil && ctx.Err() == nil {
		_, _ = EnqueueDispatch(context.WithoutCancel(ctx), h.Service.Queue)
	}
	return err
}

// Tunables of the pipeline.
const (
	// earlyFailure is how far into the file a failure still counts as "this engine cannot do it" rather than an error.
	earlyFailure = 0.05
	laneWait     = 2 * time.Second
	diskFullHold = 10 * time.Minute
	// WorkDirectory is the folder inside a root's recycle bin that holds results until they replace their originals.
	WorkDirectory = ".transcode"
)

// fileRun is everything one conversion reads, loaded once.
type fileRun struct {
	item     library.TranscodeItem
	target   settings.QualityTarget
	stored   settings.StoredTranscodeSetup
	override Override
	root     settings.RootFolder
	relPath  string
	abs      string
	source   transcode.File
	info     fsx.FileInfo
	plan     Plan
	wanted   Languages
	progress *jobs.Progress
}

func (r *fileRun) setup() settings.TranscodeSetup { return r.stored.Setup }

// RunFile converts one file the dispatcher marked running. It returns nil after recording its own outcome; only a
// cancelled context returns an error, which the queue does not count.
func (s *Service) RunFile(ctx context.Context, id int64, progress *jobs.Progress) error {
	item, found, err := s.Items.Get(ctx, id)
	if err != nil || !found || item.State != library.TranscodeRunning {
		return err
	}
	run, proceed, err := s.prepare(ctx, item, progress)
	if err == nil && proceed {
		err = s.convert(ctx, run)
	}
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		if interruptErr := s.interrupted(ctx, item); interruptErr != nil {
			s.Logger.Warn("transcode: record an interrupted conversion", "id", id, "error", interruptErr)
		}
		return ctx.Err()
	}
	s.Logger.Warn("transcode: conversion failed", "id", id, "error", err)
	return s.errored(ctx, item, err)
}

// prepare loads what the conversion needs and settles the item itself when there is nothing to run; proceed is false then.
func (s *Service) prepare(ctx context.Context, item library.TranscodeItem, progress *jobs.Progress) (*fileRun, bool, error) {
	id := item.MediaObjectID
	libraries, err := s.Settings.Libraries(ctx)
	if err != nil {
		return nil, false, err
	}
	var one *settings.TranscodeLibrary
	for _, candidate := range libraries {
		if candidate.CategoryID == item.CategoryID && candidate.Enabled && candidate.Supported() {
			one = &candidate
		}
	}
	if !s.Settings.Global().Enabled || one == nil {
		return nil, false, s.release(ctx, id)
	}
	stored, err := s.Settings.Setup(ctx, one.Target.ID)
	if err != nil {
		return nil, false, err
	}
	run := &fileRun{item: item, target: *one.Target, stored: stored, progress: progress, wanted: WantedLanguages(*one.Target, s.Settings.Languages(item.CategoryID))}
	if item.OverrideJSON != "" {
		if err := json.Unmarshal([]byte(item.OverrideJSON), &run.override); err != nil {
			return nil, false, describe("read the retry settings", err)
		}
	}
	paths, err := s.Items.Paths(ctx, id)
	if err != nil {
		return nil, false, err
	}
	var present []library.ObjectPath
	for _, one := range paths {
		if !one.Missing {
			present = append(present, one)
		}
	}
	switch {
	case len(present) == 0:
		return nil, false, s.blocked(ctx, id, FailureSourceMissing)
	case len(present) > 1:
		return nil, false, s.blocked(ctx, id, FailureLinkedCopies)
	}
	root, known := s.Settings.RootFolder(present[0].RootFolderID)
	if !known || !root.Online || !root.Writable || root.ReadOnly {
		s.blockRoot(present[0].RootFolderID, s.Clock.Now().Add(diskFullHold))
		return nil, false, s.release(ctx, id)
	}
	run.root, run.relPath = root, present[0].RelPath
	run.abs = path.Join(root.Path, run.relPath)
	if run.info, err = s.FS.Stat(run.abs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, s.blocked(ctx, id, FailureSourceMissing)
		}
		return nil, false, err
	}
	if run.source, err = s.probe(ctx, run.abs); err != nil {
		return nil, false, err
	}
	run.plan = Analyze(run.source, run.target, run.setup(), run.wanted)
	if run.plan.Verdict != Needed {
		return nil, false, s.settleVerdict(ctx, run)
	}
	if encoded, err := json.Marshal(run.plan); err == nil {
		reasons, _ := json.Marshal(run.plan.Reasons)
		warnings, _ := json.Marshal(run.plan.Warnings)
		if err := s.Items.SetJudgement(ctx, id, string(reasons), string(encoded), string(warnings)); err != nil {
			return nil, false, err
		}
	}
	if _, free, err := s.FS.DiskUsage(root.Path); err == nil && free-run.info.Size < root.MinFreeBytes {
		s.blockRoot(root.ID, s.Clock.Now().Add(diskFullHold))
		return nil, false, s.release(ctx, id)
	}
	return run, true, nil
}

// probe reads the file's streams on its disk's turn, with the conversion marker an MP4 with a cover keeps outside ffprobe's sight.
func (s *Service) probe(ctx context.Context, absolute string) (transcode.File, error) {
	var file transcode.File
	err := iogate.OnDisk(ctx, "", []string{absolute}, func() (probeErr error) {
		file, probeErr = s.Prober.ProbeStreams(ctx, absolute)
		return probeErr
	})
	if err != nil {
		return transcode.File{}, err
	}
	if file.Tags[MarkerTag] == "" && s.Tagger != nil {
		if marker := s.Tagger.Marker(ctx, absolute); marker != "" {
			tags := map[string]string{}
			for key, value := range file.Tags {
				tags[key] = value
			}
			tags[MarkerTag], file.Tags = marker, tags
		}
	}
	return file, nil
}

// settleVerdict records that the file's own streams say nothing needs doing, or that it cannot be converted.
func (s *Service) settleVerdict(ctx context.Context, run *fileRun) error {
	reasons, _ := json.Marshal(run.plan.Reasons)
	warnings, _ := json.Marshal(run.plan.Warnings)
	if err := s.Items.SetJudgement(ctx, run.item.MediaObjectID, string(reasons), "", string(warnings)); err != nil {
		return err
	}
	to := library.TranscodeNotNeeded
	if run.plan.Verdict == Blocked {
		to = library.TranscodeBlocked
	}
	return s.settle(ctx, run.item.MediaObjectID, to, func(item *library.TranscodeItem) { item.Interrupted, item.Lane = false, "" })
}

// passAt rebuilds the settings of the pass an interrupted item had reached.
func (r *fileRun) passAt() PassSettings {
	pass := FirstPass(r.setup(), r.override)
	for pass.Pass < int(r.item.Pass) {
		next, more := NextPass(r.setup(), r.plan, pass, []Reason{{Code: ReasonResultLarger}})
		if !more {
			break
		}
		pass = next
	}
	return pass
}

// convert runs the passes of the ladder until a result is accepted, refused for good, or an error stops it.
func (s *Service) convert(ctx context.Context, run *fileRun) error {
	engines, err := s.enginesFor(run)
	if err != nil {
		return s.failedWith(ctx, run.item.MediaObjectID, FailureEngineUnavailable, err.Error())
	}
	pass := run.passAt()
	for {
		output := s.outputPath(run, pass.Pass)
		started := s.Clock.Now()
		used, err := s.encode(ctx, run, engines, pass, output)
		if err != nil {
			s.discard(output)
			return err
		}
		outcome, result, err := s.judgeResult(ctx, run, output)
		if err != nil {
			s.discard(output)
			return err
		}
		encodeSeconds := int64(s.Clock.Since(started).Seconds())
		if outcome.Accepted {
			return s.finish(ctx, run, result, outcome, used, pass.Pass, encodeSeconds, output)
		}
		s.discard(output)
		next, laddered := NextPass(run.setup(), run.plan, pass, outcome.Failures)
		state := AfterRefused(itemStateOf(run.item), outcome.Failures, next, laddered)
		if laddered {
			pass = next
			run.item.Pass = int64(state.Pass)
			if err := s.settle(ctx, run.item.MediaObjectID, library.TranscodeRunning, func(item *library.TranscodeItem) { applyState(item, state) }); err != nil {
				return err
			}
			continue
		}
		return s.refused(ctx, run, outcome, state)
	}
}

// enginesFor lists the engines to try; a conversion that encodes no video needs none.
func (s *Service) enginesFor(run *fileRun) ([]transcode.Accel, error) {
	if !run.plan.EncodesVideo {
		return []transcode.Accel{transcode.Software}, nil
	}
	codec, known := transcode.CodecNamed(run.target.VideoCodec)
	if !known {
		return nil, ErrEngineUnavailable
	}
	return ResolveEngines(s.Hardware, EngineQuery{Setup: run.setup(), Requested: run.override.Engine, Codec: codec, TenBit: run.plan.tenBitVideo()})
}

func (s *Service) outputPath(run *fileRun, pass int) string {
	name := fmt.Sprintf("%d.%d.%s", run.item.MediaObjectID, pass, extensionOf(run.plan.Container))
	return path.Join(run.root.Path, library.RecycleBinDirectory, WorkDirectory, name)
}

// extensionOf is the file extension of a container name.
func extensionOf(container string) string {
	if container == "matroska" {
		return "mkv"
	}
	return strings.ToLower(container)
}

func (s *Service) discard(output string) { _ = s.FS.Remove(output) }

// acquireLane waits for a place in the lane, showing signs of life while it does.
func (s *Service) acquireLane(ctx context.Context, lane Lane, progress *jobs.Progress) (func(), error) {
	for {
		if release, ok := s.Slots.Acquire(lane); ok {
			return release, nil
		}
		progress.Beat()
		if err := s.Clock.Sleep(ctx, laneWait); err != nil {
			return nil, err
		}
	}
}

// encode writes the pass's output with the first engine step that works; a step that fails early hands over to the next.
func (s *Service) encode(ctx context.Context, run *fileRun, engines []transcode.Accel, pass PassSettings, output string) (transcode.Accel, error) {
	if err := s.FS.MkdirAll(path.Dir(output), 0o755); err != nil {
		return "", err
	}
	var last error
	for _, engine := range engines {
		lane := run.plan.Lane(engine)
		release, err := s.acquireLane(ctx, lane, run.progress)
		if err != nil {
			return "", err
		}
		err = s.encodeOn(ctx, run, engine, lane, pass, output)
		release()
		if err == nil {
			return engine, nil
		}
		var early *earlyError
		if !errors.As(err, &early) || ctx.Err() != nil {
			return "", err
		}
		s.discard(output)
		last = err
	}
	return "", last
}

// earlyError is a step that failed before it got anywhere: the next engine gets the file.
type earlyError struct{ cause error }

func (e *earlyError) Error() string {
	return "transcoder: the engine failed at the start: " + e.cause.Error()
}
func (e *earlyError) Unwrap() error { return e.cause }

// encodeOn runs ffmpeg once per step the engine offers: frames kept on the GPU, then decoded on the CPU.
func (s *Service) encodeOn(ctx context.Context, run *fileRun, engine transcode.Accel, lane Lane, pass PassSettings, output string) error {
	support := transcode.EncoderSupport{Engine: engine}
	if run.plan.EncodesVideo {
		codec, _ := transcode.CodecNamed(run.target.VideoCodec)
		support, _ = s.Hardware.EncoderFor(engine, codec)
		support.Engine, support.Codec = engine, codec
	}
	marker := transcode.MarkerValue(run.target.Name, run.stored.Revision, pass.Pass, s.Clock.Now().UTC().Format("2006-01-02"))
	build := func(fullGPU bool) transcode.ConvertSpec {
		return BuildSpec(BuildInput{
			File: run.source, Plan: run.plan, Target: run.target, Setup: run.setup(), Pass: pass, Marker: marker, Encoder: support,
			Device: s.device(engine), FullGPU: fullGPU, Tolerant: run.item.Attempts > 0, Input: run.abs, Output: output,
		})
	}
	specs := []transcode.ConvertSpec{build(false)}
	if engine != transcode.Software && transcode.CanDecodeOnGPU(engine) && allOnGPU(engine, specs[0]) {
		specs = []transcode.ConvertSpec{build(true), specs[0]}
	}
	var last error
	for _, spec := range specs {
		err := s.runFFmpeg(ctx, run, spec, lane)
		if err == nil {
			return s.carryTags(ctx, spec, output)
		}
		var early *earlyError
		if !errors.As(err, &early) || ctx.Err() != nil {
			return err
		}
		s.discard(output)
		last = err
	}
	return last
}

func (s *Service) device(engine transcode.Accel) string {
	if s.Devices == nil {
		return ""
	}
	return s.Devices(engine)
}

// allOnGPU says every encoded video stream has GPU forms of its filters.
func allOnGPU(engine transcode.Accel, spec transcode.ConvertSpec) bool {
	for _, video := range spec.Video {
		if !video.Copy && !transcode.CanRunOnGPU(engine, video) {
			return false
		}
	}
	return len(spec.Video) > 0
}

// runFFmpeg starts one spec and waits for it, reporting progress; a failure in the first 5 % is an earlyError.
func (s *Service) runFFmpeg(ctx context.Context, run *fileRun, spec transcode.ConvertSpec, lane Lane) error {
	args, err := transcode.ConvertArgs(spec)
	if err != nil {
		return describe("build the command", err)
	}
	total := int64(run.source.Duration.Seconds())
	var reached atomic.Int64
	process, err := s.Starter.Start(ctx, args, func(report transcode.Progress) {
		reached.Store(int64(report.OutTime))
		run.progress.Set(int64(report.OutTime.Seconds()), total)
	}, s.ScratchDir)
	if err != nil {
		return &earlyError{cause: err}
	}
	untrack := s.Slots.Track(lane, process)
	err = process.Wait()
	untrack()
	if err == nil {
		return nil
	}
	if ctx.Err() == nil && float64(reached.Load()) < earlyFailure*float64(run.source.Duration) {
		return &earlyError{cause: err}
	}
	return err
}

// carryTags writes the custom tags and the marker an MP4 that holds a cover lost on the way (ffmpeg keeps a cover or custom tags, not both).
func (s *Service) carryTags(ctx context.Context, spec transcode.ConvertSpec, output string) error {
	if spec.CustomTags || s.Tagger == nil {
		return nil
	}
	if flags := strings.ToLower(spec.Container); flags != "mp4" && flags != "m4a" && flags != "m4v" {
		return nil
	}
	tags := map[string]string{MarkerTag: spec.Marker}
	for key, value := range spec.Tags {
		if name := strings.ToLower(key); strings.HasPrefix(name, "musicbrainz") || strings.HasPrefix(name, "acoustid") {
			tags[key] = value
		}
	}
	return s.Tagger.WriteTags(ctx, output, tags)
}

// judgeResult probes the output and applies the setup's checks.
func (s *Service) judgeResult(ctx context.Context, run *fileRun, output string) (Outcome, transcode.File, error) {
	var result transcode.File
	err := iogate.OnDisk(ctx, "", []string{output}, func() (probeErr error) {
		result, probeErr = s.Prober.ProbeStreams(ctx, output)
		return probeErr
	})
	if err != nil {
		// A result nobody can read is a refused result, not a broken system.
		return Outcome{Failures: []Reason{{Code: ReasonUnreadable, Stream: -1}}}, transcode.File{}, nil //nolint:nilerr // an unreadable result is refused, not an error
	}
	if info, statErr := s.FS.Stat(output); statErr == nil && result.SizeBytes == 0 {
		result.SizeBytes = info.Size
	}
	original := run.source
	if original.SizeBytes == 0 {
		original.SizeBytes = run.info.Size
	}
	return Check(original, result, run.target, run.setup(), run.plan), result, nil
}

// refused records a result the checks turned down for good: failed, or another attempt.
func (s *Service) refused(ctx context.Context, run *fileRun, outcome Outcome, state ItemState) error {
	reasons, _ := json.Marshal(outcome.Failures)
	plan, _ := json.Marshal(run.plan)
	warnings, _ := json.Marshal(outcome.Warnings)
	if err := s.Items.SetJudgement(ctx, run.item.MediaObjectID, string(reasons), string(plan), string(warnings)); err != nil {
		return err
	}
	if err := s.succeeded(ctx, run.item.Kind); err != nil {
		return err
	}
	return s.settle(ctx, run.item.MediaObjectID, library.TranscodeState(state.State), func(item *library.TranscodeItem) {
		applyState(item, state)
		item.LastError = string(outcome.Failures[0].Code)
	})
}
