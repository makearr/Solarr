package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/makearr/MediaNexus/internal/metadata/metacache"
	"github.com/makearr/MediaNexus/internal/platform/clock"
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
	// cacheKindReleaseGroup and cacheKindRelease are internal/metadata/metacache's "kind"
	// for this package's two entities.
	cacheKindReleaseGroup = "musicbrainz:release-group"
	cacheKindRelease      = "musicbrainz:release"
	// cacheKindSearch is a text search's result set, keyed on searchCacheKey
	// rather than an MBID -- there is no id to key it on until a search answers.
	cacheKindSearch = "musicbrainz:search"
	// cacheKindArtist is one artist's own detail, keyed on their MBID.
	cacheKindArtist = "musicbrainz:artist"
	// cacheKindDiscography is one artist's full release-group list, keyed on
	// their MBID -- refreshed by the same RefreshAfter policy as everything
	// else, since a discography grows over time unlike a release group's own
	// fixed facts.
	cacheKindDiscography = "musicbrainz:discography"
)

// Provider is ADR-29 end to end: try each source in priority order, falling
// through on failure (ADR-13's breaker already skips a source that is down
// without this package needing to ask it first), permanent cache in front so a
// repeat lookup costs nothing.
type Provider struct {
	sources      []Source
	cache        metacache.Cache
	clock        clock.Clock
	logger       *slog.Logger
	refreshAfter time.Duration
}

// Config is what a Provider needs. Sources are tried in order -- ADR-29 puts the
// Servarr proxy first, direct MusicBrainz second, but this package does not care
// which or how many.
type Config struct {
	Sources []Source
	Cache   metacache.Cache
	Clock   clock.Clock
	Logger  *slog.Logger
	// RefreshAfter is how old a cache entry may be before it is treated as a miss
	// (ADR-29's staleness policy). Zero means never expire -- today's behaviour.
	RefreshAfter time.Duration
}

