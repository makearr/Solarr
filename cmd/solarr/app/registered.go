package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// registerMedia builds the one list of media types in the binary, each wired to what it needs from the process.
// A new type is one more module here; nothing else in this package names a type's scan.
func (p *process) registerMedia() error {
	registry, err := media.NewRegistry(p.musicModule(), p.tvModule())
	if err != nil {
		return err
	}
	p.media = registry
	p.cache.EnableNamingValidation(adapters.NamingValidator(registry))
	return nil
}

// musicModule wires music's sweep: its consumer, the audio files it reads and its housekeeping.
func (p *process) musicModule() music.Module {
	physical := library.NewPhysical(p.library)
	provider := p.musicBrainzProvider()
	desired := library.NewDesired(p.library)
	people := library.NewPeople(p.library)
	attempts := library.NewAttempts(p.library)
	consumer := music.NewConsumer(music.ConsumerConfig{
		FS:           p.fsys,
		Physical:     physical,
		Identity:     library.NewIdentity(p.library),
		Unidentified: library.NewUnidentified(p.library),
		Attempts:     &attempts,
		Search:       provider,
		Albums:       provider,
		Fingerprints: p.tool,
		AcoustID:     p.acoustIDLookup(),
		Tracks:       provider,
		Clock:        clock.NewSystem(),
		Logger:       p.logger,
		// One identification threshold for scan and import (2026-09-24).
		Threshold:  func() float64 { return p.cache.Snapshot().ImportAutoCommitConfidence() },
		Desired:    &desired,
		People:     &people,
		Categories: adapters.Categories{Cache: p.cache},
		// The user's own default when they haven't configured one for the
		// category yet -- ensureDefaultQualityProfile seeded it at startup.
		DefaultProfileID: p.defaultQualityProfileID,
	})
	rename := music.RenameConfig{
		Categories: adapters.Categories{Cache: p.cache},
		Roots:      adapters.DrainRoots{Cache: p.cache}, Files: physical,
		Identities: library.NewDesired(p.library), Works: library.NewWorks(p.library),
		MoveJobs: library.NewMoveJobs(p.library), Queue: p.queue, Clock: clock.NewSystem(),
	}
	feed := music.Feed{
		Grab: p.musicGrab(), Indexers: p.indexerConns, WantedByArtist: library.NewHunt(p.library).WantedAlbumsByArtist, Logger: p.logger,
	}
	return music.Module{Hunt: p.musicHunt, Feed: feed, Rename: &rename, Sweep: scan.Sweep{
		Consumer: consumer,
		Include:  scan.IsAudio,
		Housekeeping: music.NewHousekeeping(music.HousekeepingConfig{
			FS: p.fsys, Categories: adapters.Categories{Cache: p.cache}, Roots: adapters.DrainRoots{Cache: p.cache},
			Affinity: p.cache, Desired: &desired, Subscriptions: library.NewSubscriptions(p.library),
			People: library.NewPeople(p.library), GroupID: lookupGroupID,
		}),
		// Enrichment follows at once, not at the next hourly run: a new library had no tracklists,
		// photos or Wanted for up to an hour (found 2026-09-28).
		OnSwept: func(ctx context.Context) { _, _ = p.enqueueCategoryCounts(ctx); _, _ = p.enqueueEnrichment(ctx) },
	}}
}

// tvModule wires TV's sweep and the job that identifies again the files that waited for review.
func (p *process) tvModule() tv.Module {
	consumer := tv.NewConsumer(tv.ConsumerConfig{
		FS: p.fsys, Physical: library.NewPhysical(p.library), Unidentified: library.NewUnidentified(p.library),
		Service: p.tvService(), Reader: library.NewSeriesReader(p.library), Clock: clock.NewSystem(), Logger: p.logger,
		DefaultProfileID: p.defaultQualityProfileID, Categories: adapters.Categories{Cache: p.cache},
	})
	retry := tv.NewRetryJob(tv.RetryConfig{
		Unidentified: library.NewUnidentified(p.library), Consumer: consumer, FS: p.fsys, Logger: p.logger,
		Libraries: func() []tv.RetryLibrary {
			snapshot := p.cache.Snapshot()
			var libraries []tv.RetryLibrary
			for _, category := range snapshot.Categories() {
				if category.MediaType == tv.MediaType {
					libraries = append(libraries, tv.RetryLibrary{CategoryID: category.ID, Roots: snapshot.RootFoldersForCategory(category.ID)})
				}
			}
			return libraries
		},
	})
	return tv.Module{
		Sweep: scan.Sweep{
			Consumer: consumer, Include: tv.IsVideo,
			OnSwept: func(ctx context.Context) { _, _ = p.enqueueCategoryCounts(ctx) },
		},
		Hunt: p.tvHunt,
		Feed: tv.Feed{
			Grab: p.tvGrab(), Reader: library.NewSeriesReader(p.library), Indexers: p.tvIndexerConns, Logger: p.logger,
			Libraries: func() []int64 {
				var ids []int64
				for _, category := range p.cache.Snapshot().Categories() {
					if category.MediaType == tv.MediaType {
						ids = append(ids, category.ID)
					}
				}
				return ids
			},
		},
		Retry: retry,
	}
}

// musicHunt searches one album the hunt chose and sends the best release.
func (p *process) musicHunt(ctx context.Context, itemID int64) (plan.Outcome, error) {
	indexers, err := p.indexerConns(ctx)
	if err != nil || len(indexers) == 0 {
		return plan.OutcomeSearched, err
	}
	result, err := p.musicGrab().SearchAndSend(ctx, itemID, indexers, music.GrabAutomatic, clock.NewSystem().Now())
	switch {
	case errors.Is(err, music.ErrTrackSearchNotSupported):
		return plan.OutcomeNotWanted, nil
	case err != nil:
		return plan.OutcomeSearched, err
	case result.AlreadyInFlight:
		return plan.OutcomeInFlight, nil
	}
	if result.Sent != nil || result.Held {
		p.logger.Info("hunt: release chosen", slog.Int64("item_id", itemID), slog.Bool("held", result.Held))
	}
	if len(result.Refusals) > 0 {
		p.logger.Info("hunt: releases refused", slog.Int64("item_id", itemID), slog.Any("refused", result.Refusals))
	}
	return plan.OutcomeSearched, nil
}

// tvHunt searches one season the hunt chose and sends the best release.
func (p *process) tvHunt(ctx context.Context, itemID int64) (plan.Outcome, error) {
	conns, err := p.tvIndexerConns(ctx)
	if err != nil {
		return plan.OutcomeSearched, err
	}
	result, err := p.tvGrab().SearchAndSend(ctx, itemID, conns, dispatch.GrabAutomatic, clock.NewSystem().Now())
	switch {
	case errors.Is(err, tv.ErrNotSearchable):
		return plan.OutcomeNotWanted, nil
	case err != nil:
		return plan.OutcomeSearched, err
	case result.AlreadyInFlight:
		return plan.OutcomeInFlight, nil
	}
	if result.Sent != nil || result.Held {
		p.logger.Info("hunt: release chosen", slog.Int64("item_id", itemID), slog.Bool("held", result.Held))
	}
	return plan.OutcomeSearched, nil
}
