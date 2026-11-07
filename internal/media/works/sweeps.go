package works

import (
	"context"

	"github.com/makearr/Solarr/internal/jobs"
)

// RefreshDefinition declares a type's refresh sweep: background metadata work, retried on transient failure.
func RefreshDefinition(name string, jobType jobs.Type) jobs.Definition {
	return jobs.Definition{Name: name, Type: jobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3}
}

// ArtworkDefinition declares a type's picture sweep: background metadata work, resumed by the next run.
func ArtworkDefinition(name string, jobType jobs.Type) jobs.Definition {
	return jobs.Definition{Name: name, Type: jobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 1}
}

// EnqueueRefresh submits one refresh sweep, one at a time (Invariant 16).
func EnqueueRefresh(ctx context.Context, queue *jobs.Queue, jobType jobs.Type) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: jobType, ActionKey: string(jobType), Priority: jobs.PriorityLow, Payload: []byte("{}")})
}

// EnqueueArtwork submits one picture sweep, one at a time.
func EnqueueArtwork(ctx context.Context, queue *jobs.Queue, jobType jobs.Type) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: jobType, ActionKey: string(jobType), Priority: jobs.PriorityLow})
}
