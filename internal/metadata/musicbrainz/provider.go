package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/metadata/metaresolve"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound is every source answering that an id does not exist: retired, not merely unreachable.
var ErrNotFound = errors.New("musicbrainz: not found at any source")

// Priority is ADR-29's request-priority ladder: within one source's rate-limit bucket a
// lower level takes the next token first (httpx.WithPriority), so a user never waits behind a scan.
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
	cacheKindArtist = "musicbrainz:artist:v2"
	// cacheKindDiscography is one artist's full release-group list, keyed on
	// their MBID -- refreshed by the same RefreshAfter policy as everything
	// else, since a discography grows over time unlike a release group's own
	// fixed facts.
	cacheKindDiscography = "musicbrainz:discography"
	// cacheKindReleases is every release of one release group, tracklists included.
	cacheKindReleases = "musicbrainz:releases"
	// cacheKindRecordingSearch is a recording search's result set, keyed like cacheKindSearch.
	cacheKindRecordingSearch = "musicbrainz:recording-search"
	// cacheKindRecording is one recording with the release groups it is on, keyed on its MBID.
	cacheKindRecording = "musicbrainz:recording"
	// cacheKindArtistSearch is an artist search's result set, keyed on query and limit.
	cacheKindArtistSearch = "musicbrainz:artist-search"
)

// Provider is ADR-29 end to end: try each source in priority order, falling
// through on failure (ADR-13's breaker already skips a source that is down
// without this package needing to ask it first), permanent cache in front so a
// repeat lookup costs nothing.
type Provider struct {
	sources      []Source
	liveOrder    func(library int64) []string
	cache        metacache.Cache
	clock        clock.Clock
	logger       *slog.Logger
	refreshAfter time.Duration
}

type libraryKey struct{}

// ForLibrary makes every lookup under ctx follow that music library's own source order; 0 is no library, the default order.
func ForLibrary(ctx context.Context, categoryID int64) context.Context {
	return context.WithValue(ctx, libraryKey{}, categoryID)
}

// LibraryOf answers the library ForLibrary put on ctx, 0 for none.
func LibraryOf(ctx context.Context) int64 {
	categoryID, _ := ctx.Value(libraryKey{}).(int64)
	return categoryID
}

// orderedSources answers the sources to try, in the library's order, for one call; read live, so a reorder in the
// interface applies at once. A source the order leaves out is dropped, a name no source provides ignored.
func (p Provider) orderedSources(library int64) []Source {
	if p.liveOrder == nil {
		return p.sources
	}
	order := p.liveOrder(library)
	if len(order) == 0 {
		return p.sources
	}
	byName := make(map[string]Source, len(p.sources))
	for _, source := range p.sources {
		byName[source.Name()] = source
	}
	ordered := make([]Source, 0, len(p.sources))
	for _, name := range order {
		if source, found := byName[name]; found {
			ordered = append(ordered, source)
			delete(byName, name)
		}
	}
	return ordered
}

// route is one lookup's sources in its library's order: the order names its cache entries, so one library's answer is
// never served to another that ranks the sources differently (documentation/metadata-providers.md).
type route struct {
	sources []Source
	// tag suffixes every cache kind: the source names in order.
	tag string
	// lead is the first source able to answer this lookup; an entry cached before orders had tags is served only from it.
	lead string
}

// routeFor reads the order once for a lookup of capability C.
func routeFor[C any](ctx context.Context, p Provider) route {
	sources := p.orderedSources(LibraryOf(ctx))
	names := make([]string, len(sources))
	lead := ""
	for index, source := range sources {
		names[index] = source.Name()
		if _, capable := any(source).(C); capable && lead == "" {
			lead = source.Name()
		}
	}
	return route{sources: sources, tag: "@" + strings.Join(names, ">"), lead: lead}
}

// Config is what a Provider needs. Sources are tried in order -- ADR-29 puts the
// Servarr proxy first, direct MusicBrainz second, but this package does not care
// which or how many.
type Config struct {
	Sources []Source
	// LiveOrder answers the source names to try, in order, for a music library (0: none, the default order); called
	// once per lookup. Optional: nil keeps Sources' own order.
	LiveOrder func(library int64) []string
	Cache     metacache.Cache
	Clock     clock.Clock
	Logger    *slog.Logger
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
		sources: config.Sources, liveOrder: config.LiveOrder, cache: config.Cache, clock: config.Clock, logger: logger,
		refreshAfter: config.RefreshAfter,
	}
}

