package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/metacache"
)

// Priority is ADR-29's request-priority ladder. Every level is accepted today, but
// only ordering by source (ADR-13's breaker/limiter, per source) actually happens
// yet -- there is exactly one caller (the stage 3 enrichment job, priority 3 or 4)
// and building real cross-priority scheduling ahead of a second caller needing it
// would be anticipation, not generalising from evidence. The type exists so a
// second caller does not have to change every signature to add it.
type Priority int

const (
	// PriorityInteractive is a work the user is looking at right now.
	PriorityInteractive Priority = iota + 1
	// PriorityIdentify is a file with no embedded MBID (stage 4's parser).
	PriorityIdentify
	// PriorityEnrich is a monitored item's normal background enrichment.
	PriorityEnrich
	// PriorityBackfill is everything else, in no particular hurry.
	PriorityBackfill
	// PriorityRefresh is ADR-29's staleness policy, lowest of all.
	PriorityRefresh
)

const (
	// cacheKindReleaseGroup and cacheKindRelease are internal/metacache's "kind"
	// for this package's two entities.
	cacheKindReleaseGroup = "musicbrainz:release-group"
	cacheKindRelease      = "musicbrainz:release"
)

// Provider is ADR-29 end to end: try each source in priority order, falling
// through on failure (ADR-13's breaker already skips a source that is down
// without this package needing to ask it first), permanent cache in front so a
// repeat lookup costs nothing.
type Provider struct {
	sources []Source
	cache   metacache.Cache
	clock   clock.Clock
	logger  *slog.Logger
}

// Config is what a Provider needs. Sources are tried in order -- ADR-29 puts the
// Servarr proxy first, direct MusicBrainz second, but this package does not care
// which or how many.
type Config struct {
	Sources []Source
	Cache   metacache.Cache
	Clock   clock.Clock
	Logger  *slog.Logger
}

// New builds a Provider.
func New(config Config) Provider {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return Provider{
		sources: config.Sources, cache: config.Cache, clock: config.Clock, logger: logger,
	}
}

// Album answers a release group and one of its releases, cache first. priority is
// accepted but does not yet change behaviour -- see Priority's doc comment.
func (p Provider) Album(
	ctx context.Context, _ Priority, releaseGroupMBID, releaseMBID string,
) (ReleaseGroup, Release, error) {
	if rg, release, ok := p.cached(ctx, releaseGroupMBID, releaseMBID); ok {
		return rg, release, nil
	}

	var errs []error
	for _, source := range p.sources {
		rg, release, err := source.Album(ctx, releaseGroupMBID, releaseMBID)
		if err != nil {
			p.logger.WarnContext(ctx, "musicbrainz source failed",
				slog.String("source", source.Name()), slog.Any("error", err))
			errs = append(errs, err)
			continue
		}
		p.remember(ctx, source.Name(), rg, release)
		return rg, release, nil
	}
	return ReleaseGroup{}, Release{}, fmt.Errorf("musicbrainz: no source answered for %s/%s: %w",
		releaseGroupMBID, releaseMBID, errors.Join(errs...))
}

func (p Provider) cached(ctx context.Context, releaseGroupMBID, releaseMBID string) (ReleaseGroup, Release, bool) {
	rgEntry, err := p.cache.Get(ctx, cacheKindReleaseGroup, releaseGroupMBID)
	if err != nil {
		return ReleaseGroup{}, Release{}, false
	}
	releaseEntry, err := p.cache.Get(ctx, cacheKindRelease, releaseMBID)
	if err != nil {
		return ReleaseGroup{}, Release{}, false
	}

	var rg ReleaseGroup
	var release Release
	if json.Unmarshal([]byte(rgEntry.Body), &rg) != nil || json.Unmarshal([]byte(releaseEntry.Body), &release) != nil {
		// A cache row this package itself wrote should always decode; treated as
		// a miss rather than a hard failure, since a re-fetch always recovers.
		return ReleaseGroup{}, Release{}, false
	}
	return rg, release, true
}

func (p Provider) remember(ctx context.Context, source string, rg ReleaseGroup, release Release) {
	now := p.clock.Now()
	rgBody, err := json.Marshal(rg)
	if err == nil {
		if err := p.cache.Set(ctx, cacheKindReleaseGroup, rg.MBID,
			metacache.Entry{Source: source, Body: string(rgBody), FetchedAt: now}); err != nil {
			p.logger.WarnContext(ctx, "musicbrainz: cache write failed", slog.Any("error", err))
		}
	}
	releaseBody, err := json.Marshal(release)
	if err == nil {
		if err := p.cache.Set(ctx, cacheKindRelease, release.MBID,
			metacache.Entry{Source: source, Body: string(releaseBody), FetchedAt: now}); err != nil {
			p.logger.WarnContext(ctx, "musicbrainz: cache write failed", slog.Any("error", err))
		}
	}
}
