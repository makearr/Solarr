package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// SystemHealth answers the health checks that need more than the settings
// snapshot apiserver already holds -- the connection lists, which are real
// rows rather than cached scalars.
type SystemHealth struct {
	Cache *settings.Cache
}

// Checks reports what is missing. Every probe is best-effort: a failure to
// read one list is reported as nothing rather than failing the whole screen,
// since a health page that will not render is worse than an incomplete one.
func (a SystemHealth) Checks(ctx context.Context) []apiserver.HealthCheck {
	var checks []apiserver.HealthCheck

	if indexers, err := a.Cache.Indexers(ctx); err == nil && !anyEnabledIndexer(indexers) {
		checks = append(checks, apiserver.HealthCheck{
			ID: "no_indexer", Severity: apiserver.HealthWarning,
			Message: "No indexer is enabled, so nothing can be searched for.",
			Link:    "/connections/indexers",
		})
	}
	if downloaders, err := a.Cache.Downloaders(ctx); err == nil && !anyEnabledDownloader(downloaders) {
		checks = append(checks, apiserver.HealthCheck{
			ID: "no_downloader", Severity: apiserver.HealthWarning,
			Message: "No download client is enabled, so nothing found could be fetched.",
			Link:    "/connections/downloaders",
		})
	}
	return checks
}

func anyEnabledIndexer(indexers []settings.Indexer) bool {
	for _, indexer := range indexers {
		if indexer.Enabled {
			return true
		}
	}
	return false
}

func anyEnabledDownloader(downloaders []settings.Downloader) bool {
	for _, downloader := range downloaders {
		if downloader.Enabled {
			return true
		}
	}
	return false
}
