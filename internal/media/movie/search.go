package movie

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/releasefacts"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrNotSearchable reports an item the movie search does not handle: not the one wanted item of a movie.
var ErrNotSearchable = works.ErrNotSearchable

// Item states the cutoff check and the search move a movie between.
const (
	ItemStateUpgradable = works.StateUpgradable
	ItemStateComplete   = works.StateComplete
)

// ItemSearchResult is one movie's search; the shared result under the movie name.
type ItemSearchResult = judge.Result

// Search is the movie specialisation of the shared candidate pipeline (works.Search): what a movie wants, the queries
// Radarr asks, and whether a release is the film.
type Search struct {
	Desired    library.Desired
	Reader     library.SeriesReader
	Candidates statistics.Candidates
	Settings   *settings.Cache
	Clock      clock.Clock
	Blocklist  *library.BlocklistStore
	// Sightings, when set, remember what RSS showed for "fresh" (ADR-55).
	Sightings *library.Sightings
	// ReleaseFilters, when set, hold every release against the movie's own required, denied and preferred words and groups.
	ReleaseFilters *library.ReleaseFilters
	// IndexerDeadline bounds each tier's fan-out; zero is judge.GroupDeadline.
	IndexerDeadline time.Duration
}

func (s Search) shared() works.Search[wanted] {
	return works.Search[wanted]{
		Name: "movie", Spec: movieSpec{s}, Desired: s.Desired, Candidates: s.Candidates, Settings: s.Settings, Clock: s.Clock,
		Blocklist: s.Blocklist, Sightings: s.Sightings, ReleaseFilters: s.ReleaseFilters, IndexerDeadline: s.IndexerDeadline,
	}
}

// SearchItem searches one movie's item: tiers widen until one finds a release of the movie.
func (s Search) SearchItem(ctx context.Context, itemID int64, conns []judge.Conn, at time.Time) (ItemSearchResult, error) {
	return s.shared().SearchItem(ctx, itemID, conns, at)
}

// SearchPart implements works.Searcher; a movie has no parts, so it is SearchItem.
func (s Search) SearchPart(ctx context.Context, itemID int64, _ int, conns []judge.Conn, at time.Time) (ItemSearchResult, error) {
	return s.shared().SearchItem(ctx, itemID, conns, at)
}

// JudgeFeed judges releases a feed listed against one wanted movie, exactly as a search's would be.
func (s Search) JudgeFeed(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (ItemSearchResult, error) {
	return s.shared().JudgeFeed(ctx, itemID, conn, raws, at)
}

// wanted is what a search needs to know of a movie it looks for.
type wanted struct {
	title     string
	aliases   []string
	titles    map[string]bool
	year      int
	tmdb      string
	imdb      string
	runtimeMs int64
	// owned is, for an upgrade, the file the movie has now: a release must strictly beat it.
	owned *score.Facts
}

// movieSpec is works.Spec for movies.
type movieSpec struct{ search Search }

// Wanted implements works.Spec: a movie wants a file until it has one, then one that beats a file below its cutoff.
func (m movieSpec) Wanted(ctx context.Context, item librarydb.DesiredItem, _ int, now time.Time) (wanted, bool, error) {
	if err := works.WholeItem(item); err != nil {
		return wanted{}, false, err
	}
	basics, err := m.search.Reader.Basics(ctx, item.WorkID)
	if err != nil {
		return wanted{}, false, err
	}
	ids, err := m.search.Reader.ProviderIDs(ctx, item.WorkID)
	if err != nil {
		return wanted{}, false, err
	}
	w := wanted{title: basics.Title, aliases: basics.Aliases, year: int(basics.Year), tmdb: ids[ProviderTMDB], imdb: ids[ProviderIMDb], runtimeMs: basics.RuntimeMs}
	w.titles = w.titleSet()
	if item.PresentCount == 0 {
		return w, true, nil
	}
	if m.search.Settings == nil {
		return w, false, nil
	}
	files, err := itemFiles(ctx, m.search.Reader, item.ID)
	if err != nil {
		return w, false, err
	}
	if owned, below := works.BelowCutoff(m.search.Settings.Snapshot(), item.CategoryID, files, works.UpgradeNewest(ctx, now))[""]; below {
		w.owned = &owned.Facts
	}
	return w, w.owned != nil, nil
}

// Owned implements works.Spec.
func (movieSpec) Owned(w wanted) map[int]score.Facts {
	if w.owned == nil {
		return nil
	}
	return map[int]score.Facts{0: *w.owned}
}

// Queries implements works.Spec.
func (movieSpec) Queries(w wanted) [][]judge.Query { return w.queries() }

// Read implements works.Spec: a release is the movie when it names one of its titles in its year, give or take one.
func (movieSpec) Read(w wanted, raw indexer.RawItem, conn judge.Conn, _ time.Time) (works.Release, bool) {
	info, ok := parsemovie.Parse(raw.Title)
	if !ok || isExtra(raw.Title) || !w.isTheMovie(info) {
		return works.Release{}, false
	}
	facts := releasefacts.FromRelease(info.Claims, info.ReleaseGroup, raw, conn.Impl)
	releasefacts.EstimateBitrate(&facts, raw.SizeBytes, 1, w.runtimeMs)
	return works.Release{Facts: facts, ReleaseGroup: info.ReleaseGroup, Revised: info.Claims.Version > 1}, true
}

// itemFiles are a movie's files as the cutoff check and the upgrade search judge them.
func itemFiles(ctx context.Context, reader library.SeriesReader, itemID int64) ([]works.File, error) {
	files, err := reader.ItemFiles(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]works.File, len(files))
	for index, file := range files {
		out[index] = works.File{
			MediaObjectID: file.File.MediaObjectID, AddedAt: file.AddedAt, Measured: file.Measured, Monitored: file.Monitored,
			Facts: releasefacts.FromFile(file.File),
		}
	}
	return out, nil
}

