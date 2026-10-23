package adapters

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs/cachebudget"
	"github.com/makearr/Solarr/internal/platform/clock"
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
			MinFreePercent: &budget.MinFreePercent,
		},
	}
	if measurement, found := a.Cache.Snapshot().CacheMeasurement(); found {
		report.ImagesBytes = &measurement.ImagesBytes
		report.MetadataBytes = &measurement.MetadataBytes
		measuredAt := time.Unix(measurement.MeasuredAt, 0).UTC()
		report.MeasuredAt = &measuredAt
		if measurement.DiskTotalBytes > 0 {
			report.DiskTotalBytes, report.DiskFreeBytes = &measurement.DiskTotalBytes, &measurement.DiskFreeBytes
		}
		if measurement.ShrunkToBytes > 0 {
			report.ShrunkToBytes = &measurement.ShrunkToBytes
		}
	}
	return report, nil
}

func (a CacheManager) SetBudget(ctx context.Context, budget apiserver.CacheBudget) error {
	if err := a.Registry.LookupAll(
		settings.KeyCacheMaxBytes, settings.KeyCacheMaxAgeDays, settings.KeyCacheImagesShare, settings.KeyCacheMetadataShare,
		settings.KeyCacheMinFreePercent,
	); err != nil {
		return err
	}
	minFree := a.Cache.Snapshot().CacheBudget().MinFreePercent
	if budget.MinFreePercent != nil {
		minFree = *budget.MinFreePercent
	}
	return a.Cache.SetCacheBudget(ctx, a.Clock, settings.CacheBudget{
		MaxBytes: budget.MaxBytes, MaxAgeDays: budget.MaxAgeDays,
		ImagesSharePercent: budget.ImagesSharePercent, MetadataSharePercent: budget.MetadataSharePercent,
		MinFreePercent: minFree,
	})
}

func (a CacheManager) Clear(ctx context.Context) error {
	return a.Sweep.Clear(ctx)
}
