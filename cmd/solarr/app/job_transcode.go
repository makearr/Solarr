package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

// transcodeScheduleCron is how often the dispatcher looks for work; it also refills lanes after every conversion.
const transcodeScheduleCron = "*/5 * * * *"

// transcodeService is the conversion pipeline, built once so its lane slots are shared.
func (p *process) transcodeService() *transcoder.Service {
	p.transcodeOnce.Do(func() {
		p.pipeline = &transcoder.Service{
			Items: library.NewTranscodeItems(p.library, clock.NewSystem()), Settings: transcodeSettings{cache: p.cache},
			Hardware: p.transcoder, Queue: p.queue, Clock: clock.NewSystem(), Logger: p.logger,
		}
	})
	return p.pipeline
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
	for _, definition := range []jobs.Definition{transcoder.AnalyzeDefinition, transcoder.DispatchDefinition} {
		if err := registry.Register(definition); err != nil {
			return err
		}
	}
	executor.Handle(transcoder.AnalyzeJobType, transcoder.AnalyzeHandler{Service: service})
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
