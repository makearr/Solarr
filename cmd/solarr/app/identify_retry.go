package app

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// identifyRetryDirectoriesPerScan bounds one rescan job, so a large queue becomes several short jobs.
const identifyRetryDirectoriesPerScan = 200

// identifyRetryTask is "Identify waiting files": the files a scan could not place are asked about again, because a scan
// only offers new or changed files and a provider outage or a fix would otherwise leave them waiting for good.
func (p *process) identifyRetryTask() jobs.Task {
	return jobs.Task{
		Key: media.IdentifyRetryKey, Name: "Identify waiting files", DefaultCadence: identifyRetryScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryLibrary, Enqueue: p.enqueueIdentifyRetry,
	}
}

// enqueueIdentifyRetry rescans the folders of the waiting files of every library whose type declares this retry; each folder goes through its own media type's consumer, probed as a scan would.
func (p *process) enqueueIdentifyRetry(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.identifyWaiting(ctx, func(settings.Category) bool { return true })
}

// IdentifyLibraryAgain asks again about one library's waiting files, now; it answers the folders queued.
func (p *process) IdentifyLibraryAgain(ctx context.Context, categoryID int64) (int, error) {
	category, found := p.cache.Snapshot().Category(categoryID)
	if !found || !sharedRetry(p.media, category.MediaType) {
		return 0, nil
	}
	_, folders, err := p.identifyCategory(ctx, category)
	return folders, err
}

func (p *process) identifyWaiting(ctx context.Context, wanted func(settings.Category) bool) (jobs.EnqueueResult, error) {
	var last jobs.EnqueueResult
	for _, category := range p.cache.Snapshot().Categories() {
		if !sharedRetry(p.media, category.MediaType) || !wanted(category) {
			continue
		}
		result, _, err := p.identifyCategory(ctx, category)
		if err != nil {
			return last, err
		}
		if result.Job.ID != 0 {
			last = result
		}
	}
	return last, nil
}

// identifyCategory queues a rescan of the folders of one library's waiting files.
func (p *process) identifyCategory(ctx context.Context, category settings.Category) (jobs.EnqueueResult, int, error) {
	unidentified := library.NewUnidentified(p.library)
	var last jobs.EnqueueResult
	folders := 0
	for _, root := range p.cache.Snapshot().RootFoldersForCategory(category.ID) {
		directories, err := adapters.WaitingDirectories(ctx, unidentified, root.ID)
		if err != nil {
			return last, folders, err
		}
		for chunk := range slices.Chunk(directories, identifyRetryDirectoriesPerScan) {
			result, err := scan.EnqueueDirectories(ctx, p.queue, root.ID, category.ID, chunk)
			if err != nil {
				p.logger.Warn("enqueue identify retry", slog.Int64("root_folder_id", root.ID), slog.Any("error", err))
				return last, folders, err
			}
			last = result
			folders += len(chunk)
		}
	}
	return last, folders, nil
}

// outageRetrySpacing keeps a provider that flaps from re-reading every waiting folder each time it comes back.
const outageRetrySpacing = time.Hour

// noteProviderRecovered is the breaker's report of a circuit closing; a request is on its way, so only a note is made.
func (p *process) noteProviderRecovered(provider string) {
	if _, metadata := settings.MetadataProviderByKey(provider); !metadata {
		return
	}
	p.recoveredMu.Lock()
	defer p.recoveredMu.Unlock()
	if p.recovered == nil {
		p.recovered = map[string]bool{}
	}
	p.recovered[provider] = true
}

// outageRetryTask asks again about the files a provider outage left waiting, once it is back (live_test 7).
func (p *process) outageRetryTask() jobs.Task {
	return jobs.Task{
		Key: "identify_after_outage", Name: "Identify files a provider outage left waiting", DefaultCadence: outageRetryScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryLibrary, Enqueue: p.enqueueOutageRetry,
	}
}

func (p *process) enqueueOutageRetry(ctx context.Context) (jobs.EnqueueResult, error) {
	mediaTypes := p.takeRecoveredMediaTypes(clock.NewSystem().Now())
	if len(mediaTypes) == 0 {
		return jobs.EnqueueResult{}, nil
	}
	return p.identifyWaiting(ctx, func(category settings.Category) bool { return slices.Contains(mediaTypes, category.MediaType) })
}

// takeRecoveredMediaTypes empties the notes into the media types of the providers not retried within the spacing.
func (p *process) takeRecoveredMediaTypes(now time.Time) []string {
	p.recoveredMu.Lock()
	defer p.recoveredMu.Unlock()
	if p.lastOutageRetry == nil {
		p.lastOutageRetry = map[string]time.Time{}
	}
	var mediaTypes []string
	for key := range p.recovered {
		provider, known := settings.MetadataProviderByKey(key)
		if !known || now.Sub(p.lastOutageRetry[key]) < outageRetrySpacing {
			continue
		}
		p.lastOutageRetry[key] = now
		delete(p.recovered, key)
		mediaTypes = append(mediaTypes, provider.MediaTypes...)
	}
	slices.Sort(mediaTypes)
	return slices.Compact(mediaTypes)
}

// sharedRetry says a library of the media type has its waiting files retried by this schedule, as its type declares.
func sharedRetry(registry *media.Registry, mediaType string) bool {
	module, found := registry.Module(media.Key(mediaType))
	return found && module.Descriptor().UnidentifiedRetry == media.IdentifyRetryKey
}
