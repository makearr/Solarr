package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/imdbdata"
	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

const (
	imdbJobType        = jobs.Type("metadata.imdb_ratings")
	imdbScheduleKey    = "imdb_ratings"
	imdbScheduleCron   = "40 3 * * *" // daily; IMDb refreshes the file overnight
	imdbDownloadBudget = 15 * time.Minute
)

var imdbDefinition = jobs.Definition{
	Name: "Downloading IMDb's ratings", Type: imdbJobType, Pool: jobs.PoolMetadata, Mutating: false, MaxAttempts: 2,
}

// imdbData is the client over IMDb's dataset, shared by the TV and movie services and the daily download.
func (p *process) imdbData() imdbdata.Client {
	if p.imdbShared == nil {
		client := imdbdata.New(p.providerClient(providerIMDb, nil, httpx.ClientConfig{Timeout: imdbDownloadBudget}), metacache.New(p.metadata))
		p.imdbShared = &client
	}
	return *p.imdbShared
}

func (p *process) imdbJob() media.JobRegistration {
	return media.JobRegistration{Definition: imdbDefinition, Handler: jobs.HandlerFunc(func(ctx context.Context, _ jobs.Job) error {
		stored, err := p.imdbData().Sync(ctx)
		if err != nil {
			return err
		}
		p.logger.Info("imdb ratings downloaded", slog.Int("titles", stored))
		return nil
	})}
}

// imdbTask downloads the dataset daily while the provider is switched on; it is off by default.
func (p *process) imdbTask() jobs.Task {
	return jobs.Task{
		Key: imdbScheduleKey, Name: "Download IMDb's ratings", DefaultCadence: imdbScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMetadata,
		Enqueue: func(ctx context.Context) (jobs.EnqueueResult, error) {
			if !p.cache.Snapshot().ProviderEnabled(settings.MovieSourceIMDb) {
				return jobs.EnqueueResult{}, nil
			}
			return p.queue.Enqueue(ctx, jobs.Request{Type: imdbJobType, ActionKey: string(imdbJobType), Priority: jobs.PriorityLow})
		},
	}
}
