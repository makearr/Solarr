package tv

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/releasefacts"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrNotSearchable reports a desired item the TV search does not handle on its own (a single episode item).
var ErrNotSearchable = errors.New("tv: search: only season items are searched on their own")

// searchTVParams are what a structured id query needs the indexer to accept.
var (
	idParams      = []string{"tvdbid", "season"}
	idParamsOneEp = []string{"tvdbid", "season", "ep"}
)

// ItemSearchResult is one season item's search; the shared result under TV's name.
type ItemSearchResult = judge.Result

// IndexerError is one indexer's own failure; the shared type under TV's name.
type IndexerError = judge.IndexerError

// Search is the TV candidate pipeline over one season item at a time.
type Search struct {
	Desired    library.Desired
	Series     library.SeriesReader
	Candidates statistics.Candidates
	Settings   *settings.Cache
	Clock      clock.Clock
	Blocklist  *library.BlocklistStore
	// Sightings, when set, remember what RSS showed for "fresh" (ADR-55).
	Sightings *library.Sightings
	// ReleaseFilters, when set, hold every release against the series' own required, denied and preferred words and groups.
	ReleaseFilters *library.ReleaseFilters
	// IndexerDeadline bounds each tier's fan-out; zero is judge.GroupDeadline.
	IndexerDeadline time.Duration
}

// wanted is what a season item is missing: the monitored episodes that have aired and have no file.
type wanted struct {
	season   int
	missing  map[int]library.SeriesEpisode // by episode number
	allCount int
	title    string
	aliases  []string
	tvdb     string
	year     int64
	// anime series are also searched by the absolute number releases carry; absolute maps it to the episode number.
	anime    bool
	absolute map[int]int
	// runtimeMs is an episode's usual length, to turn a release's size into a bitrate; zero when nothing says.
	runtimeMs int64
	// owned is, for an upgrade, what the file each wanted episode has now is: a release must strictly beat it.
	owned map[int]score.Facts
	// names are every title a release may call the series by, cleaned for comparison.
	names map[string]bool
}

func seasonNumber(mediaRef string) (int, bool) {
	number, err := strconv.Atoi(strings.TrimPrefix(mediaRef, "s"))
	return number, err == nil && strings.HasPrefix(mediaRef, "s")
}

func (s Search) wantedFor(ctx context.Context, item librarydb.DesiredItem, only int) (wanted, error) {
	if item.SatisfactionKind != "SET" {
		return wanted{}, ErrNotSearchable
	}
	season, ok := seasonNumber(item.MediaRef)
	if !ok {
		return wanted{}, ErrNotSearchable
	}
	now := s.Clock.Now()
	detail, err := s.Series.Get(ctx, item.CategoryID, item.WorkID, now)
	if err != nil {
		return wanted{}, fmt.Errorf("series %d: %w", item.WorkID, err)
	}
	w := wanted{
		season: season, missing: map[int]library.SeriesEpisode{}, title: detail.Summary.Title, aliases: detail.Aliases,
		tvdb: detail.IDs[ProviderTVDB], year: detail.Summary.Year, absolute: map[int]int{},
		anime: SeriesType(detail.Summary.Fields["series_type"]) == SeriesAnime,
	}
	var runtimeSum, runtimeCount int64
	for _, episode := range detail.Episodes {
		if episode.Season != season {
			continue
		}
		w.allCount++
		if episode.RuntimeMs > 0 {
			runtimeSum, runtimeCount = runtimeSum+episode.RuntimeMs, runtimeCount+1
		}
		if absolute, err := strconv.Atoi(episode.Absolute); err == nil && absolute > 0 {
			w.absolute[absolute] = episode.Number
		}
		// An episode with neither a time nor a date is not asked for, whatever its row says of being announced.
		aired := (episode.AvailableAt > 0 || episode.Date != "") &&
			library.PartOut(episode.AvailableAt, episode.AvailableGrace, episode.Date, "", now)
		switch {
		case only > 0:
			if episode.Number == only {
				w.missing[episode.Number] = episode
			}
		case episode.Monitored && !episode.Present && aired:
			w.missing[episode.Number] = episode
		}
	}
	if err := s.addUpgrades(ctx, item, only, detail, &w); err != nil {
		return wanted{}, err
	}
	w.names = w.titles()
	switch {
	case runtimeCount > 0:
		w.runtimeMs = runtimeSum / runtimeCount
	default:
		w.runtimeMs = detail.RuntimeMs
	}
	return w, nil
}

