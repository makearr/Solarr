package adapters

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs/cachebudget"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// CacheManager satisfies apiserver.CacheManager over cachebudget.Sweep and settings.Cache.
type CacheManager struct {
	Sweep    *cachebudget.Sweep
	Cache    *settings.Cache
	Registry *settings.Registry
	Clock    clock.Clock
	// Images and Metadata give the lookups the two caches answered or missed.
	Images, Metadata interface{ Totals() (hits, misses int64) }
}

// DatabaseMemory satisfies apiserver.DatabaseMemoryReader over the open stores.
type DatabaseMemory struct{ Stores []*store.Store }

func (a DatabaseMemory) DatabaseMemory(ctx context.Context) []apiserver.DatabaseMemoryRow {
	rows := make([]apiserver.DatabaseMemoryRow, 0, len(a.Stores))
	for _, opened := range a.Stores {
		report := opened.PageCache(ctx)
		rows = append(rows, apiserver.DatabaseMemoryRow{
			Name: report.Name, FileBytes: report.FileBytes, WALBytes: report.WALBytes,
			PerConnectionKB: int64(report.PerConnectionKB), Connections: int64(report.Connections),
			LimitBytes: report.LimitBytes, UsedBytes: report.UsedBytes, Hits: report.Hits, Misses: report.Misses,
		})
	}
	return rows
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
	if a.Images != nil {
		report.ImagesHits, report.ImagesMisses = a.Images.Totals()
	}
	if a.Metadata != nil {
		report.MetadataHits, report.MetadataMisses = a.Metadata.Totals()
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
