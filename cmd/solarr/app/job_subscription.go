package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// registerSubscriptionExpandJob wires section 19.6's discography expansion
// into the registry and the executor: the same musicbrainz.Provider
// enrichment already shares, this process's own category resolution
// (adapters.Categories, already used by the scan wiring), and the fallback
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
		Categories:       adapters.Categories{Cache: p.cache},
		Provider:         p.musicBrainzProvider(),
		DefaultProfileID: p.defaultQualityProfileID,
		Clock:            clock.NewSystem(),
		Logger:           p.logger,
		Search: func(ctx context.Context, categoryID, personID int64) error {
			_, err := music.EnqueueSearchArtist(ctx, p.queue, categoryID, personID)
			return err
		},
	})
	executor.Handle(music.SubscriptionExpandJobType, job)
	return p.registerFollowJob(registry, executor)
}

// registerFollowJob wires the TV counterpart: the new series of followed people and networks, found through TMDB.
func (p *process) registerFollowJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(tv.FollowDefinition); err != nil {
		return err
	}
	p.tvService()
	executor.Handle(tv.FollowJobType, tv.NewFollowJob(tv.FollowConfig{
		Subscriptions: library.NewSubscriptions(p.library), People: library.NewPeople(p.library), Source: p.tmdbShared,
		Service: p.tvService(), DefaultProfileID: p.defaultQualityProfileID, Categories: adapters.Categories{Cache: p.cache},
		Clock: clock.NewSystem(), Logger: p.logger,
	}))
	return nil
}

// enqueueSubscriptionExpand submits one expand sweep. Registered as a task
// with internal/jobs.TaskRunner (todo 4.19), so it also fires on its own
// schedule, not only once at startup.
func (p *process) enqueueSubscriptionExpand(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := music.EnqueueSubscriptionExpand(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue subscription expand", slog.Any("error", err))
	}
	if _, followErr := tv.EnqueueFollow(ctx, p.queue, 0); followErr != nil {
		p.logger.Warn("enqueue tv follow sweep", slog.Any("error", followErr))
	}
	return result, err
}