// addUpgrades makes the monitored episodes whose file misses the library's cutoff the wanted ones when the season has
// no aired gap left to fill: an UPGRADABLE season, or one that is only partial because episodes have yet to air.
func (s Search) addUpgrades(ctx context.Context, item librarydb.DesiredItem, only int, detail library.SeriesDetailRow, w *wanted) error {
	if only > 0 || len(w.missing) > 0 || s.Settings == nil || (item.State != ItemStateUpgradable && item.State != "PARTIAL") {
		return nil
	}
	files, err := seasonFiles(ctx, s.Series, item.ID)
	if err != nil {
		return err
	}
	below := works.BelowCutoff(s.Settings.Snapshot(), item.CategoryID, files, s.Clock.Now().Add(-works.UpgradeMinFileAge).Unix())
	for _, episode := range detail.Episodes {
		if file, isBelow := below[strconv.Itoa(episode.Number)]; isBelow && episode.Season == w.season && episode.Monitored {
			w.missing[episode.Number] = episode
			if w.owned == nil {
				w.owned = map[int]score.Facts{}
			}
			w.owned[episode.Number] = file.Facts
		}
	}
	return nil
}

// seasonFiles are a season item's files as the cutoff check and the upgrade search judge them, one per episode a file
// holds; Ref is the episode's number within the season.
func seasonFiles(ctx context.Context, reader library.SeriesReader, itemID int64) ([]works.File, error) {
	files, err := reader.SeasonFiles(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]works.File, len(files))
	for index, file := range files {
		out[index] = works.File{
			MediaObjectID: file.File.MediaObjectID, Ref: strconv.Itoa(file.Episode), AddedAt: file.AddedAt, Measured: file.Measured,
			Monitored: file.Monitored, Facts: FileFacts(file.File),
		}
	}
	return out, nil
}

// titles are every name a release may call the series by, cleaned for comparison.
func (w wanted) titles() map[string]bool {
	names := map[string]bool{}
	for _, title := range append([]string{w.title}, w.aliases...) {
		if clean := parsetv.CleanTitle(title); clean != "" {
			names[clean] = true
		}
	}
	return names
}

// queries builds the tiers of one season's search: the id query first, then text in the spellings releases use.
func (w wanted) queries() [][]judge.Query {
	season := strconv.Itoa(w.season)
	oneEpisode := len(w.missing) == 1
	var episodeNumber int
	for number := range w.missing {
		episodeNumber = number
	}

	var tiers [][]judge.Query
	if w.tvdb != "" {
		extra := map[string]string{"tvdbid": w.tvdb, "season": season}
		needs := idParams
		if oneEpisode {
			extra["ep"] = strconv.Itoa(episodeNumber)
			needs = idParamsOneEp
		}
		tiers = append(tiers, []judge.Query{{Function: "tvsearch", Extra: extra, Needs: needs}})
	}

	names := append([]string{w.title}, w.aliases...)
	if len(names) > 3 {
		names = names[:3]
	}
	var text []judge.Query
	for _, name := range names {
		clean := releasefacts.CleanQuery(name)
		if oneEpisode {
			text = append(text, judge.Query{Function: "search", Text: fmt.Sprintf("%s S%02dE%02d", clean, w.season, episodeNumber)})
		} else {
			text = append(text, judge.Query{Function: "search", Text: fmt.Sprintf("%s S%02d", clean, w.season)})
		}
	}
	tiers = append(tiers, text)
	if w.anime {
		tiers = append(tiers, w.absoluteQueries(names))
	}

	var spelled []judge.Query
	for _, name := range names {
		spelled = append(spelled, judge.Query{Function: "search", Text: fmt.Sprintf("%s Season %d", releasefacts.CleanQuery(name), w.season)})
	}
	tiers = append(tiers, spelled)
	if w.anime {
		for _, tier := range tiers {
			for index := range tier {
				tier[index].MoreCategories = []int{animeCategory}
			}
		}
	}
	return tiers
}

// animeCategory is Newznab's TV/Anime, where indexers file what Sonarr searches as its anime categories.
const animeCategory = 5070

// maxAbsoluteQueries bounds the per-episode searches an anime season may cost.
const maxAbsoluteQueries = 5

// absoluteQueries are the text searches anime releases answer to: "Title 062", one per missing episode that has an
// absolute number, the lowest first.
func (w wanted) absoluteQueries(names []string) []judge.Query {
	var numbers []int
	for absolute, number := range w.absolute {
		if _, missing := w.missing[number]; missing {
			numbers = append(numbers, absolute)
		}
	}
	slices.Sort(numbers)
	var queries []judge.Query
	for _, absolute := range numbers[:min(len(numbers), maxAbsoluteQueries)] {
		queries = append(queries, judge.Query{Function: "search", Text: fmt.Sprintf("%s %03d", releasefacts.CleanQuery(names[0]), absolute)})
	}
	return queries
}

func (s Search) shared() works.Search[wanted] {
	return works.Search[wanted]{
		Name: "tv", Spec: seasonSpec{s}, Desired: s.Desired, Candidates: s.Candidates, Settings: s.Settings, Clock: s.Clock,
		Blocklist: s.Blocklist, Sightings: s.Sightings, ReleaseFilters: s.ReleaseFilters, IndexerDeadline: s.IndexerDeadline,
	}
}

// SearchItem searches one season item: tiers widen until one finds a release that covers a missing episode, and every
// release that did is judged and recorded (a REJECT or DEFER is a finding, not silence).
func (s Search) SearchItem(ctx context.Context, itemID int64, conns []judge.Conn, at time.Time) (ItemSearchResult, error) {
	return s.shared().SearchPart(ctx, itemID, 0, conns, at)
}