// titleSet is every name a release may call the movie by, cleaned for comparison.
func (w wanted) titleSet() map[string]bool {
	names := map[string]bool{}
	for _, title := range append([]string{w.title}, w.aliases...) {
		if clean := parsetv.CleanTitle(title); clean != "" {
			names[clean] = true
		}
	}
	return names
}

// queries builds the tiers of one movie's search: the id queries first, then text in the spellings releases use.
func (w wanted) queries() [][]judge.Query {
	var tiers [][]judge.Query
	var byID []judge.Query
	if w.imdb != "" {
		byID = append(byID, judge.Query{Function: "movie", Extra: map[string]string{"imdbid": strings.TrimPrefix(w.imdb, "tt")}, Needs: []string{"imdbid"}})
	}
	if w.tmdb != "" {
		byID = append(byID, judge.Query{Function: "movie", Extra: map[string]string{"tmdbid": w.tmdb}, Needs: []string{"tmdbid"}})
	}
	if len(byID) > 0 {
		tiers = append(tiers, byID)
	}
	names := append([]string{w.title}, w.aliases...)
	if len(names) > 3 {
		names = names[:3]
	}
	var withYear, bare []judge.Query
	for _, name := range names {
		clean := releasefacts.CleanQuery(name)
		if clean == "" {
			continue
		}
		bare = append(bare, judge.Query{Function: "search", Text: clean})
		if w.year > 0 {
			withYear = append(withYear, judge.Query{Function: "search", Text: fmt.Sprintf("%s %d", clean, w.year)})
		}
	}
	if len(withYear) > 0 {
		tiers = append(tiers, withYear)
	}
	return append(tiers, bare)
}

// isTheMovie says a release names this movie: one of its titles, in its year give or take one. A release with no year is
// not taken: a title alone cannot tell a film from its remake.
func (w wanted) isTheMovie(info parsemovie.Info) bool {
	named := false
	for _, title := range append([]string{info.Title}, info.AllTitles...) {
		if w.titles[parsetv.CleanTitle(title)] {
			named = true
		}
	}
	return named && yearsAgree(w.year, info.Year)
}

// extraMarker names a release of bonus material, which carries the film's title but is not the film.
var extraMarker = regexp.MustCompile(`(?i)\[extras?\b|[._ -]extras[._ -]|featurettes?|behind[._ -]the[._ -]scenes|deleted[._ -]scenes`)

func isExtra(title string) bool { return extraMarker.MatchString(title) }
