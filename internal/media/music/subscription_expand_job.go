package music

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
	"github.com/makearr/MediaNexus/internal/platform/clock"
)

// SubscriptionExpandJobType is the registered type of the discography
// expansion sweep.
const SubscriptionExpandJobType = jobs.Type("music.subscription_expand")

// SubscriptionExpandActionKey deduplicates the sweep: global, not per-
// subscription, so at most one may be active at a time (Invariant 16), the
// same shape EnrichActionKey already uses.
const SubscriptionExpandActionKey = "music.subscription_expand"

// SubscriptionExpandDefinition declares how expansion behaves: background,
// low priority, retried on transient failure, never blocking a user.
var SubscriptionExpandDefinition = jobs.Definition{
	Type: SubscriptionExpandJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// SubscriptionExpandJob is section 19.6's "an artist subscription is
// expanded by a job, not by a query": for every enabled person subscription
// covering music, it browses the artist's discography, admits release
// groups per the subscription's filters, and creates an album-level
// desired_item for each newly-admitted one. Idempotent and additive only
// (Invariant 71) -- library.Desired.EnsureAlbumItem already leaves an
// existing item exactly as it is, which is what makes a run of this job safe
// to repeat: an unmonitored album never comes back.
type SubscriptionExpandJob struct {
	subscriptions    library.Subscriptions
	people           library.People
	identity         library.Identity
	desired          *library.Desired
	categories       CategoryFinder
	provider         musicbrainz.Provider
	defaultProfileID int64
	clock            clock.Clock
	logger           *slog.Logger
}

// SubscriptionExpandConfig is what a SubscriptionExpandJob needs. Every
// field is required except Logger.
type SubscriptionExpandConfig struct {
	Subscriptions    library.Subscriptions
	People           library.People
	Identity         library.Identity
	Desired          *library.Desired
	Categories       CategoryFinder
	Provider         musicbrainz.Provider
	DefaultProfileID int64
	Clock            clock.Clock
	Logger           *slog.Logger
}

// NewSubscriptionExpandJob builds the handler.
func NewSubscriptionExpandJob(config SubscriptionExpandConfig) *SubscriptionExpandJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &SubscriptionExpandJob{
		subscriptions: config.Subscriptions, people: config.People, identity: config.Identity,
		desired: config.Desired, categories: config.Categories, provider: config.Provider,
		defaultProfileID: config.DefaultProfileID, clock: config.Clock, logger: logger,
	}
}

// Run walks every enabled subscription once. One subscription failing (no
// external id yet, the provider unavailable) does not stop the sweep -- it
// simply produces nothing this run and is retried the next one, the same
// per-item isolation EnrichJob's own Run already uses.
func (j *SubscriptionExpandJob) Run(ctx context.Context, _ jobs.Job) error {
	subscriptions, err := j.subscriptions.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("music: subscription expand: %w", err)
	}

	var admittedTotal, skippedTotal, failed int
	for _, subscription := range subscriptions {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("subscription_expand.context", string(SubscriptionExpandJobType), err)
		}
		if subscription.SubjectType != "person" || !slices.Contains(subscription.MediaTypes, "music") {
			continue
		}
		admitted, skipped, err := j.expandOne(ctx, subscription)
		admittedTotal += admitted
		skippedTotal += skipped
		if err != nil {
			failed++
			j.logger.WarnContext(ctx, "subscription expand failed",
				slog.Int64("subscription_id", subscription.ID), slog.Any("error", err))
		}
	}
	j.logger.Info("subscription expand finished",
		slog.Int("admitted", admittedTotal), slog.Int("skipped", skippedTotal), slog.Int("failed", failed))
	return nil
}

// expandOne browses one subscribed artist's discography and creates an album
// item for every release group the subscription's filters admit. admitted
// counts release groups an item was ensured for (whether or not that item
// already existed -- Invariant 71 means "admitted" is not "created new");
// skipped counts those the filters excluded.
func (j *SubscriptionExpandJob) expandOne(
	ctx context.Context, subscription library.Subscription,
) (admitted, skipped int, err error) {
	artistMBID, ok, err := j.people.ExternalIDFor(ctx, subscription.SubjectID, ProviderArtist)
	if err != nil {
		return 0, 0, fmt.Errorf("external id: %w", err)
	}
	if !ok {
		return 0, 0, fmt.Errorf("person %d has no %s external id yet", subscription.SubjectID, ProviderArtist)
	}

	category, ok := j.categories.CategoryForMediaType("music")
	if !ok {
		return 0, 0, fmt.Errorf("no category configured for music")
	}
	profileID := j.defaultProfileID
	if category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}

	groups, err := j.provider.ArtistReleaseGroups(ctx, musicbrainz.PriorityBackfill, artistMBID)
	if err != nil {
		return 0, 0, fmt.Errorf("musicbrainz: %w", err)
	}

	filters := ParseSubscriptionFilters(subscription.FiltersJSON)
	now := j.clock.Now()
	for _, releaseGroup := range groups {
		if !filters.Admits(releaseGroup) {
			skipped++
			continue
		}
		if err := j.ensureItemFor(ctx, now, releaseGroup, category.ID, profileID); err != nil {
			j.logger.WarnContext(ctx, "subscription expand: ensure item failed",
				slog.String("release_group", releaseGroup.MBID), slog.Any("error", err))
			continue
		}
		admitted++
	}
	return admitted, skipped, nil
}

func (j *SubscriptionExpandJob) ensureItemFor(
	ctx context.Context, at time.Time, releaseGroup musicbrainz.ReleaseGroup, categoryID, profileID int64,
) error {
	workIDs, err := j.identity.ResolveWorks(ctx, at, []library.WorkLink{{
		Provider: ProviderReleaseGroup, ExternalID: releaseGroup.MBID, Confidence: 1.0,
		Work: library.WorkIdentity{
			MediaType: "music", CanonicalTitle: releaseGroup.Title, SortTitle: SortTitle(releaseGroup.Title),
		},
	}})
	if err != nil {
		return fmt.Errorf("resolve work: %w", err)
	}
	_, err = j.desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: workIDs[0], CategoryID: categoryID, ProfileID: profileID, Monitored: true,
	})
	if err != nil {
		return fmt.Errorf("ensure album item: %w", err)
	}
	return nil
}

// EnqueueSubscriptionExpand submits one subscription expand sweep,
// deduplicated by SubscriptionExpandActionKey.
func EnqueueSubscriptionExpand(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{
		Type: SubscriptionExpandJobType, ActionKey: SubscriptionExpandActionKey,
		Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
}