// SearchEpisode is SearchItem for one episode of the season, whether or not it is monitored or already on disk; zero
// searches what the season is missing.
func (s Search) SearchEpisode(ctx context.Context, itemID int64, episode int, conns []judge.Conn, at time.Time) (ItemSearchResult, error) {
	return s.shared().SearchPart(ctx, itemID, episode, conns, at)
}

// SearchPart implements works.Searcher: a part of a season is an episode.
func (s Search) SearchPart(ctx context.Context, itemID int64, part int, conns []judge.Conn, at time.Time) (ItemSearchResult, error) {
	return s.shared().SearchPart(ctx, itemID, part, conns, at)
}

// JudgeFeed judges releases a feed listed against one wanted season, exactly as a search's would be; nothing is
// searched and the season's search record is left alone.
func (s Search) JudgeFeed(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (ItemSearchResult, error) {
	return s.shared().JudgeFeed(ctx, itemID, conn, raws, at)
}

// seasonSpec is works.Spec for a season item: its parts are its episodes, numbered within the season.
type seasonSpec struct{ search Search }

// Wanted implements works.Spec: the monitored episodes that have aired and lack a file, else those below the cutoff.
func (t seasonSpec) Wanted(ctx context.Context, item librarydb.DesiredItem, episode int, _ time.Time) (wanted, bool, error) {
	w, err := t.search.wantedFor(ctx, item, episode)
	return w, len(w.missing) > 0, err
}

// Queries implements works.Spec.
func (seasonSpec) Queries(w wanted) [][]judge.Query { return w.queries() }

// Owned implements works.Spec.
func (seasonSpec) Owned(w wanted) map[int]score.Facts { return w.owned }

// Read implements works.Spec: a release is wanted when it names the series and holds a wanted episode of the season.
func (seasonSpec) Read(w wanted, raw indexer.RawItem, conn judge.Conn, now time.Time) (works.Release, bool) {
	info, ok := parsetv.Parse(raw.Title, now)
	if !ok {
		return works.Release{}, false
	}
	numbers, ok := w.coverage(info)
	if !ok {
		return works.Release{}, false
	}
	facts := releasefacts.FromRelease(info.Claims, info.ReleaseGroup, raw, conn.Impl)
	held := releaseEpisodes(info, len(numbers), w.allCount)
	releasefacts.EstimateBitrate(&facts, raw.SizeBytes, held, w.runtimeMs)
	return works.Release{
		Facts: facts, ReleaseGroup: info.ReleaseGroup, Revised: info.Claims.Version > 1, Parts: numbers, Held: held,
		Share: float64(len(numbers)) / float64(len(w.missing)),
	}, true
}

// coverage says which of the wanted episodes the release holds; false when it is not this series, this season or
// anything wanted.
func (w wanted) coverage(info parsetv.Info) ([]int, bool) {
	if !w.names[parsetv.CleanTitle(info.SeriesTitle)] && !w.names[parsetv.CleanTitle(info.TitleWithoutYear)] {
		return nil, false
	}
	if info.SeasonExtra {
		return nil, false
	}
	wantedSeason := w.season
	switch {
	case info.FullSeason || info.MultiSeason && len(info.EpisodeNumbers) == 0:
		inSeason := info.SeasonNumber == wantedSeason
		for _, season := range info.Seasons {
			inSeason = inSeason || season == wantedSeason
		}
		if !inSeason {
			return nil, false
		}
		numbers := make([]int, 0, len(w.missing))
		for number := range w.missing {
			numbers = append(numbers, number)
		}
		return numbers, true
	case len(info.EpisodeNumbers) > 0:
		if info.SeasonNumber != wantedSeason {
			return nil, false
		}
		var numbers []int
		for _, number := range info.EpisodeNumbers {
			if _, missing := w.missing[number]; missing {
				numbers = append(numbers, number)
			}
		}
		return numbers, len(numbers) > 0
	case len(info.AbsoluteNumbers) > 0 && w.anime:
		var numbers []int
		for _, absolute := range info.AbsoluteNumbers {
			if number, known := w.absolute[absolute]; known {
				if _, missing := w.missing[number]; missing {
					numbers = append(numbers, number)
				}
			}
		}
		return numbers, len(numbers) > 0
	case info.AirDate != "":
		for number, episode := range w.missing {
			if episode.Date == info.AirDate {
				return []int{number}, true
			}
		}
	}
	return nil, false
}

// releaseEpisodes is how many episodes a release holds: the ones its name numbers, else the season it is a pack of.
func releaseEpisodes(info parsetv.Info, covered, seasonEpisodes int) int {
	switch {
	case len(info.EpisodeNumbers) > 0:
		return len(info.EpisodeNumbers)
	case info.FullSeason || info.MultiSeason:
		return max(seasonEpisodes, covered)
	}
	return max(covered, 1)
}
