package tv

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrNotSearchable reports a desired item the TV search does not handle on its own (a single episode item).
var ErrNotSearchable = errors.New("tv: search: only season items are searched on their own")

// indexerGroupDeadline bounds one tier's fan-out to every indexer (ADR-37).
const indexerGroupDeadline = 20 * time.Second

// searchTVParams are what a structured id query needs the indexer to accept.
var (
	idParams      = []string{"tvdbid", "season"}
	idParamsOneEp = []string{"tvdbid", "season", "ep"}
)

// ItemSearchResult is one season item's search: which tier produced finds and every decision recorded.
type ItemSearchResult struct {
	TierUsed      *int
	Decisions     []library.DecisionDetail
	IndexerErrors []IndexerError
}

// IndexerError is one indexer's own failure; partial results are normal (ADR-37).
type IndexerError struct {
	IndexerID   int64
	IndexerName string
	Error       string
	TooSlow     bool
}

// Search is the TV candidate pipeline over one season item at a time.
type Search struct {
	Desired    library.Desired
	Series     library.SeriesReader
	Candidates library.Candidates
	Settings   *settings.Cache
	Clock      clock.Clock
	Blocklist  *library.BlocklistStore
	// IndexerDeadline bounds each tier's fan-out; zero is indexerGroupDeadline.
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
	today := now.UTC().Format("2006-01-02")
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
		aired := (episode.AvailableAt > 0 && episode.AvailableAt <= now.Unix()) || (episode.AvailableAt == 0 && episode.Date != "" && episode.Date <= today)
		switch {
		case only > 0:
			if episode.Number == only {
				w.missing[episode.Number] = episode
			}
		case episode.Monitored && !episode.Present && aired:
			w.missing[episode.Number] = episode
		}
	}
	switch {
	case runtimeCount > 0:
		w.runtimeMs = runtimeSum / runtimeCount
	default:
		w.runtimeMs = detail.RuntimeMs
	}
	return w, nil
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
		clean := cleanQuery(name)
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
		spelled = append(spelled, judge.Query{Function: "search", Text: fmt.Sprintf("%s Season %d", cleanQuery(name), w.season)})
	}
	return append(tiers, spelled)
}

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
		queries = append(queries, judge.Query{Function: "search", Text: fmt.Sprintf("%s %03d", cleanQuery(names[0]), absolute)})
	}
	return queries
}

