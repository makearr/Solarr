package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/platform/iogate"
)

// StallPolicy says when a running job counts as hung and how long it then waits before it is started again.
type StallPolicy struct {
	// Floor is the least time without any sign of progress before a job can count as hung; the longer of this and
	// ten times the job's mean time per unit of work applies.
	Floor time.Duration
	// Restart is how long a hung job waits, after it is cancelled, before it runs again.
	Restart time.Duration
}

// Watchdog tells the executor which jobs to watch for hanging.
type Watchdog interface {
	// StallPolicy answers false for a job that is not watched.
	StallPolicy(job Job) (StallPolicy, bool)
}

// stallFactor is how many times its mean time per unit a job may go quiet before it counts as hung.
const stallFactor = 10

// abandonGrace is how long a cancelled, hung handler gets to return before the executor stops waiting for it: a read
// blocked inside a dead network mount cannot be interrupted, and must not hold a worker for ever.
const abandonGrace = 30 * time.Second

// watchEvery is how often a running job is checked.
const watchEvery = 15 * time.Second

// extraStallAttempts is how many times beyond its MaxAttempts a hung job may be started again: hanging is the
// disk's fault, not the job's, but a job that hangs every time must still end.
const extraStallAttempts = 2

// StalledError is the failure of a job cancelled for hanging; it is retried after RestartAfter.
type StalledError struct {
	Type         Type
	Idle         time.Duration
	RestartAfter time.Duration
}

func (e *StalledError) Error() string {
	return fmt.Sprintf("%s made no progress for %s and was cancelled; it restarts in %s", e.Type, e.Idle.Round(time.Second), e.RestartAfter)
}

// Retryable marks the failure as one that may pass.
func (e *StalledError) Retryable() bool { return true }

// RetryAfter is the delay the queue uses instead of its backoff.
func (e *StalledError) RetryAfter() time.Duration { return e.RestartAfter }

// beatingTurns lets the watchdog tell waiting for a disk from hanging on it: the wait does not count, and taking and
// giving back a turn are signs of life.
type beatingTurns struct {
	inner    iogate.Turns
	progress *Progress
}

func (t beatingTurns) AcquirePaths(ctx context.Context, kind string, paths []string) (func(), error) {
	t.progress.setWaiting(true)
	release, err := t.inner.AcquirePaths(ctx, kind, paths)
	t.progress.setWaiting(false)
	if err != nil {
		return nil, err
	}
	return func() {
		release()
		t.progress.Beat()
	}, nil
}
