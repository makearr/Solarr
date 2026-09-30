package app

import (
	"context"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
)

// registerCutoffJob wires M1d's cutoff check: albums whose files fall short of the cutoff become UPGRADABLE.
func (p *process) registerCutoffJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(music.CutoffDefinition); err != nil {
		return err
	}
	executor.Handle(music.CutoffJobType, music.CutoffJob{
		Desired: library.NewDesired(p.library), Settings: p.cache, Logger: p.logger,
	})
	return nil
}

func (p *process) enqueueCutoff(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.queue.Enqueue(ctx, jobs.Request{
		Type: music.CutoffJobType, ActionKey: string(music.CutoffJobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
}
