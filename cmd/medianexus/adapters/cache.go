package adapters

import (
	"context"
	"time"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/jobs/cachebudget"
	"github.com/makearr/MediaNexus/internal/platform/clock"
)

// CacheManager satisfies apiserver.CacheManager over cachebudget.Sweep and settings.Cache.
type CacheManager struct {
	Sweep    *cachebudget.Sweep
	Cache    *settings.Cache
	Registry *settings.Registry
	Clock    clock.Clock
}

func (a CacheManager) Report(_ context.Context) (apiserver.CacheReport, error) {
	budget := a.Cache.Snapshot().CacheBudget()
	report := apiserver.CacheReport{
		Budget: apiserver.CacheBudget{
			MaxBytes: budget.MaxBytes, MaxAgeDays: budget.MaxAgeDays,
			ImagesSharePercent: budget.ImagesSharePercent, MetadataSharePercent: budget.MetadataSharePercent,
		},
	}
	if measurement, found := a.Cache.Snapshot().CacheMeasurement(); found {
		report.ImagesBytes = &measurement.ImagesBytes
		report.MetadataBytes = &measurement.MetadataBytes
		measuredAt := time.Unix(measurement.MeasuredAt, 0).UTC()
		report.MeasuredAt = &measuredAt
	}
	return report, nil
}

func (a CacheManager) SetBudget(ctx context.Context, budget apiserver.CacheBudget) error {
	if err := a.Registry.LookupAll(
		settings.KeyCacheMaxBytes, settings.KeyCacheMaxAgeDays, settings.KeyCacheImagesShare, settings.KeyCacheMetadataShare,
	); err != nil {
		return err
	}
	return a.Cache.SetCacheBudget(ctx, a.Clock, settings.CacheBudget{
		MaxBytes: budget.MaxBytes, MaxAgeDays: budget.MaxAgeDays,
		ImagesSharePercent: budget.ImagesSharePercent, MetadataSharePercent: budget.MetadataSharePercent,
	})
}

func (a CacheManager) Clear(ctx context.Context) error {
	return a.Sweep.Clear(ctx)
}