// Album answers a release group and one of its releases, cache first, at priority (see Priority).
func (p Provider) Album(
	ctx context.Context, priority Priority, releaseGroupMBID, releaseMBID string,
) (ReleaseGroup, Release, error) {
	ctx = httpx.WithPriority(ctx, httpx.Priority(priority))
	r := routeFor[Source](ctx, p)
	if rg, release, ok := p.cached(ctx, r, releaseGroupMBID, releaseMBID); ok {
		return rg, release, nil
	}

	type album struct {
		group   ReleaseGroup
		release Release
	}
	answer, answered, errs := firstAnswer(ctx, p, r, func(ctx context.Context, source Source) (album, error) {
		rg, release, err := source.Album(ctx, releaseGroupMBID, releaseMBID)
		return album{rg, release}, err
	})
	if answered {
		p.remember(ctx, r, answer.Source.Name(), answer.Value.group, answer.Value.release)
		return answer.Value.group, answer.Value.release, nil
	}
	if len(errs) > 0 && !slices.ContainsFunc(errs, func(err error) bool { return !notFound(err) }) {
		return ReleaseGroup{}, Release{}, fmt.Errorf("musicbrainz: %s/%s: %w: %w",
			releaseGroupMBID, releaseMBID, ErrNotFound, errors.Join(errs...))
	}
	return ReleaseGroup{}, Release{}, fmt.Errorf("musicbrainz: no source answered for %s/%s: %w",
		releaseGroupMBID, releaseMBID, errors.Join(errs...))
}

// firstAnswer asks the route's sources able to answer C, in order, and stops at the first answer (metaresolve.FallThrough);
// every failure but a 404 is logged under the source that gave it.
func firstAnswer[C, T any](
	ctx context.Context, p Provider, r route, ask func(ctx context.Context, capable C) (T, error),
) (metaresolve.Answer[Source, T], bool, []error) {
	return metaresolve.FallThrough(ctx, r.sources, func(ctx context.Context, source Source) (T, bool, error) {
		capable, ok := any(source).(C)
		if !ok {
			var none T
			return none, false, nil
		}
		value, err := ask(ctx, capable)
		if err != nil && !notFound(err) {
			p.logger.WarnContext(ctx, "musicbrainz source failed", slog.String("source", source.Name()), slog.Any("error", err))
		}
		return value, true, err
	})
}

// errReleaseNotInGroup is the proxy's own way of saying a release does not exist: it answers the
// release group and leaves the release out, where direct MusicBrainz answers 404.
var errReleaseNotInGroup = errors.New("release not under its release group")

// notFound reports a source answering that the id itself does not exist.
func notFound(err error) bool {
	var statusErr *httpx.StatusError
	return errors.As(err, &statusErr) && statusErr.Status == http.StatusNotFound || errors.Is(err, errReleaseNotInGroup)
}

