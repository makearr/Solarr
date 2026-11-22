package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/db/recovery"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// restoreRetryJobType is the registered one-shot that asks a download client again after a restore found it unreachable.
const restoreRetryJobType = jobs.Type("recovery.retry_download_clients")

// restoreRetryActionKey keeps one retry queued at a time.
const restoreRetryActionKey = "recovery.retry_download_clients"

// restoreRetryAttempts bounds the retries; each waits longer than the one before.
const restoreRetryAttempts = 6

// restoreRetryWait is the wait before the first retry; attempt n waits n times as long.
const restoreRetryWait = 2 * time.Minute

var restoreRetryDefinition = jobs.Definition{
	Type: restoreRetryJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 1,
}

type restoreRetryPayload struct {
	ClientIDs []int64 `json:"client_ids"`
	Attempt   int     `json:"attempt"`
}

func (p *process) registerRestoreRetryJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(restoreRetryDefinition); err != nil {
		return err
	}
	executor.Handle(restoreRetryJobType, jobs.HandlerFunc(p.runRestoreRetry))
	return nil
}

// retryUnreachedClients queues the first retry for the clients the restore could not ask.
func (p *process) retryUnreachedClients(ctx context.Context) {
	if len(p.restoredUnreached) == 0 {
		return
	}
	p.enqueueRestoreRetry(ctx, restoreRetryPayload{ClientIDs: p.restoredUnreached, Attempt: 1}, clock.NewSystem().Now())
	p.restoredUnreached = nil
}

func (p *process) enqueueRestoreRetry(ctx context.Context, payload restoreRetryPayload, now time.Time) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, err = p.queue.Enqueue(ctx, jobs.Request{
		Type: restoreRetryJobType, ActionKey: fmt.Sprintf("%s:%d", restoreRetryActionKey, payload.Attempt), Priority: jobs.PriorityNormal, Payload: encoded,
		RunAfter: now.Add(time.Duration(payload.Attempt) * restoreRetryWait),
	})
	if err != nil {
		p.logger.Warn("restore: the retry of an unreachable download client was not queued", slog.Any("error", err))
	}
}

// runRestoreRetry asks the clients again; one still unreachable is retried later, up to a bound.
func (p *process) runRestoreRetry(ctx context.Context, job jobs.Job) error {
	var payload restoreRetryPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	var clients []recovery.DownloadClient
	for _, client := range p.restoreDownloadClients(ctx) {
		if slices.Contains(payload.ClientIDs, client.ID) {
			clients = append(clients, client)
		}
	}
	wall := clock.NewSystem()
	report, err := recovery.ReconcileDownloads(ctx, wall, p.library, clients)
	if err != nil {
		return err
	}
	p.importUntracked(ctx, report.Untracked)
	p.logger.Info("restore: download clients asked again",
		slog.Int64("downloads_closed", report.DownloadsClosed), slog.Int("still_unreachable", len(report.Unreached)))
	if next, again := nextRestoreRetry(payload, report.Unreached); again {
		p.enqueueRestoreRetry(ctx, next, wall.Now())
	}
	return nil
}

// nextRestoreRetry is the retry that follows one that still left clients unreachable; none once the bound is reached.
func nextRestoreRetry(done restoreRetryPayload, unreached []int64) (restoreRetryPayload, bool) {
	if len(unreached) == 0 || done.Attempt >= restoreRetryAttempts {
		return restoreRetryPayload{}, false
	}
	return restoreRetryPayload{ClientIDs: unreached, Attempt: done.Attempt + 1}, true
}
