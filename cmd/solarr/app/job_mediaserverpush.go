package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/serverpush"
	"github.com/makearr/Solarr/internal/service/webhook"
)

// mediaServerPushJobType is the registered type of one media server's push run.
const mediaServerPushJobType = jobs.Type("library.media_server_push")

// mediaServerPushScheduleCron picks up rows a missed schedule left behind.
const mediaServerPushScheduleCron = "* * * * *"

// mediaServerPushDefinition is mutating: the dry-run gate refuses it, because a push is an outward write.
var mediaServerPushDefinition = jobs.Definition{
	Name: "Telling media servers about new files",
	Type: mediaServerPushJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 3,
}

type mediaServerPushPayload struct {
	ServerID int64 `json:"server_id"`
}

// mediaServerPushJob sends one server's due rows.
type mediaServerPushJob struct{ service serverpush.Service }

func (j mediaServerPushJob) Run(ctx context.Context, job jobs.Job) error {
	var payload mediaServerPushPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return jobs.Fatalf("media_server_push", "payload", err)
	}
	return j.service.Flush(ctx, payload.ServerID)
}

// pushScheduler enqueues one deduplicated run per server.
type pushScheduler struct {
	p *process
}

func (s pushScheduler) ScheduleFlush(ctx context.Context, serverID int64, delay time.Duration) error {
	payload, err := json.Marshal(mediaServerPushPayload{ServerID: serverID})
	if err != nil {
		return err
	}
	request := jobs.Request{
		Type: mediaServerPushJobType, ActionKey: "media-server-push:" + strconv.FormatInt(serverID, 10),
		Priority: jobs.PriorityLow, Payload: payload,
	}
	if delay > 0 {
		request.RunAfter = clock.NewSystem().Now().Add(delay)
	}
	_, err = s.p.queue.Enqueue(ctx, request)
	return err
}

// pushClients opens a connection's client and asks whether it can be told of files.
type pushClients struct{ p *process }

func (c pushClients) Pusher(ctx context.Context, server settings.MediaServer) (mediaserver.Pusher, error) {
	credential, err := c.p.cache.DecryptedMediaServerCredential(ctx, server.ID)
	if err != nil {
		return nil, err
	}
	client, err := c.p.mediaServerClients(server.Kind, server.BaseURL, server.Username, credential)
	if err != nil {
		return nil, err
	}
	pusher, ok := client.(mediaserver.Pusher)
	if !ok {
		return nil, fmt.Errorf("media server: %s does not take pushes yet", server.Kind)
	}
	return pusher, nil
}

// serverPush is the push service every placing, moving and recycling seam records into.
func (p *process) serverPush() serverpush.Service {
	return serverpush.Service{
		Settings: p.cache, Roots: adapters.DrainRoots{Cache: p.cache}, Queue: library.NewMediaServerPending(p.library),
		Clients: pushClients{p}, Scheduler: pushScheduler{p}, Clock: clock.NewSystem(), Logger: p.logger,
	}
}

// recordServerPush queues a work's folders for the media servers; a failure is logged, never the placing's.
func (p *process) recordServerPush(ctx context.Context, categoryID, workID int64, change string) {
	if err := p.serverPush().RecordWork(ctx, categoryID, workID, change); err != nil {
		p.logger.Warn("could not queue a media server push", "work_id", workID, "error", err)
	}
	p.recordWebhook(ctx, categoryID, workID, map[string]webhook.Event{
		library.PendingCreated: webhook.EventImported, library.PendingModified: webhook.EventUpgraded, library.PendingDeleted: webhook.EventFileRecycled,
	}[change])
}

// registerMediaServerPushJob wires the push run into the registry and the executor.
func (p *process) registerMediaServerPushJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(mediaServerPushDefinition); err != nil {
		return err
	}
	executor.Handle(mediaServerPushJobType, mediaServerPushJob{service: p.serverPush()})
	return nil
}

// enqueueMediaServerPushes asks for a run of every server with due rows.
func (p *process) enqueueMediaServerPushes(ctx context.Context) (jobs.EnqueueResult, error) {
	return jobs.EnqueueResult{}, p.serverPush().ScheduleDue(ctx)
}

// afterPlacing is the seam of the media types that have no server files to write: only the media servers are told.
func (p *process) afterPlacing(ctx context.Context, categoryID, workID int64) {
	p.recordServerPush(ctx, categoryID, workID, library.PendingCreated)
}

// recycleBin is the bin of the places that delete a library file for the user: the media servers hear of each file.
func (p *process) recycleBin() library.RecycleBin {
	return library.NewRecycleBin(p.library, p.fsys, clock.NewSystem()).WithOnRecycled(func(ctx context.Context, entry library.Entry) {
		if entry.RootFolderID != nil {
			p.recordServerFolder(ctx, *entry.RootFolderID, path.Dir(entry.OriginalPath), library.PendingDeleted)
		}
	})
}

// recordServerFolder queues a folder for every library that keeps files under the root folder.
func (p *process) recordServerFolder(ctx context.Context, rootFolderID int64, folder, change string) {
	for _, attached := range p.cache.Snapshot().CategoryRoots() {
		if attached.RootFolderID != rootFolderID {
			continue
		}
		if err := p.serverPush().Record(ctx, attached.CategoryID, 0, change, []string{folder}); err != nil {
			p.logger.Warn("could not queue a media server push", "folder", folder, "error", err)
		}
	}
}

// recordMoveJobPush queues the folders a finished move or rename left and entered.
func (p *process) recordMoveJobPush(ctx context.Context, moveJobID int64) {
	items, err := library.NewMoveJobs(p.library).ListItems(ctx, moveJobID)
	if err != nil {
		p.logger.Warn("could not list a move job for the media servers", "move_job_id", moveJobID, "error", err)
		return
	}
	roots := adapters.DrainRoots{Cache: p.cache}
	renamed := 0
	for _, item := range items {
		if item.State != library.MoveItemDone {
			continue
		}
		renamed++
		if root, ok := roots.RootFolder(item.DestRootID); ok {
			p.recordServerFolder(ctx, root.ID, path.Dir(path.Join(root.Path, item.DestRelPath)), library.PendingModified)
		}
		root, ok := roots.RootFolder(item.SourceRootID)
		if !ok || item.KeepSource || item.SourceRelPath == "" {
			continue
		}
		left := path.Dir(path.Join(root.Path, item.SourceRelPath))
		if item.SourceRootID != item.DestRootID || path.Dir(item.SourceRelPath) != path.Dir(item.DestRelPath) {
			p.recordServerFolder(ctx, root.ID, left, library.PendingDeleted)
		}
	}
	if job, err := library.NewMoveJobs(p.library).Get(ctx, moveJobID); err == nil && job.CategoryID != nil && renamed > 0 {
		p.recordWebhookOccurrence(ctx, *job.CategoryID, webhook.Occurrence{Event: webhook.EventRenamed, Message: fmt.Sprintf("%d files renamed", renamed)})
	}
}

// forgetMediaServer drops the push rows of a deleted connection; a failure is logged, the delete stands.
func (p *process) forgetMediaServer(ctx context.Context, serverID int64) {
	if err := library.NewMediaServerPending(p.library).ClearServer(ctx, serverID); err != nil {
		p.logger.Warn("media server push rows not cleared", "server", serverID, "err", err)
	}
	if err := library.NewPlayback(p.library).ClearServer(ctx, serverID); err != nil {
		p.logger.Warn("media server watch state not cleared", "server", serverID, "err", err)
	}
}
