package app

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/platform/comicrepack"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/platform/transcode"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/events"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

// transcodeScheduleCron is how often the dispatcher looks for work; it also refills lanes after every conversion.
const transcodeScheduleCron = "*/5 * * * *"

// transcodeService is the conversion pipeline, built once so its lane slots are shared.
func (p *process) transcodeService() *transcoder.Service {
	p.transcodeOnce.Do(func() {
		cl := clock.NewSystem()
		snapshot := func() settings.TranscodeGlobal { return p.cache.Snapshot().TranscodeGlobal() }
		p.pipeline = &transcoder.Service{
			Items: library.NewTranscodeItems(p.library, cl), Settings: transcodeSettings{cache: p.cache}, Hardware: p.transcoder,
			Devices: p.transcoder.DeviceFor, Prober: p.transcoder, Tagger: transcodeTagger{tool: p.tool}, Comics: transcodeComics{fsys: p.fsys},
			Bin:      transcodeBin{RecycleBin: library.NewRecycleBin(p.library, p.fsys, cl).WithOnline(p.rootOnline)},
			Reporter: transcodeReporter{logger: p.logger, stats: statistics.NewTranscodes(p.statistics), history: statistics.NewHistory(p.statistics), bus: p.bus},
			Starter:  transcode.ExecStarter{Nice: mediatool.Nice{Level: 19}, IONice: mediatool.IONice{Class: 3}, Clock: cl},
			Slots: transcoder.NewSlots(func(playing bool) map[transcoder.Lane]int {
				global := snapshot()
				lanes := global.Parallel
				if playing {
					lanes = global.PlaybackParallel
				}
				return map[transcoder.Lane]int{transcoder.LaneCPU: int(lanes.CPU), transcoder.LaneGPU: int(lanes.GPU), transcoder.LaneAudio: int(lanes.Audio)}
			}, p.transcoder.Active, cl),
			Queue: p.queue, FS: p.fsys, Clock: cl, Logger: p.logger, ScratchDir: filepath.Join(p.config.Paths.CacheDir, "transcode-work"),
			SafeMode: p.config.SafeMode,
		}
	})
	return p.pipeline
}

// transcodeComics reads a comic archive's pages for the conversion's analysis.
type transcodeComics struct{ fsys fsx.FS }

func (c transcodeComics) ProbeComic(ctx context.Context, absolute string) (transcoder.ComicFacts, error) {
	pages, err := comicrepack.Measure(ctx, c.fsys, absolute)
	if err != nil {
		return transcoder.ComicFacts{}, err
	}
	return transcoder.ComicFacts{Pages: pages.Count, MedianLong: pages.MedianLong, MedianShort: pages.MedianShort, ImageFormat: pages.Format}, nil
}