// Search answers a text search for release groups matching artist and title,
// cache first. Only sources that implement Searcher are tried -- ADR-29's proxy
// does not (Searcher's own doc comment), so this silently has fewer sources than
// Album does, never an error for that reason alone.
func (p Provider) Search(
	ctx context.Context, priority Priority, artist, title string, limit int,
) ([]ReleaseGroupMatch, error) {
	ctx = httpx.WithPriority(ctx, httpx.Priority(priority))
	r := routeFor[Searcher](ctx, p)
	key := searchCacheKey(artist, title, limit)
	if matches, ok := cachedJSON[[]ReleaseGroupMatch](ctx, p, r, cacheKindSearch, key); ok {
		return matches, nil
	}

	answer, answered, errs := firstAnswer(ctx, p, r, func(ctx context.Context, searcher Searcher) ([]ReleaseGroupMatch, error) {
		return searcher.SearchReleaseGroups(ctx, artist, title, limit)
	})
	if answered {
		rememberJSON(ctx, p, r, answer.Source.Name(), cacheKindSearch, key, answer.Value)
		return answer.Value, nil
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
func (p Provider) SearchByQuery(ctx context.Context, priority Priority, query string, limit int) ([]ReleaseGroupMatch, error) {
	ctx = httpx.WithPriority(ctx, httpx.Priority(priority))
	r := routeFor[QuerySearcher](ctx, p)
	key := queryCacheKey(query, limit)
	if matches, ok := cachedJSON[[]ReleaseGroupMatch](ctx, p, r, cacheKindSearch, key); ok {
		return matches, nil
	}

	answer, answered, errs := firstAnswer(ctx, p, r, func(ctx context.Context, searcher QuerySearcher) ([]ReleaseGroupMatch, error) {
		return searcher.SearchReleaseGroupsByQuery(ctx, query, limit)
	})
	if answered {
		rememberJSON(ctx, p, r, answer.Source.Name(), cacheKindSearch, key, answer.Value)
		return answer.Value, nil
	}
	if len(errs) == 0 {
		// No configured source can search at all -- a wiring gap, not a failed
		// lookup, so the caller's chain treats it exactly like "found nothing".
		return nil, nil
	}
	return nil, fmt.Errorf("musicbrainz: no source answered query search %q: %w", query, errors.Join(errs...))
}

// Releases answers every release of a release group with its tracklist, cache first, from
// the first source able to list them. Album identification asks it for every candidate.
func (p Provider) Releases(ctx context.Context, priority Priority, releaseGroupMBID string) ([]Release, error) {
	ctx = httpx.WithPriority(ctx, httpx.Priority(priority))
	r := routeFor[ReleaseLister](ctx, p)
	if releases, ok := cachedJSON[[]Release](ctx, p, r, cacheKindReleases, releaseGroupMBID); ok {
		return releases, nil
	}
	answer, answered, errs := firstAnswer(ctx, p, r, func(ctx context.Context, lister ReleaseLister) ([]Release, error) {
		return lister.Releases(ctx, releaseGroupMBID)
	})
	if answered {
		rememberJSON(ctx, p, r, answer.Source.Name(), cacheKindReleases, releaseGroupMBID, answer.Value)
		return answer.Value, nil
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("musicbrainz: no source can list releases (release group %s)", releaseGroupMBID)
	}
	return nil, fmt.Errorf("musicbrainz: no source listed releases of %s: %w", releaseGroupMBID, errors.Join(errs...))
}

// SearchArtists answers artists matching a name, cache first, from the first
// source able to search them -- what adding an artist the library has never
// seen starts from (revision 7.1). Only ArtistSearcher sources are tried, the
// same optional-capability shape SearchByQuery already uses; no source able to
// search at all reads as "found nothing", since that is a wiring gap rather
// than a failed lookup and the caller cannot act on the difference.
func (p Provider) SearchArtists(ctx context.Context, priority Priority, query string, limit int) ([]ArtistMatch, error) {
	ctx = httpx.WithPriority(ctx, httpx.Priority(priority))
	r := routeFor[ArtistSearcher](ctx, p)
	key := queryCacheKey(query, limit)
	if matches, ok := cachedJSON[[]ArtistMatch](ctx, p, r, cacheKindArtistSearch, key); ok {
		return matches, nil
	}

	answer, answered, errs := firstAnswer(ctx, p, r, func(ctx context.Context, searcher ArtistSearcher) ([]ArtistMatch, error) {
		return searcher.SearchArtists(ctx, query, limit)
	})
	if answered {
		rememberJSON(ctx, p, r, answer.Source.Name(), cacheKindArtistSearch, key, answer.Value)
		return answer.Value, nil
	}
	if len(errs) == 0 {
		return nil, nil
	}
	return nil, fmt.Errorf("musicbrainz: no source answered artist search %q: %w", query, errors.Join(errs...))
}

// SearchRecordings answers recordings matching a track title (and artist, when known),
// each with the release groups it appears on, cache first. Only RecordingSearcher sources are tried.
func (p Provider) SearchRecordings(ctx context.Context, priority Priority, artist, title string, limit int) ([]RecordingMatch, error) {
	ctx = httpx.WithPriority(ctx, httpx.Priority(priority))
	r := routeFor[RecordingSearcher](ctx, p)
	key := searchCacheKey(artist, title, limit)
	if matches, ok := cachedJSON[[]RecordingMatch](ctx, p, r, cacheKindRecordingSearch, key); ok {
		return matches, nil
	}

	answer, answered, errs := firstAnswer(ctx, p, r, func(ctx context.Context, searcher RecordingSearcher) ([]RecordingMatch, error) {
		return searcher.SearchRecordingReleaseGroups(ctx, artist, title, limit)
	})
	if answered {
		rememberJSON(ctx, p, r, answer.Source.Name(), cacheKindRecordingSearch, key, answer.Value)
		return answer.Value, nil
	}
	if len(errs) == 0 {
		return nil, nil
	}
	return nil, fmt.Errorf("musicbrainz: no source answered recording search %q/%q: %w", artist, title, errors.Join(errs...))
}

// Recording answers one recording and the release groups it is on, cache first; ErrNotFound when
// every source that can look recordings up answered 404. Only RecordingLookup sources are tried.
func (p Provider) Recording(ctx context.Context, priority Priority, mbid string) (RecordingMatch, error) {
	ctx = httpx.WithPriority(ctx, httpx.Priority(priority))
	r := routeFor[RecordingLookup](ctx, p)
	if recording, ok := cachedJSON[RecordingMatch](ctx, p, r, cacheKindRecording, mbid); ok {
		return recording, nil
	}
	answer, answered, errs := firstAnswer(ctx, p, r, func(ctx context.Context, lookup RecordingLookup) (RecordingMatch, error) {
		return lookup.Recording(ctx, mbid)
	})
	if answered {
		rememberJSON(ctx, p, r, answer.Source.Name(), cacheKindRecording, mbid, answer.Value)
		return answer.Value, nil
	}
	if len(errs) > 0 && !slices.ContainsFunc(errs, func(err error) bool { return !notFound(err) }) {
		return RecordingMatch{}, ErrNotFound
	}
	return RecordingMatch{}, fmt.Errorf("musicbrainz: no source answered recording %s: %w", mbid, errors.Join(errs...))
}

// Artist answers one artist's own detail, cache-first, permanent like Album.
// Only sources implementing ArtistLookup are tried -- same reasoning as
// Search, the proxy's artist shape is unverified live.
func (p Provider) Artist(ctx context.Context, priority Priority, mbid string) (Artist, error) {
	ctx = httpx.WithPriority(ctx, httpx.Priority(priority))
	r := routeFor[ArtistLookup](ctx, p)
	if artist, ok := cachedJSON[Artist](ctx, p, r, cacheKindArtist, mbid); ok {
		return artist, nil
	}

	answer, answered, errs := firstAnswer(ctx, p, r, func(ctx context.Context, lookup ArtistLookup) (Artist, error) {
		return lookup.Artist(ctx, mbid)
	})
	if answered {
		rememberJSON(ctx, p, r, answer.Source.Name(), cacheKindArtist, mbid, answer.Value)
		return answer.Value, nil
	}
	if len(errs) == 0 {
		return Artist{}, fmt.Errorf("musicbrainz: no source can look up an artist (mbid %s)", mbid)
	}
	return Artist{}, fmt.Errorf("musicbrainz: no source answered artist %s: %w", mbid, errors.Join(errs...))
}

// ArtistReleaseGroups answers an artist's full discography, cache-first. Only
// sources that implement Discography are tried, same reasoning as Search and
// Artist.
func (p Provider) ArtistReleaseGroups(ctx context.Context, priority Priority, artistMBID string) ([]ReleaseGroup, error) {
	ctx = httpx.WithPriority(ctx, httpx.Priority(priority))
	r := routeFor[Discography](ctx, p)
	if groups, ok := cachedJSON[[]ReleaseGroup](ctx, p, r, cacheKindDiscography, artistMBID); ok {
		return groups, nil
	}

	answer, answered, errs := firstAnswer(ctx, p, r, func(ctx context.Context, discography Discography) ([]ReleaseGroup, error) {
		return discography.ArtistReleaseGroups(ctx, artistMBID)
	})
	if answered {
		rememberJSON(ctx, p, r, answer.Source.Name(), cacheKindDiscography, artistMBID, answer.Value)
		return answer.Value, nil
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("musicbrainz: no source can browse a discography (artist %s)", artistMBID)
	}
	return nil, fmt.Errorf("musicbrainz: no source answered discography %s: %w", artistMBID, errors.Join(errs...))
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

// cachedEntry reads the route's own entry; failing that, an entry cached before orders had tags, when the route's lead
// source gave it, since that answer is the same whichever order ranks that source first.
func (p Provider) cachedEntry(ctx context.Context, r route, kind, key string) (metacache.Entry, bool) {
	if wantsFresh(ctx) {
		return metacache.Entry{}, false
	}
	if entry, err := p.cache.Get(ctx, kind+r.tag, key); err == nil && !p.stale(entry) {
		return entry, true
	}
	entry, err := p.cache.Get(ctx, kind, key)
	if err != nil || p.stale(entry) || r.lead == "" || entry.Source != r.lead {
		return metacache.Entry{}, false
	}
	return entry, true
}

func (p Provider) cached(ctx context.Context, r route, releaseGroupMBID, releaseMBID string) (ReleaseGroup, Release, bool) {
	rgEntry, ok := p.cachedEntry(ctx, r, cacheKindReleaseGroup, releaseGroupMBID)
	if !ok {
		return ReleaseGroup{}, Release{}, false
	}
	releaseEntry, ok := p.cachedEntry(ctx, r, cacheKindRelease, releaseMBID)
	if !ok {
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

type freshKey struct{}

// WithFresh makes every lookup under ctx skip the cache and ask a source, then cache that answer:
// what a person's "refresh metadata" means, as opposed to RefreshAfter's staleness policy.
func WithFresh(ctx context.Context) context.Context { return context.WithValue(ctx, freshKey{}, true) }

func wantsFresh(ctx context.Context) bool {
	fresh, _ := ctx.Value(freshKey{}).(bool)
	return fresh
}

// stale reports whether entry is old enough to force a re-fetch. RefreshAfter
// zero means never -- today's permanent-cache behaviour, so existing callers
// that leave it unset are unaffected.
func (p Provider) stale(entry metacache.Entry) bool {
	return p.refreshAfter > 0 && p.clock.Now().Sub(entry.FetchedAt) >= p.refreshAfter
}

func (p Provider) remember(ctx context.Context, r route, source string, rg ReleaseGroup, release Release) {
	rememberJSON(ctx, p, r, source, cacheKindReleaseGroup, rg.MBID, rg)
	rememberJSON(ctx, p, r, source, cacheKindRelease, release.MBID, release)
}

// cachedJSON reads the route's cache entry as T; a refresh, a miss, a stale entry or an undecodable body is a miss.
func cachedJSON[T any](ctx context.Context, p Provider, r route, kind, key string) (T, bool) {
	var value T
	entry, ok := p.cachedEntry(ctx, r, kind, key)
	if !ok || json.Unmarshal([]byte(entry.Body), &value) != nil {
		return value, false
	}
	return value, true
}

// rememberJSON stores value under the route's order; a failed write only costs a later re-fetch.
func rememberJSON[T any](ctx context.Context, p Provider, r route, source, kind, key string, value T) {
	body, err := json.Marshal(value)
	if err != nil {
		return
	}
	if err := p.cache.Set(ctx, kind+r.tag, key, metacache.Entry{Source: source, Body: string(body), FetchedAt: p.clock.Now()}); err != nil {
		p.logger.WarnContext(ctx, "musicbrainz: cache write failed", slog.String("kind", kind), slog.Any("error", err))
	}
}

// cacheKindLabel is one label's details; cacheKindLabelCatalogue its release groups.
const (
	cacheKindLabel          = "musicbrainz:label"
	cacheKindLabelCatalogue = "musicbrainz:label-catalogue"
)

// Label answers one label's details, cache-first, from the sources that can look one up.
func (p Provider) Label(ctx context.Context, priority Priority, mbid string) (Label, error) {
	ctx = httpx.WithPriority(ctx, httpx.Priority(priority))
	r := routeFor[LabelLookup](ctx, p)
	if label, ok := cachedJSON[Label](ctx, p, r, cacheKindLabel, mbid); ok {
		return label, nil
	}
	answer, answered, errs := firstAnswer(ctx, p, r, func(ctx context.Context, lookup LabelLookup) (Label, error) {
		return lookup.Label(ctx, mbid)
	})
	if answered {
		rememberJSON(ctx, p, r, answer.Source.Name(), cacheKindLabel, mbid, answer.Value)
		return answer.Value, nil
	}
	if len(errs) == 0 {
		return Label{}, fmt.Errorf("musicbrainz: no source can look up a label (mbid %s)", mbid)
	}
	return Label{}, fmt.Errorf("musicbrainz: no source answered label %s: %w", mbid, errors.Join(errs...))
}

// LabelReleaseGroups answers what a label released, cache-first, from the sources that can browse it.
func (p Provider) LabelReleaseGroups(ctx context.Context, priority Priority, labelMBID string) ([]ReleaseGroup, error) {
	ctx = httpx.WithPriority(ctx, httpx.Priority(priority))
	r := routeFor[LabelCatalogue](ctx, p)
	if groups, ok := cachedJSON[[]ReleaseGroup](ctx, p, r, cacheKindLabelCatalogue, labelMBID); ok {
		return groups, nil
	}
	answer, answered, errs := firstAnswer(ctx, p, r, func(ctx context.Context, catalogue LabelCatalogue) ([]ReleaseGroup, error) {
		return catalogue.LabelReleaseGroups(ctx, labelMBID)
	})
	if answered {
		rememberJSON(ctx, p, r, answer.Source.Name(), cacheKindLabelCatalogue, labelMBID, answer.Value)
		return answer.Value, nil
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("musicbrainz: no source can browse a label (mbid %s)", labelMBID)
	}
	return nil, fmt.Errorf("musicbrainz: no source answered label catalogue %s: %w", labelMBID, errors.Join(errs...))
}
