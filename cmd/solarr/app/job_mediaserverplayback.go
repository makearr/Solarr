package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/playback"
)

// mediaServerPlaybackJobType is the registered type of one media server's watch-state sync.
const mediaServerPlaybackJobType = jobs.Type("library.media_server_playback")

// mediaServerPlaybackScheduleCron syncs every 30 minutes.
const mediaServerPlaybackScheduleCron = "*/30 * * * *"

// mediaServerPlaybackDefinition only reads from the servers and writes the library's own table: the dry-run gate has nothing to hold back.
var mediaServerPlaybackDefinition = jobs.Definition{
	Name: "Reading what media server users watched",
	Type: mediaServerPlaybackJobType, Pool: jobs.PoolHousekeeping, MaxAttempts: 2,
}

type mediaServerPlaybackPayload struct {
	ServerID int64 `json:"server_id"`
	Full     bool  `json:"full"`
}

type mediaServerPlaybackJob struct{ service playback.Service }

func (j mediaServerPlaybackJob) Run(ctx context.Context, job jobs.Job) error {
	var payload mediaServerPlaybackPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return jobs.Fatalf("media_server_playback", "payload", err)
	}
	_, err := j.service.Sync(ctx, payload.ServerID, payload.Full)
	return err
}

// playbackSettings adds the root folders to the settings cache.
type playbackSettings struct{ *settings.Cache }

func (s playbackSettings) RootFolders() []settings.RootFolder { return s.Snapshot().RootFolders() }

// playbackClients opens the playback side of a connection's client.
type playbackClients struct{ p *process }

func (c playbackClients) Playback(ctx context.Context, server settings.MediaServer) (playback.Client, error) {
	credential, err := c.p.cache.DecryptedMediaServerCredential(ctx, server.ID)
	if err != nil {
		return playback.Client{}, err
	}
	client, err := c.p.mediaServerClients(server.Kind, server.BaseURL, server.Username, credential)
	if err != nil {
		return playback.Client{}, err
	}
	reader, ok := client.(mediaserver.PlaybackReader)
	if !ok {
		return playback.Client{}, fmt.Errorf("media server: %s keeps no watch state to read yet", server.Kind)
	}
	lister, _ := client.(mediaserver.UserLister)
	return playback.Client{Reader: reader, Lister: lister}, nil
}

func (p *process) playbackService() playback.Service {
	return playback.Service{
		Settings: playbackSettings{p.cache}, Store: library.NewPlayback(p.library), Clients: playbackClients{p},
		Clock: clock.NewSystem(), Logger: p.logger,
	}
}

// enqueueMediaServerPlayback asks for one run per connection that syncs watch state, deduplicated by server.
func (p *process) enqueueMediaServerPlayback(ctx context.Context) (jobs.EnqueueResult, error) {
	servers, err := p.cache.MediaServers(ctx)
	if err != nil {
		return jobs.EnqueueResult{}, err
	}
	for _, server := range servers {
		if server.Enabled && server.SyncPlayback {
			if err := p.syncMediaServerPlayback(ctx, server.ID, false); err != nil {
				return jobs.EnqueueResult{}, err
			}
		}
	}
	return jobs.EnqueueResult{}, nil
}

// syncMediaServerPlayback queues one connection's sync; Run now on the connection reads everything again.
func (p *process) syncMediaServerPlayback(ctx context.Context, serverID int64, full bool) error {
	_, err := p.queuePlaybackSync(ctx, serverID, full)
	return err
}

// queuePlaybackSync is syncMediaServerPlayback that says which job carries the sync.
func (p *process) queuePlaybackSync(ctx context.Context, serverID int64, full bool) (jobs.EnqueueResult, error) {
	payload, err := json.Marshal(mediaServerPlaybackPayload{ServerID: serverID, Full: full})
	if err != nil {
		return jobs.EnqueueResult{}, err
	}
	return p.queue.Enqueue(ctx, jobs.Request{
		Type: mediaServerPlaybackJobType, ActionKey: "media-server-playback:" + strconv.FormatInt(serverID, 10) + ":" + strconv.FormatBool(full),
		Priority: jobs.PriorityLow, Payload: payload,
	})
}

func (p *process) registerMediaServerPlaybackJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(mediaServerPlaybackDefinition); err != nil {
		return err
	}
	executor.Handle(mediaServerPlaybackJobType, mediaServerPlaybackJob{service: p.playbackService()})
	return nil
}
