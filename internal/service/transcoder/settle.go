package transcoder

import (
	"context"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

// The failure codes an item stops with that are not a check's reason.
const (
	FailureEngineUnavailable = "engine_unavailable"
	FailurePathTaken         = "path_taken"
	FailureSourceMissing     = "source_missing"
	FailureLinkedCopies      = string(ReasonLinkedCopies)
)

// maxLastError keeps an item's last error to a line the interface can show.
const maxLastError = 300

func itemStateOf(item library.TranscodeItem) ItemState {
	return ItemState{State: string(item.State), Attempts: int(item.Attempts), Pass: int(item.Pass), Interrupted: item.Interrupted, FailureCode: item.FailureCode}
}

func applyState(item *library.TranscodeItem, state ItemState) {
	item.Attempts, item.Pass, item.Interrupted, item.FailureCode = int64(state.Attempts), int64(state.Pass), state.Interrupted, state.FailureCode
}

func healthOf(stored library.TranscodeHealth) Health {
	health := Health{
		ConsecutiveFailures: int(stored.ConsecutiveFailures), Mode: HealthMode(stored.Mode), ProbesLeft: int(stored.ProbesLeft),
		BackoffStep: int(stored.BackoffStep), LastFailedID: stored.LastFailedID,
	}
	if stored.BackoffUntil != 0 {
		health.BackoffUntil = time.Unix(stored.BackoffUntil, 0)
	}
	return health
}

func storedHealth(kind string, health Health, runSeq int64, lastError string) library.TranscodeHealth {
	stored := library.TranscodeHealth{
		Kind: kind, ConsecutiveFailures: int64(health.ConsecutiveFailures), Mode: string(health.Mode), ProbesLeft: int64(health.ProbesLeft),
		BackoffStep: int64(health.BackoffStep), LastFailedID: health.LastFailedID, RunSeq: runSeq, LastError: lastError,
	}
	if !health.BackoffUntil.IsZero() {
		stored.BackoffUntil = health.BackoffUntil.Unix()
	}
	return stored
}

// settle moves a running item on; the write outlives a cancelled run, so a shutdown still records where the item stands.
func (s *Service) settle(ctx context.Context, id int64, to library.TranscodeState, mutate func(*library.TranscodeItem)) error {
	return s.Items.Transition(context.WithoutCancel(ctx), id, library.TranscodeRunning, to, mutate)
}

// release puts a running item back untouched: the switch went off, or the file has to wait.
func (s *Service) release(ctx context.Context, id int64) error {
	return s.settle(ctx, id, library.TranscodeNeeded, nil)
}

// interrupted is a run the process ended: the item goes first next time and no attempt is spent.
func (s *Service) interrupted(ctx context.Context, item library.TranscodeItem) error {
	next := AfterInterruption(itemStateOf(item))
	return s.settle(ctx, item.MediaObjectID, library.TranscodeNeeded, func(one *library.TranscodeItem) { applyState(one, next) })
}

// blocked ends an item's conversions for a reason only a person or the file can change.
func (s *Service) blocked(ctx context.Context, id int64, code string) error {
	return s.settle(ctx, id, library.TranscodeBlocked, func(item *library.TranscodeItem) { item.FailureCode = code })
}

// errored records a conversion that went wrong and what that says about the system.
func (s *Service) errored(ctx context.Context, item library.TranscodeItem, cause error) error {
	ctx = context.WithoutCancel(ctx)
	stored, err := s.Items.Health(ctx, item.Kind)
	if err != nil {
		return err
	}
	next, health := AfterError(itemStateOf(item), healthOf(stored), item.MediaObjectID, s.Clock.Now())
	message := cause.Error()
	if len(message) > maxLastError {
		message = message[:maxLastError]
	}
	if err := s.Items.SaveHealth(ctx, storedHealth(item.Kind, health, stored.RunSeq, message)); err != nil {
		return err
	}
	return s.settle(ctx, item.MediaObjectID, library.TranscodeState(next.State), func(one *library.TranscodeItem) {
		applyState(one, next)
		one.LastError = message
	})
}

// succeeded tells the health of the kind a conversion worked, which ends a probing or backoff.
func (s *Service) succeeded(ctx context.Context, kind string) error {
	stored, err := s.Items.Health(ctx, kind)
	if err != nil {
		return err
	}
	return s.Items.SaveHealth(ctx, storedHealth(kind, AfterSuccess(), stored.RunSeq, ""))
}

// failedWith ends an item as failed with a code and a note.
func (s *Service) failedWith(ctx context.Context, id int64, code, note string) error {
	return s.settle(ctx, id, library.TranscodeFailed, func(item *library.TranscodeItem) { item.FailureCode, item.LastError = code, note })
}

func (s *Service) blockRoot(id int64, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blockedRoots == nil {
		s.blockedRoots = map[int64]time.Time{}
	}
	s.blockedRoots[id] = until
}

// rootsBlocked are the roots still held back for want of space.
func (s *Service) rootsBlocked() map[int64]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Clock.Now()
	out := map[int64]bool{}
	for id, until := range s.blockedRoots {
		if now.Before(until) {
			out[id] = true
		} else {
			delete(s.blockedRoots, id)
		}
	}
	return out
}

func describe(what string, err error) error { return fmt.Errorf("transcoder: %s: %w", what, err) }