// cleanQuery drops the punctuation indexers' text search chokes on.
func cleanQuery(title string) string {
	var out strings.Builder
	for _, r := range strings.ReplaceAll(title, "&", " and ") {
		switch {
		case r == '\'' || r == '’':
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r > 127:
			out.WriteRune(r)
		default:
			out.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// SearchItem searches one season item: tiers widen until one finds a release that covers a missing episode, and every
// release that did is judged and recorded (a REJECT or DEFER is a finding, not silence).
func (s Search) SearchItem(ctx context.Context, itemID int64, conns []judge.Conn, at time.Time) (ItemSearchResult, error) {
	return s.SearchEpisode(ctx, itemID, 0, conns, at)
}

// SearchEpisode is SearchItem for one episode of the season, whether or not it is monitored or already on disk; zero
// searches what the season is missing.
func (s Search) SearchEpisode(ctx context.Context, itemID int64, episode int, conns []judge.Conn, at time.Time) (ItemSearchResult, error) {
	item, err := s.Desired.Get(ctx, itemID)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("tv: search item %d: %w", itemID, err)
	}
	w, err := s.wantedFor(ctx, item, episode)
	if err != nil {
		return ItemSearchResult{}, err
	}
	if err := s.Desired.RecordSearched(ctx, itemID, at.Unix()); err != nil {
		return ItemSearchResult{}, fmt.Errorf("tv: search item %d: %w", itemID, err)
	}
	if len(w.missing) == 0 {
		return ItemSearchResult{}, nil
	}
	yardstick, err := s.yardstick(ctx, item, w)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("tv: search item %d: %w", itemID, err)
	}

	result := ItemSearchResult{}
	for tierIndex, queries := range w.queries() {
		answers, err := judge.AskEveryIndexer(ctx, conns, queries, cmp.Or(s.IndexerDeadline, indexerGroupDeadline))
		if err != nil {
			return result, fmt.Errorf("tv: search item %d: %w", itemID, err)
		}
		var finds []judge.Find
		for index, answer := range answers {
			conn := conns[index]
			if answer.Err != nil {
				result.IndexerErrors = append(result.IndexerErrors, IndexerError{IndexerID: conn.ID, IndexerName: conn.Name, Error: answer.Err.Error(), TooSlow: answer.TooSlow})
			}
			raws, err := s.withoutBlocklisted(ctx, conn.ID, answer.Raws, at)
			if err != nil {
				return result, fmt.Errorf("tv: search item %d: %w", itemID, err)
			}
			finds = append(finds, yardstick.evaluate(raws, conn)...)
		}
		if len(finds) == 0 {
			continue
		}
		tier := tierIndex
		result.TierUsed = &tier
		result.Decisions, err = s.decide(ctx, itemID, finds, "search", at)
		return result, err
	}
	return result, nil
}

// yardstick judges a release against what the season wants and the library's rules.
type yardstick struct {
	wanted         wanted
	titles         map[string]bool
	rules          []judge.RankedProfile
	languagePolicy *score.LanguagePolicy
	preferPropers  bool
	delay          judge.ProtocolDelay
	now            time.Time
}

func (s Search) yardstick(ctx context.Context, item librarydb.DesiredItem, w wanted) (yardstick, error) {
	snapshot := s.Settings.Snapshot()
	rules, err := judge.LibraryProfiles(ctx, s.Desired, snapshot, item.ID, item.CategoryID, item.ProfileID)
	if err != nil {
		return yardstick{}, err
	}
	built := yardstick{
		wanted: w, titles: w.titles(), rules: rules, languagePolicy: judge.LanguagePolicyFor(snapshot, item.CategoryID),
		preferPropers: true, now: s.Clock.Now(),
	}
	if category, found := snapshot.Category(item.CategoryID); found {
		built.preferPropers = category.FileManagement.PropersRepacks != settings.PropersDoNotPrefer
		built.delay = judge.ProtocolDelay{Preferred: category.PreferredProtocol, Window: time.Duration(category.ProtocolDelayMinutes) * time.Minute, Now: built.now}
	}
	return built, nil
}

func (y yardstick) evaluate(raws []indexer.RawItem, conn judge.Conn) []judge.Find {
	var finds []judge.Find
	for _, raw := range raws {
		info, ok := parsetv.Parse(raw.Title, y.now)
		if !ok {
			continue
		}
		covered, ok := y.coverage(info)
		if !ok {
			continue
		}
		facts := factsFromRelease(info, raw, conn.Impl)
		estimateBitrate(&facts, raw.SizeBytes, releaseEpisodes(info, covered, y.wanted.allCount), y.wanted.runtimeMs)
		decision := judge.WithRevision(judge.EvaluateInOrder(y.rules, facts, y.languagePolicy), info.Claims.Version > 1, y.preferPropers)
		fraction := float64(covered) / float64(len(y.wanted.missing))
		finds = append(finds, judge.Find{
			Conn: conn, Raw: raw, ParseConfidence: 1, Distance: 1 - fraction, MatchConfidence: max(0.5, fraction), Decision: decision,
		})
	}
	y.delay.Apply(finds)
	return finds
}

// coverage says how many of the wanted episodes the release holds; false when it is not this series, this season or
// anything wanted.
func (y yardstick) coverage(info parsetv.Info) (int, bool) {
	if !y.titles[parsetv.CleanTitle(info.SeriesTitle)] && !y.titles[parsetv.CleanTitle(info.TitleWithoutYear)] {
		return 0, false
	}
	if info.SeasonExtra {
		return 0, false
	}
	wantedSeason := y.wanted.season
	switch {
	case info.FullSeason || info.MultiSeason && len(info.EpisodeNumbers) == 0:
		inSeason := info.SeasonNumber == wantedSeason
		for _, season := range info.Seasons {
			inSeason = inSeason || season == wantedSeason
		}
		if !inSeason {
			return 0, false
		}
		return len(y.wanted.missing), true
	case len(info.EpisodeNumbers) > 0:
		if info.SeasonNumber != wantedSeason {
			return 0, false
		}
		covered := 0
		for _, number := range info.EpisodeNumbers {
			if _, missing := y.wanted.missing[number]; missing {
				covered++
			}
		}
		return covered, covered > 0
	case len(info.AbsoluteNumbers) > 0 && y.wanted.anime:
		covered := 0
		for _, absolute := range info.AbsoluteNumbers {
			if number, known := y.wanted.absolute[absolute]; known {
				if _, missing := y.wanted.missing[number]; missing {
					covered++
				}
			}
		}
		return covered, covered > 0
	case info.AirDate != "":
		for _, episode := range y.wanted.missing {
			if episode.Date == info.AirDate {
				return 1, true
			}
		}
	}
	return 0, false
}

// factsFromRelease stands a release name's claims in for measured facts before download (ADR-39).
func factsFromRelease(info parsetv.Info, raw indexer.RawItem, impl string) score.Facts {
	claims := info.Claims
	facts := score.Facts{
		Release: score.ReleaseFacts{Title: raw.Title, ReleaseGroup: info.ReleaseGroup, Seeders: raw.Seeders, Leechers: raw.Peers, Protocol: judge.ProtocolFor(impl)},
		File: score.FileFacts{
			Source: sourceFact(claims), VideoCodec: claims.VideoCodec, HDR: claims.HDR, AudioCodec: claims.AudioCodec,
			AudioChannels: claims.AudioChannels, AudioLanguages: claims.Languages, ReleaseGroup: info.ReleaseGroup, ProperRepack: claims.Version > 1,
		},
	}
	if claims.Resolution > 0 {
		facts.File.Resolution = strconv.Itoa(claims.Resolution) + "p"
	}
	return facts
}

func sourceFact(claims parsetv.Claims) string {
	if claims.Remux {
		return "remux"
	}
	switch claims.Source {
	case "bdrip", "brrip":
		return "bluray"
	case "pdtv", "sdtv", "dsr", "tvrip":
		return "hdtv"
	}
	return claims.Source
}

func (s Search) withoutBlocklisted(ctx context.Context, indexerID int64, raws []indexer.RawItem, at time.Time) ([]indexer.RawItem, error) {
	if s.Blocklist == nil {
		return raws, nil
	}
	kept := raws[:0:0]
	for _, raw := range raws {
		blocked, err := s.Blocklist.IsBlocked(ctx, &indexerID, raw.GUID, "", at.Unix())
		if err != nil {
			return nil, err
		}
		if !blocked {
			kept = append(kept, raw)
		}
	}
	return kept, nil
}

func (s Search) decide(ctx context.Context, itemID int64, finds []judge.Find, source string, at time.Time) ([]library.DecisionDetail, error) {
	finds = judge.WithoutCrossIndexerDuplicates(finds)
	judge.SortBestFirst(finds)
	decisions := make([]library.DecisionDetail, 0, len(finds))
	for _, find := range finds {
		detail, err := judge.Persist(ctx, s.Candidates, itemID, find, source, at)
		if err != nil {
			return decisions, fmt.Errorf("tv: search item %d: persist: %w", itemID, err)
		}
		decisions = append(decisions, detail)
	}
	return decisions, nil
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

// videoShare is the part of a file's bits the picture takes; the sound is the rest. A guess, as the size is the
// only fact before download, and good enough to rank a bloated release above a starved one.
const videoShare = 0.92

// estimateBitrate stands the release's size over its length in for the bitrate the probe will measure.
func estimateBitrate(facts *score.Facts, sizeBytes int64, episodes int, runtimeMs int64) {
	if sizeBytes <= 0 || episodes <= 0 || runtimeMs <= 0 {
		return
	}
	overall := float64(sizeBytes) * 8 / (float64(episodes) * float64(runtimeMs) / 1000)
	facts.File.OverallBitrate = int64(overall)
	facts.File.VideoBitrate = int64(overall * videoShare)
}
