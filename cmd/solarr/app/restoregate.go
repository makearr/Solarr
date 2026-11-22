package app

import (
	"context"
	"io"
	"log/slog"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/recovery"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// restoreGateFile marks, beside config.yml, that a restored library is held back from searching until it has been scanned again.
const restoreGateFile = "restore-gate.pending"

// restoreGatedTasks are the tasks that would search or send before the files imported since the backup are attached again.
var restoreGatedTasks = []string{"plan", huntTaskKey, "rss", "cutoff"}

// restoreGateCheckEvery is how often the held tasks look for the scans to be over.
const restoreGateCheckEvery = 5 * time.Second

func (p *process) restoreGatePath() string {
	return filepath.Join(p.config.Paths.ConfigDir, restoreGateFile)
}

// restoreDownloadClients are the enabled download clients as the restore reads them: what each holds under Solarr's categories.
func (p *process) restoreDownloadClients(ctx context.Context) []recovery.DownloadClient {
	clients, err := p.pollClients(ctx)
	if err != nil {
		p.logger.Warn("download clients not read for the restore", slog.Any("error", err))
		return nil
	}
	configured, err := p.cache.Downloaders(ctx)
	if err != nil {
		return nil
	}
	mediaTypes := p.cache.Snapshot().CategoryMediaTypes()
	var out []recovery.DownloadClient
	for _, downloader := range configured {
		client, ok := clients[downloader.ID]
		if !ok {
			continue
		}
		var categories []string
		for _, mediaType := range mediaTypes {
			if category := downloader.Options().CategoryFor(mediaType); !slices.Contains(categories, category) {
				categories = append(categories, category)
			}
		}
		out = append(out, recovery.DownloadClient{
			ID: downloader.ID, Name: downloader.Name, Categories: categories,
			Held: func(ctx context.Context, categories []string) ([]recovery.HeldDownload, error) {
				var held []recovery.HeldDownload
				for _, category := range categories {
					listed, err := client.Client.Downloads(ctx, category)
					if err != nil {
						return nil, err
					}
					for _, entry := range listed {
						if entry.Part {
							continue
						}
						path := ""
						if entry.Path != "" {
							path = entry.Path
							if client.LocalPath != nil {
								path = client.LocalPath(entry.Path)
							}
						}
						held = append(held, recovery.HeldDownload{
							Ref: entry.ClientRef, Name: entry.Name, Category: entry.Category, Path: path, Finished: entry.State == download.StateCompleted,
						})
					}
				}
				return held, nil
			},
		})
	}
	return out
}

// holdForRestore marks the gate and keeps what the restore found for the start that follows.
func (p *process) holdForRestore(report recovery.Report) {
	p.restoredUntracked = report.Untracked
	p.restoredUnreached = report.Unreached
	_ = fsx.WriteAtomic(p.fsys, p.restoreGatePath(), func(w io.Writer) error {
		_, err := io.WriteString(w, "the library was restored from a backup; planning waits for the first scan\n")
		return err
	})
}

// startRestoreGate, before the schedulers start, holds the searching tasks while every root is scanned again and
// imports what the clients hold that no download references; the tasks are released when no scan is left.
func (p *process) startRestoreGate(ctx context.Context) {
	if _, err := p.fsys.Stat(p.restoreGatePath()); err != nil {
		return
	}
	for _, task := range restoreGatedTasks {
		if err := p.pauses.Pause(ctx, task); err != nil {
			p.logger.Warn("restore: task not held back", slog.String("task", task), slog.Any("error", err))
		}
	}
	if _, err := p.enqueueScans(ctx, true, true, false); err != nil {
		p.logger.Warn("restore: the libraries were not queued for a scan", slog.Any("error", err))
	}
	p.importUntrackedAfterRestore(ctx)
	p.retryUnreachedClients(ctx)
	p.logger.Warn("restore: searching is held until every library has been scanned again", slog.String("tasks", strings.Join(restoreGatedTasks, ", ")))
	go p.releaseRestoreGate(ctx, clock.NewSystem())
}

func (p *process) releaseRestoreGate(ctx context.Context, wall clock.Clock) {
	ticker := wall.NewTicker(restoreGateCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			counts, err := p.queue.Counts(ctx)
			if err != nil || !scansIdle(counts) {
				continue
			}
			for _, task := range restoreGatedTasks {
				if err := p.pauses.Resume(ctx, task); err != nil {
					p.logger.Warn("restore: task not released", slog.String("task", task), slog.Any("error", err))
				}
			}
			_ = p.fsys.Remove(p.restoreGatePath())
			p.logger.Info("restore: the libraries are scanned again, searching resumes")
			return
		}
	}
}

// scansIdle is true when no library scan is waiting or running.
func scansIdle(counts map[jobs.State]map[jobs.Pool]int64) bool {
	for _, state := range []jobs.State{jobs.StatePending, jobs.StateLeased, jobs.StateRunning} {
		if counts[state][jobs.PoolScan] > 0 || counts[state][jobs.PoolRescan] > 0 {
			return false
		}
	}
	return true
}

// untrackedImportPayload imports a single-file download as the one file of its folder, the shape every download
// with an Only list already has; a folder is imported whole.
func untrackedImportPayload(fsys fsx.FS, downloadPath string) importjob.Payload {
	payload := importjob.Payload{SourcePath: downloadPath, Origin: library.ImportOriginWatched}
	if info, err := fsys.Stat(downloadPath); err == nil && !info.IsDir {
		payload.SourcePath = path.Dir(downloadPath)
		payload.Only = []string{path.Base(downloadPath)}
	}
	return payload
}

// importUntrackedAfterRestore queues an import of every finished download the restore found that nothing references;
// the import identifies the files by inspection, as every import does, so no item is guessed.
func (p *process) importUntrackedAfterRestore(ctx context.Context) {
	p.importUntracked(ctx, p.restoredUntracked)
	p.restoredUntracked = nil
}

func (p *process) importUntracked(ctx context.Context, untracked []recovery.UntrackedDownload) {
	for _, found := range untracked {
		if !found.Finished || found.Path == "" {
			continue
		}
		if _, err := importjob.Enqueue(ctx, p.queue, untrackedImportPayload(p.fsys, found.Path)); err != nil {
			p.logger.Warn("restore: a finished download was not queued for import", slog.String("name", found.Name), slog.Any("error", err))
			continue
		}
		p.logger.Info("restore: importing a download no record references", slog.String("name", found.Name), slog.String("client", found.Client))
	}
}
