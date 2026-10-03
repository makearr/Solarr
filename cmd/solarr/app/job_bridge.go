package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/tv"
)

// registerBridgeJob wires the sweep that recognises the same human in TV and music.
func (p *process) registerBridgeJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(tv.BridgeDefinition); err != nil {
		return err
	}
	executor.Handle(tv.BridgeJobType, tv.NewBridgeJob(tv.BridgeConfig{People: library.NewPeople(p.library), Source: p.tmdbShared, Logger: p.logger}))
	return nil
}

// enqueueBridge submits one bridge sweep; registered as the "Match people across libraries" task.
func (p *process) enqueueBridge(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := tv.EnqueueBridge(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue people bridge", slog.Any("error", err))
	}
	return result, err
}
