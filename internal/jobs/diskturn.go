package jobs

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/iogate"
)

// DiskGate lets a job that reads or writes a disk wait for its turn on it.
type DiskGate interface {
	// Acquire is the job's turn for its whole run; the release is a no-op for a job that takes turns per batch.
	Acquire(ctx context.Context, job Job) (release func(), err error)
	// TurnsFor is what the job's code uses, through iogate.OnDisk, to take one batch's turn.
	TurnsFor(job Job) iogate.Turns
}
