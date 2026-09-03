package music

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/musicbrainz"
)

// EnrichJobType is the registered type of the background enrichment sweep.
const EnrichJobType = jobs.Type("music.enrich")

// EnrichActionKey deduplicates the sweep: it is global, not per-root, so at most
// one may be active at a time (Invariant 16) the same way a per-root scan is.
const EnrichActionKey = "music.enrich"

// EnrichDefinition declares how enrichment behaves: background, low priority,
// retried on transient failure, never blocking anything a user is waiting on.
var EnrichDefinition = jobs.Definition{
	Type: EnrichJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// enrichBatchSize bounds how many placeholder editions one pass asks a provider
// about before checking in with ListPlaceholderEditions again. Small enough that
// a shutdown mid-run (ctx cancelled between batches) loses at most one batch's
// worth of otherwise-committed progress -- each batch's writes are already their
// own transaction (library.RefineEdition), so nothing partial survives either way.
const enrichBatchSize = 20

// EnrichJob refines every placeholder edition (kind='unknown') it can find into
// real metadata, and populates the parts (tracks) under it. Direct-wired the way
// the rest of internal/music is until stage 9.
type EnrichJob struct {
	identity library.Identity
	provider musicbrainz.Provider
	clock    clock.Clock
	logger   *slog.Logger
}

// EnrichJobConfig is what an EnrichJob needs. Every field is required except Logger.
type EnrichJobConfig struct {
	Identity library.Identity
	Provider musicbrainz.Provider
	Clock    clock.Clock
	Logger   *slog.Logger
}

// NewEnrichJob builds the handler.
func NewEnrichJob(config EnrichJobConfig) *EnrichJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &EnrichJob{
		identity: config.Identity, provider: config.Provider, clock: config.Clock, logger: logger,
	}
}

// Run processes every placeholder edition it can find, in bounded batches, until
// none remain or the context is cancelled. There is no persisted cursor: a
// refined edition's kind moves off 'unknown', so PlaceholderEditions' own result
// set shrinks as work succeeds -- a restart simply asks the same question again
// and gets whatever is left (library.PlaceholderEditions's doc comment).
func (j *EnrichJob) Run(ctx context.Context, _ jobs.Job) error {
	total, failed := 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("enrich.context", string(EnrichJobType), err)
		}

		batch, err := j.identity.PlaceholderEditions(ctx, enrichBatchSize)
		if err != nil {
			return fmt.Errorf("music: enrich: %w", err)
		}
		if len(batch) == 0 {
			break
		}

		for _, edition := range batch {
			total++
			if err := j.enrichOne(ctx, edition); err != nil {
				failed++
				// One placeholder edition failing (no data at the provider, a
				// transient network error) is not a reason to stop the sweep --
				// it simply stays a placeholder and is retried next run.
				j.logger.WarnContext(ctx, "edition enrichment failed",
					slog.Int64("edition_id", edition.EditionID), slog.Any("error", err))
			}
		}
		if len(batch) < enrichBatchSize {
			break
		}
	}
	j.logger.Info("music enrichment finished", slog.Int("processed", total), slog.Int("failed", failed))
	return nil
}

func (j *EnrichJob) enrichOne(ctx context.Context, edition library.PlaceholderEdition) error {
	releaseGroupMBID, ok, err := j.identity.WorkExternalID(ctx, edition.WorkID, ProviderReleaseGroup)
	if err != nil {
		return fmt.Errorf("release group id: %w", err)
	}
	if !ok {
		// A placeholder edition always comes from newEditionLink, which links
		// the release-group id in the same transaction -- this should not
		// happen, but an edition missing its work's identity is not this job's
		// to invent, so it is skipped rather than guessed at.
		return fmt.Errorf("work %d has no %s external id", edition.WorkID, ProviderReleaseGroup)
	}

	rg, release, err := j.provider.Album(ctx, musicbrainz.PriorityEnrich, releaseGroupMBID, edition.ExternalID)
	if err != nil {
		return fmt.Errorf("musicbrainz: %w", err)
	}

	workYear, refinement, parts := refinementFor(rg, release)
	if err := j.identity.RefineEdition(ctx, j.clock.Now().Unix(),
		edition.EditionID, edition.WorkID, workYear, refinement, parts); err != nil {
		return fmt.Errorf("refine: %w", err)
	}
	return nil
}

// EnqueueEnrichment submits one sweep, deduplicated by EnrichActionKey.
func EnqueueEnrichment(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{
		Type: EnrichJobType, ActionKey: EnrichActionKey, Priority: jobs.PriorityLow,
		Payload: []byte("{}"),
	})
}