// sizeText writes a byte count as the file lists do: decimal units, one decimal under ten.
func sizeText(bytes int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value, unit := float64(bytes), 0
	for value >= 1000 && unit < len(units)-1 {
		value /= 1000
		unit++
	}
	if value >= 10 || unit == 0 {
		return fmt.Sprintf("%.0f %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

// transcodeBin adds the lookup by original path to the recycle bin.
type transcodeBin struct{ library.RecycleBin }

func (b transcodeBin) Find(ctx context.Context, originalPath string) (library.Entry, bool, error) {
	entries, _, _, err := b.List(ctx)
	if err != nil {
		return library.Entry{}, false, err
	}
	var found library.Entry
	for _, entry := range entries {
		if entry.OriginalPath == originalPath && entry.RestoredAt == nil && entry.ID > found.ID {
			found = entry
		}
	}
	return found, found.ID != 0, nil
}

// transcodeTagger writes and reads the freeform tags ffmpeg drops from an MP4 that holds a cover.
type transcodeTagger struct{ tool mediatool.Tool }

func (t transcodeTagger) WriteTags(ctx context.Context, file string, tags map[string]string) error {
	results, err := t.tool.WriteTags(ctx, []mediatool.TagEdit{{Path: file, Set: mediatool.Tags(tags)}})
	if err != nil {
		return err
	}
	if len(results) == 1 {
		return results[0].Err
	}
	return nil
}

func (t transcodeTagger) Marker(ctx context.Context, file string) string {
	probes, err := t.tool.ReadTags(ctx, []string{file})
	if err != nil || len(probes) != 1 {
		return ""
	}
	return probes[0].Tags.First(transcoder.MarkerTag)
}

// transcodeReporter logs every finished conversion and adds it to the savings statistics.
type transcodeReporter struct {
	logger  *slog.Logger
	stats   statistics.Transcodes
	history statistics.History
	bus     *events.Bus
}

// transcodeHistoryKind is the history row of a finished conversion; the download kinds carry "download.".
const transcodeHistoryKind = "file.transcoded"

func (r transcodeReporter) Converted(ctx context.Context, converted transcoder.Converted) {
	r.logger.InfoContext(ctx, "file converted", slog.Int64("id", converted.MediaObjectID), slog.String("engine", converted.Engine),
		slog.Int64("from_bytes", converted.OriginalBytes), slog.Int64("to_bytes", converted.ResultBytes), slog.Int64("seconds", converted.EncodeSeconds))
	err := r.stats.Record(ctx, statistics.DayOf(converted.At), statistics.TranscodeDelta{
		CategoryID: converted.CategoryID, Kind: converted.Kind, Files: 1, OriginalBytes: converted.OriginalBytes,
		ResultBytes: converted.ResultBytes, EncodeSeconds: converted.EncodeSeconds,
	})
	if err != nil {
		r.logger.WarnContext(ctx, "record conversion statistics", slog.Any("error", err))
	}
	saved := converted.OriginalBytes - converted.ResultBytes
	_, err = r.history.Append(ctx, statistics.HistoryEntry{
		At: converted.At.Unix(), Kind: transcodeHistoryKind, CategoryID: converted.CategoryID,
		ReleaseTitle: path.Base(converted.Path), Detail: fmt.Sprintf("%s, %s saved", converted.Engine, sizeText(saved)),
	})
	if err != nil {
		r.logger.WarnContext(ctx, "record conversion history", slog.Any("error", err))
	}
	r.bus.Publish(events.Event{
		Topic: events.TopicLibrary, Entity: fmt.Sprintf("transcode:%d", converted.MediaObjectID), CategoryID: converted.CategoryID,
		Kind: "converted", Data: map[string]int64{"original_bytes": converted.OriginalBytes, "result_bytes": converted.ResultBytes},
	})
}

// Reverted takes a restored conversion out of the savings with the same figures negated.
func (r transcodeReporter) Reverted(ctx context.Context, converted transcoder.Converted) {
	err := r.stats.Record(ctx, statistics.DayOf(converted.At), statistics.TranscodeDelta{
		CategoryID: converted.CategoryID, Kind: converted.Kind, Files: -1, OriginalBytes: -converted.OriginalBytes,
		ResultBytes: -converted.ResultBytes, EncodeSeconds: -converted.EncodeSeconds,
	})
	if err != nil {
		r.logger.WarnContext(ctx, "record restored conversion statistics", slog.Any("error", err))
	}
	r.bus.Publish(events.Event{
		Topic: events.TopicLibrary, Entity: fmt.Sprintf("transcode:%d", converted.MediaObjectID), CategoryID: converted.CategoryID, Kind: "restored",
	})
}

// transcodeSettings is the pipeline's view of the settings cache.
type transcodeSettings struct{ cache *settings.Cache }

func (s transcodeSettings) Global() settings.TranscodeGlobal {
	return s.cache.Snapshot().TranscodeGlobal()
}

func (s transcodeSettings) Libraries(ctx context.Context) ([]settings.TranscodeLibrary, error) {
	return s.cache.TranscodeLibraries(ctx)
}

func (s transcodeSettings) Setup(ctx context.Context, targetID int64) (settings.StoredTranscodeSetup, error) {
	return s.cache.TranscodeSetup(ctx, targetID)
}

func (s transcodeSettings) RootFolder(id int64) (settings.RootFolder, bool) {
	return s.cache.Snapshot().RootFolder(id)
}

func (s transcodeSettings) Roots() []settings.RootFolder { return s.cache.Snapshot().RootFolders() }

func (s transcodeSettings) Retention(categoryID int64) time.Duration {
	return adapters.RecyclingFor(adapters.Categories{Cache: s.cache}, []int64{categoryID}).Retain
}

func (s transcodeSettings) RootFolders(categoryID int64) []settings.RootFolder {
	return s.cache.Snapshot().RootFoldersForCategory(categoryID)
}

// Languages are the library's language profile, else the instance default for its kind.
func (s transcodeSettings) Languages(categoryID int64) []string {
	snapshot := s.cache.Snapshot()
	category, found := snapshot.Category(categoryID)
	if !found {
		return nil
	}
	if category.LanguageProfileID != nil {
		if profile, known := snapshot.LanguageProfile(*category.LanguageProfileID); known {
			return profile.AudioPreferred
		}
	}
	return settings.LanguageProfileFor(snapshot.DefaultLanguages(), category.Kind == "anime").AudioPreferred
}

// registerTranscodeJobs wires the analysis and the dispatcher.
func (p *process) registerTranscodeJobs(registry *jobs.Registry, executor *jobs.Executor) error {
	service := p.transcodeService()
	for _, definition := range []jobs.Definition{transcoder.AnalyzeDefinition, transcoder.DispatchDefinition, transcoder.FileDefinition} {
		if err := registry.Register(definition); err != nil {
			return err
		}
	}
	executor.Handle(transcoder.AnalyzeJobType, transcoder.AnalyzeHandler{Service: service})
	executor.Handle(transcoder.FileJobType, transcoder.FileHandler{Service: service})
	executor.Handle(transcoder.DispatchJobType, transcoder.DispatchHandler{Service: service})
	return nil
}

// enqueueTranscode submits a dispatch; registered as a task, so it runs every few minutes.
func (p *process) enqueueTranscode(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := transcoder.EnqueueDispatch(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue transcode dispatch", slog.Any("error", err))
	}
	return result, err
}
