package adapters

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/acquiring/poll"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
)

// SystemHealth answers the health checks that need more than the settings
// snapshot apiserver already holds -- the connection lists, which are real
// rows rather than cached scalars.
type SystemHealth struct {
	Cache *settings.Cache
	// Acquisitions finds downloads waiting on a person; nil skips that check.
	Acquisitions interface {
		ListActive(ctx context.Context) ([]library.Acquisition, error)
	}
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
	checks = append(checks, a.waitingForReview(ctx)...)
	return append(checks, pausedForRoom(a.Cache.Snapshot())...)
}

// waitingForReview counts the downloads whose import is held until someone identifies them on the Import page.
func (a SystemHealth) waitingForReview(ctx context.Context) []apiserver.HealthCheck {
	if a.Acquisitions == nil {
		return nil
	}
	active, err := a.Acquisitions.ListActive(ctx)
	if err != nil {
		return nil
	}
	waiting := 0
	for _, acquisition := range active {
		if acquisition.State == poll.StateImporting && acquisition.LastError == poll.WaitingForReview {
			waiting++
		}
	}
	if waiting == 0 {
		return nil
	}
	return []apiserver.HealthCheck{{
		ID: "downloads_waiting_for_review", Severity: apiserver.HealthWarning,
		Message: fmt.Sprintf("%d downloaded %s waiting for you to identify %s on the Import page.",
			waiting, plural(waiting, "album is", "albums are"), plural(waiting, "it", "them")),
		Link: "/storage/import",
	}}
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

// pausedForRoom names each library whose automatic downloads stop for want of free space
// (ADR-12; Invariant 29: every pause is visible, with its resume condition).
func pausedForRoom(snapshot *settings.Snapshot) []apiserver.HealthCheck {
	var checks []apiserver.HealthCheck
	for _, category := range snapshot.Categories() {
		if room, measured := snapshot.AcquisitionHeadroom(category.ID); measured && room <= 0 {
			checks = append(checks, apiserver.HealthCheck{
				ID: fmt.Sprintf("no_room_%d", category.ID), Severity: apiserver.HealthWarning,
				// No link: the fix is free disk space, and the reserve's page is advanced (2026-09-29 tier rule).
				Message: fmt.Sprintf("Automatic downloads for %s are paused: no root folder taking new files has "+
					"free space above its reserve. Free up space, or lower the reserve under Storage → Root folders; "+
					"your own grabs still go.", category.Name),
			})
		}
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