// New builds a Provider.
func New(config Config) Provider {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return Provider{
		sources: config.Sources, cache: config.Cache, clock: config.Clock, logger: logger,
		refreshAfter: config.RefreshAfter,
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

// Search answers a text search for release groups matching artist and title,
// cache first. Only sources that implement Searcher are tried -- ADR-29's proxy
// does not (Searcher's own doc comment), so this silently has fewer sources than
// Album does, never an error for that reason alone. priority is accepted for the
// same not-yet-scheduled reason as Album's.
func (p Provider) Search(
	ctx context.Context, _ Priority, artist, title string, limit int,
) ([]ReleaseGroupMatch, error) {
	key := searchCacheKey(artist, title, limit)
	if matches, ok := p.cachedSearch(ctx, key); ok {
		return matches, nil
	}

	var errs []error
	for _, source := range p.sources {
		searcher, ok := source.(Searcher)
		if !ok {
			continue
		}
		matches, err := searcher.SearchReleaseGroups(ctx, artist, title, limit)
		if err != nil {
			p.logger.WarnContext(ctx, "musicbrainz source search failed",
				slog.String("source", source.Name()), slog.Any("error", err))
			errs = append(errs, err)
			continue
		}
		p.rememberSearch(ctx, source.Name(), key, matches)
		return matches, nil
	}
	if len(errs) == 0 {
		// No configured source can search at all -- a wiring gap, not a failed
		// lookup, so the caller's chain treats it exactly like "found nothing".
		return nil, nil
	}
	return nil, fmt.Errorf("musicbrainz: no source answered search %q/%q: %w", artist, title, errors.Join(errs...))
}

// SearchByQuery is Search's own free-text counterpart (todo 4.17, ADR-56):
// only sources implementing QuerySearcher are tried, same reasoning as Search
// itself -- the proxy does not implement it either.
func (p Provider) SearchByQuery(ctx context.Context, _ Priority, query string, limit int) ([]ReleaseGroupMatch, error) {
	key := queryCacheKey(query, limit)
	if matches, ok := p.cachedSearch(ctx, key); ok {
		return matches, nil
	}

	var errs []error
	for _, source := range p.sources {
		searcher, ok := source.(QuerySearcher)
		if !ok {
			continue
		}
		matches, err := searcher.SearchReleaseGroupsByQuery(ctx, query, limit)
		if err != nil {
			p.logger.WarnContext(ctx, "musicbrainz source query search failed",
				slog.String("source", source.Name()), slog.Any("error", err))
			errs = append(errs, err)
			continue
		}
		p.rememberSearch(ctx, source.Name(), key, matches)
		return matches, nil
	}
	if len(errs) == 0 {
		// No configured source can search at all -- a wiring gap, not a failed
		// lookup, so the caller's chain treats it exactly like "found nothing".
		return nil, nil
	}
	return nil, fmt.Errorf("musicbrainz: no source answered query search %q: %w", query, errors.Join(errs...))
}

// Artist answers one artist's own detail, cache-first, permanent like Album.
// Only sources implementing ArtistLookup are tried -- same reasoning as
// Search, the proxy's artist shape is unverified live.
func (p Provider) Artist(ctx context.Context, _ Priority, mbid string) (Artist, error) {
	if artist, ok := p.cachedArtist(ctx, mbid); ok {
		return artist, nil
	}

	var errs []error
	for _, source := range p.sources {
		lookup, ok := source.(ArtistLookup)
		if !ok {
			continue
		}
		artist, err := lookup.Artist(ctx, mbid)
		if err != nil {
			p.logger.WarnContext(ctx, "musicbrainz source artist lookup failed",
				slog.String("source", source.Name()), slog.Any("error", err))
			errs = append(errs, err)
			continue
		}
		p.rememberArtist(ctx, source.Name(), mbid, artist)
		return artist, nil
	}
	if len(errs) == 0 {
		return Artist{}, fmt.Errorf("musicbrainz: no source can look up an artist (mbid %s)", mbid)
	}
	return Artist{}, fmt.Errorf("musicbrainz: no source answered artist %s: %w", mbid, errors.Join(errs...))
}

// ArtistReleaseGroups answers an artist's full discography, cache-first. Only
// sources that implement Discography are tried, same reasoning as Search and
// Artist.
func (p Provider) ArtistReleaseGroups(ctx context.Context, _ Priority, artistMBID string) ([]ReleaseGroup, error) {
	if groups, ok := p.cachedDiscography(ctx, artistMBID); ok {
		return groups, nil
	}

	var errs []error
	for _, source := range p.sources {
		discography, ok := source.(Discography)
		if !ok {
			continue
		}
		groups, err := discography.ArtistReleaseGroups(ctx, artistMBID)
		if err != nil {
			p.logger.WarnContext(ctx, "musicbrainz source discography failed",
				slog.String("source", source.Name()), slog.Any("error", err))
			errs = append(errs, err)
			continue
		}
		p.rememberDiscography(ctx, source.Name(), artistMBID, groups)
		return groups, nil
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("musicbrainz: no source can browse a discography (artist %s)", artistMBID)
	}
	return nil, fmt.Errorf("musicbrainz: no source answered discography %s: %w", artistMBID, errors.Join(errs...))
}

func (p Provider) cachedDiscography(ctx context.Context, artistMBID string) ([]ReleaseGroup, bool) {
	entry, err := p.cache.Get(ctx, cacheKindDiscography, artistMBID)
	if err != nil || p.stale(entry) {
		return nil, false
	}
	var groups []ReleaseGroup
	if json.Unmarshal([]byte(entry.Body), &groups) != nil {
		return nil, false
	}
	return groups, true
}

func (p Provider) rememberDiscography(ctx context.Context, source, artistMBID string, groups []ReleaseGroup) {
	body, err := json.Marshal(groups)
	if err != nil {
		return
	}
	if err := p.cache.Set(ctx, cacheKindDiscography, artistMBID,
		metacache.Entry{Source: source, Body: string(body), FetchedAt: p.clock.Now()}); err != nil {
		p.logger.WarnContext(ctx, "musicbrainz: discography cache write failed", slog.Any("error", err))
	}
}

func (p Provider) cachedArtist(ctx context.Context, mbid string) (Artist, bool) {
	entry, err := p.cache.Get(ctx, cacheKindArtist, mbid)
	if err != nil || p.stale(entry) {
		return Artist{}, false
	}
	var artist Artist
	if json.Unmarshal([]byte(entry.Body), &artist) != nil {
		return Artist{}, false
	}
	return artist, true
}

func (p Provider) rememberArtist(ctx context.Context, source, mbid string, artist Artist) {
	body, err := json.Marshal(artist)
	if err != nil {
		return
	}
	if err := p.cache.Set(ctx, cacheKindArtist, mbid,
		metacache.Entry{Source: source, Body: string(body), FetchedAt: p.clock.Now()}); err != nil {
		p.logger.WarnContext(ctx, "musicbrainz: artist cache write failed", slog.Any("error", err))
	}
}

// searchCacheKey normalises a search so "Vutu Doci"/"vutu doci" share one cache
// entry -- the query itself is already case-insensitive, no reason the cache
// should be stricter than the thing it is caching.
func searchCacheKey(artist, title string, limit int) string {
	return fmt.Sprintf("%s\x1f%s\x1f%d",
		strings.ToLower(strings.TrimSpace(artist)), strings.ToLower(strings.TrimSpace(title)), limit)
}

// queryCacheKey is searchCacheKey's own free-text counterpart, prefixed so a
// query search can never collide with a fielded (artist, title) one sharing
// the same cache kind.
func queryCacheKey(query string, limit int) string {
	return fmt.Sprintf("q\x1f%s\x1f%d", strings.ToLower(strings.TrimSpace(query)), limit)
}

func (p Provider) cachedSearch(ctx context.Context, key string) ([]ReleaseGroupMatch, bool) {
	entry, err := p.cache.Get(ctx, cacheKindSearch, key)
	if err != nil || p.stale(entry) {
		return nil, false
	}
	var matches []ReleaseGroupMatch
	if json.Unmarshal([]byte(entry.Body), &matches) != nil {
		return nil, false
	}
	return matches, true
}

func (p Provider) rememberSearch(ctx context.Context, source, key string, matches []ReleaseGroupMatch) {
	body, err := json.Marshal(matches)
	if err != nil {
		return
	}
	if err := p.cache.Set(ctx, cacheKindSearch, key,
		metacache.Entry{Source: source, Body: string(body), FetchedAt: p.clock.Now()}); err != nil {
		p.logger.WarnContext(ctx, "musicbrainz: search cache write failed", slog.Any("error", err))
	}
}

func (p Provider) cached(ctx context.Context, releaseGroupMBID, releaseMBID string) (ReleaseGroup, Release, bool) {
	rgEntry, err := p.cache.Get(ctx, cacheKindReleaseGroup, releaseGroupMBID)
	if err != nil || p.stale(rgEntry) {
		return ReleaseGroup{}, Release{}, false
	}
	releaseEntry, err := p.cache.Get(ctx, cacheKindRelease, releaseMBID)
	if err != nil || p.stale(releaseEntry) {
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

// stale reports whether entry is old enough to force a re-fetch. RefreshAfter
// zero means never -- today's permanent-cache behaviour, so existing callers
// that leave it unset are unaffected.
func (p Provider) stale(entry metacache.Entry) bool {
	return p.refreshAfter > 0 && p.clock.Now().Sub(entry.FetchedAt) >= p.refreshAfter
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
