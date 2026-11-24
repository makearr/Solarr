package transcoder

import (
	"context"

	"github.com/makearr/Solarr/internal/jobs"
)

// DispatchJobType keeps the analysis current and starts the next conversions.
const DispatchJobType = jobs.Type("transcode.dispatch")

// DispatchActionKey is the one dispatch that may be queued at a time.
const DispatchActionKey = "transcode.dispatch"

// DispatchDefinition declares the dispatcher: short bookkeeping that only submits other jobs.
var DispatchDefinition = jobs.Definition{Name: "Dispatching conversions", Type: DispatchJobType, Pool: jobs.PoolHousekeeping, MaxAttempts: 2}

// EnqueueDispatch submits a dispatch; one already queued absorbs this one.
func EnqueueDispatch(ctx context.Context, queue Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: DispatchJobType, ActionKey: DispatchActionKey, Priority: jobs.PriorityLow})
}

// DispatchHandler adapts the dispatcher to the job queue.
type DispatchHandler struct{ Service *Service }

// Run dispatches once.
func (h DispatchHandler) Run(ctx context.Context, _ jobs.Job) error { return h.Service.Dispatch(ctx) }

// Dispatch queues the analysis of every enabled library; the master switch off queues nothing.
func (s *Service) Dispatch(ctx context.Context) error {
	if !s.Settings.Global().Enabled {
		return nil
	}
	libraries, err := s.Settings.Libraries(ctx)
	if err != nil {
		return err
	}
	for _, one := range libraries {
		if one.Enabled && one.Supported() {
			if _, err := EnqueueAnalyze(ctx, s.Queue, one.CategoryID); err != nil {
				return err
			}
		}
	}
	return nil
}
