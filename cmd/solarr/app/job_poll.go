package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/poll"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// registerPollJob wires M1b's download poll: sent downloads followed to imported and cleaned.
func (p *process) registerPollJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(poll.Definition); err != nil {
		return err
	}
	executor.Handle(poll.JobType, p.pollJob())
	return nil
}

// pollJob is the download poll; the Downloads queue reads and removes through the same one.
func (p *process) pollJob() *poll.Job {
	return poll.NewJob(poll.Config{
		Acquisitions: library.NewAcquisitions(p.library), Sessions: library.NewImportSessions(p.library),
		Blocklist: library.NewBlocklistStore(p.library), FS: p.fsys,
		Clients: p.pollClients, DownloadFolders: p.pollDownloadFolders,
		Import: p.importAcquisition, Search: p.searchAgain, History: p.recordDownloadHistory,
		SendHeld: p.sendHeld, Imported: p.afterDownloadImported, Clock: clock.NewSystem(), Logger: p.logger,
	})
}

// sendHeld sends held releases to clients with room again (ADR-57's HELD, section 10.5).
func (p *process) sendHeld(ctx context.Context) error {
	sent, err := p.musicDispatch().SendHeld(ctx, clock.NewSystem().Now())
	if sent > 0 {
		p.logger.Info("held releases sent", slog.Int("sent", sent))
	}
	return err
}

func (p *process) enqueuePoll(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.queue.Enqueue(ctx, jobs.Request{
		Type: poll.JobType, ActionKey: string(poll.JobType), Priority: jobs.PriorityNormal, Payload: []byte("{}"),
	})
}

// pollClients answers every enabled download connection with its options, fresh each poll.
func (p *process) pollClients(ctx context.Context) (map[int64]poll.Client, error) {
	configured, err := p.cache.Downloaders(ctx)
	if err != nil {
		return nil, err
	}
	factory := p.downloaderClientFactory()
	clients := make(map[int64]poll.Client, len(configured))
	for _, downloader := range configured {
		if !downloader.Enabled {
			continue
		}
		credentials, err := p.cache.DecryptedDownloaderCredentials(ctx, downloader.ID)
		if err != nil {
			return nil, err
		}
		client, known := adapters.NewDownloadClient(downloader.Impl, factory(downloader.ID, downloader.BaseURL), downloader.BaseURL, credentials.Reveal())
		if !known {
			continue
		}
		options := downloader.Options()
		seedRatio, seedMinutes := options.SeedGoal()
		clients[downloader.ID] = poll.Client{
			Name:            downloader.Name,
			Client:          client,
			RemoveCompleted: options.RemovesCompleted(),
			SeedRatio:       seedRatio, SeedMinutes: seedMinutes,
			LocalPath: options.LocalPathFor,
		}
	}
	return clients, nil
}

// pollDownloadFolders are the configured download folders and every client's mapped local path.
func (p *process) pollDownloadFolders(ctx context.Context) ([]string, error) {
	folders, err := p.cache.DownloadFolders(ctx)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(folders))
	for _, folder := range folders {
		paths = append(paths, folder.Path)
	}
	downloaders, err := p.cache.Downloaders(ctx)
	if err != nil {
		return nil, err
	}
	for _, downloader := range downloaders {
		if local := downloader.Options().LocalPath; local != "" {
			paths = append(paths, local)
		}
	}
	return paths, nil
}

// importAcquisition starts the targeted import of one finished download into its album's library.
func (p *process) importAcquisition(ctx context.Context, acquisition library.Acquisition, localPath string) error {
	payload := importjob.Payload{SourcePath: localPath, Origin: library.ImportOriginAcquisition, AcquisitionID: &acquisition.ID}
	if acquisition.DesiredItemID != nil {
		item, err := library.NewDesired(p.library).Get(ctx, *acquisition.DesiredItemID)
		if err != nil {
			return err
		}
		payload.CategoryID = &item.CategoryID
	}
	_, err := importjob.Enqueue(ctx, p.queue, payload)
	return err
}

// searchAgain searches a desired item after its download failed and sends the best release left,
// Lidarr's "Redownload Failed"; the failed one is blocklisted by then.
func (p *process) searchAgain(ctx context.Context, desiredItemID int64) error {
	if err := library.NewDesired(p.library).RecordDownloadFailed(ctx, desiredItemID); err != nil {
		p.logger.Warn("failed download not recorded on the hunt queue", slog.Int64("desired_item_id", desiredItemID), slog.Any("error", err))
	}
	item, err := library.NewDesired(p.library).Get(ctx, desiredItemID)
	if err != nil {
		return err
	}
	category, found := p.cache.Snapshot().Category(item.CategoryID)
	if !found {
		return nil
	}
	hunting, ok := media.Capability[media.Hunting](p.media, media.Key(category.MediaType))
	if !ok {
		return nil
	}
	_, err = hunting.HuntSearch(ctx, desiredItemID)
	return err
}
