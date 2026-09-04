package main

import (
	"context"
	"log/slog"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/music"
)

// registerSubscriptionExpandJob wires section 19.6's discography expansion
// into the registry and the executor: the same musicbrainz.Provider
// enrichment already shares, this process's own category resolution
// (cacheCategories, already used by the scan wiring), and the fallback
// quality profile ensureDefaultQualityProfile seeded at startup.
func (p *process) registerSubscriptionExpandJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(music.SubscriptionExpandDefinition); err != nil {
		return err
	}
	desired := library.NewDesired(p.library)
	job := music.NewSubscriptionExpandJob(music.SubscriptionExpandConfig{
		Subscriptions:    library.NewSubscriptions(p.library),
		People:           library.NewPeople(p.library),
		Identity:         library.NewIdentity(p.library),
		Desired:          &desired,
		Categories:       cacheCategories{cache: p.cache},
		Provider:         p.musicBrainzProvider(),
		DefaultProfileID: p.defaultQualityProfileID,
		Clock:            clock.NewSystem(),
		Logger:           p.logger,
	})
	executor.Handle(music.SubscriptionExpandJobType, job)
	return nil
}

// enqueueSubscriptionExpand submits one expand sweep, once at startup -- the
// same "no scheduler yet" gap enqueueEnrichment carries.
func (p *process) enqueueSubscriptionExpand(ctx context.Context) {
	if _, err := music.EnqueueSubscriptionExpand(ctx, p.queue); err != nil {
		p.logger.Warn("enqueue subscription expand", slog.Any("error", err))
	}
}
