package tv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/metadata/metaresolve"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/parallel"
)

// ErrNoSource means neither provider could say anything about the series.
var ErrNoSource = errors.New("tv: no provider knows this series")

// SeriesSource is the Skyhook client as the service needs it.
type SeriesSource interface {
	Series(ctx context.Context, tvdbID int64) (skyhook.Series, error)
	Search(ctx context.Context, term string) ([]skyhook.Series, error)
}

// ShowSource is the TVmaze client as the service needs it.
type ShowSource interface {
	Show(ctx context.Context, id int64) (tvmaze.Show, error)
	LookupTVDB(ctx context.Context, tvdbID int64) (tvmaze.Show, error)
	Episodes(ctx context.Context, id int64) ([]tvmaze.Episode, error)
	Aliases(ctx context.Context, id int64) ([]string, error)
	Cast(ctx context.Context, id int64) ([]tvmaze.CastMember, error)
}

// RichSource is the TMDB client as the service needs it.
type RichSource interface {
	Configured() bool
	Series(ctx context.Context, id int64) (tmdb.Series, error)
	Season(ctx context.Context, seriesID int64, number int) (tmdb.Season, error)
	FindByTVDB(ctx context.Context, tvdbID int64) (tmdb.Hit, error)
	FindByIMDb(ctx context.Context, imdbID string) (tmdb.Hit, error)
	Search(ctx context.Context, term string) ([]tmdb.Hit, error)
}

// AnimeSource is the AniList client as the service needs it.
type AnimeSource interface {
	Media(ctx context.Context, id int64) (anilist.Media, error)
}

// maxAnimeEntries bounds how many of an anime's cours are asked of AniList, which splits one series into several entries.
const maxAnimeEntries = 5

// maxTMDBOnlyHits is how many search slots shows only TMDB lists may take, so Skyhook's long answer cannot hide them.
const maxTMDBOnlyHits = 5

// maxCreditSeasons bounds the per-season calls guest stars and episode crew cost.
const maxCreditSeasons = 40

// Service reads series from the providers and stores them.
type Service struct {
	Skyhook SeriesSource
	TVmaze  ShowSource
	// TMDB is nil or unconfigured until the user gives a key; TV then works on the other two.
	TMDB RichSource
	// AniList adds the titles anime is released under; nil skips it.
	AniList AnimeSource
	// Priority is the configured TV source order, read per fetch; nil is the default order.
	Priority func() []string
	// PriorityFor is a library's own order; nil, or an empty answer, follows Priority.
	PriorityFor func(categoryID int64) []string
	// Grace is the configured wait after an episode's time, read per write; nil leaves each part its default.
	Grace func() library.AvailabilityGrace
	// IgnoreNewSeasons says a library leaves the seasons a refresh finds unmonitored; nil follows the series.
	IgnoreNewSeasons func(categoryID int64) bool
	// KindOf answers a library's kind; nil takes none.
	KindOf func(categoryID int64) string
	// Orderings lists a series in TheTVDB's other orderings (DVD, absolute, ...); nil offers the aired one only.
	Orderings OrderingSource
	// Wikidata gives the ids its item records, steering the other providers to the right entry; nil asks none.
	Wikidata IDBridge
	Series   library.Series
	// Exclusions are the works the user removed and asked not to see again.
	Exclusions library.Exclusions
	Reader     library.SeriesReader
	Desired    library.Desired
	Clock      clock.Clock
	Logger     *slog.Logger
}

// Fetch merges every source that knows the series (ADR-8): Skyhook sets identity and numbering, TMDB adds credits and
// artwork, TVmaze air times. A failing source is skipped; only all failing is an error.
func (s Service) Fetch(ctx context.Context, ids IDs) (Series, error) {
	return s.fetch(ctx, ids, false, s.order(0))
}

// order is the providers a library asks, best first: its own, else the instance's.
func (s Service) order(categoryID int64) []string {
	if s.PriorityFor != nil && categoryID != 0 {
		if order := s.PriorityFor(categoryID); len(order) > 0 {
			return order
		}
	}
	if s.Priority != nil {
		if order := s.Priority(); len(order) > 0 {
			return order
		}
	}
	return settings.DefaultTVSources
}

// fetch is Fetch; recentOnly asks TMDB for the guest stars of the newest seasons only, which is all a refresh needs.
func (s Service) fetch(ctx context.Context, ids IDs, recentOnly bool, order []string) (Series, error) {
	ids = s.completeIDs(ctx, s.bridgeIDs(ctx, ids, order))
	var sources []Series
	bySource := map[string]Series{}
	if ids.TVDB > 0 && s.Skyhook != nil && slices.Contains(order, settings.TVSourceSkyhook) {
		series, err := s.Skyhook.Series(ctx, ids.TVDB)
		if err != nil {
			s.warn("skyhook", ids.TVDB, err)
		} else {
			sources = append(sources, FromSkyhook(series))
			bySource[settings.TVSourceSkyhook] = sources[len(sources)-1]
			if ids.TVmaze == 0 {
				ids.TVmaze = series.TVMazeID
			}
			if ids.TMDB == 0 {
				ids.TMDB = series.TMDBID
			}
			if ids.IMDb == "" {
				ids.IMDb = series.IMDbID
			}
			if len(ids.AniList) == 0 {
				ids.AniList = series.AniListIDs
			}
		}
	}
	// The three remaining sources each have their own rate bucket, so they are asked at the same time; the order
	// they are merged in stays TMDB, AniList, TVmaze.
	type fetched struct {
		series Series
		found  bool
	}
	var rich Series
	haveRich := false
	var wrongTVmaze atomic.Int64
	asked := slices.DeleteFunc([]string{settings.TVSourceTMDB, settings.TVSourceAniList, settings.TVSourceTVmaze}, func(source string) bool { return !slices.Contains(order, source) })
	answers := metaresolve.AskAll(ctx, asked,
		func(ctx context.Context, source string) (fetched, error) {
			switch source {
			case settings.TVSourceTMDB:
				series, found := s.fetchTMDB(ctx, ids, recentOnly)
				return fetched{series, found}, nil
			case settings.TVSourceAniList:
				series, found := s.fetchAniList(ctx, ids)
				return fetched{series, found}, nil
			}
			if s.TVmaze == nil {
				return fetched{}, nil
			}
			show, found, rejected := s.findShow(ctx, ids)
			if rejected > 0 {
				wrongTVmaze.Store(rejected)
			}
			if !found {
				return fetched{}, nil
			}
			return fetched{s.fromShow(ctx, show), true}, nil
		})
	for _, answer := range answers {
		if answer.Err != nil {
			s.warn(answer.Source, ids.TVDB, answer.Err)
		}
		if !answer.Value.found {
			continue
		}
		sources = append(sources, answer.Value.series)
		bySource[answer.Source] = answer.Value.series
		if answer.Source == settings.TVSourceTMDB {
			rich, haveRich = answer.Value.series, true
		}
	}
	facts, haveFacts := s.wikidataFacts(ctx, ids, order)
	if haveFacts && facts.Summary != "" {
		sources = append(sources, Series{Overview: facts.Summary, Sources: []string{settings.SourceWikidata}})
		bySource[settings.SourceWikidata] = sources[len(sources)-1]
	}
	if len(sources) == 0 {
		return Series{}, ErrNoSource
	}
	merged := Merge(inOrder(sources, bySource, order)...)
	if wrong := wrongTVmaze.Load(); wrong > 0 && merged.IDs.TVmaze == wrong {
		merged.IDs.TVmaze = 0
	}
	merged.Type = DetectType(merged)
	if merged.AirTime != nil && merged.AirTime.Timezone == "" {
		if zone := GuessZone(merged.OriginalCountry); zone != "" {
			merged.AirTime.Timezone, merged.AirTime.Guessed = zone, true
		}
	}
	if haveRich {
		if len(merged.Episodes) == 0 && len(rich.TMDBEpisodes) > 0 {
			merged.Episodes, merged.Basis = rich.TMDBEpisodes, ProviderTMDB+":default"
		}
		merged.Credits = guardEpisodeCredits(merged.Credits, merged.Episodes)
	}
	ApplyPriority(&merged, bySource, order)
	// Wikidata's genres are too fine ("crime drama television series") to lead; they only fill where no one has any.
	if len(merged.Genres) == 0 && haveFacts {
		merged.Genres = facts.Genres
	}
	return merged, nil
}

// wikidataFacts is what Wikidata and the English Wikipedia article say of the series, when the library asks Wikidata.
func (s Service) wikidataFacts(ctx context.Context, ids IDs, order []string) (wikidata.WorkFacts, bool) {
	if s.Wikidata == nil || ids.TVDB == 0 || !slices.Contains(order, settings.SourceWikidata) {
		return wikidata.WorkFacts{}, false
	}
	id := strconv.FormatInt(ids.TVDB, 10)
	facts, err := s.Wikidata.FactsOf(ctx, wikidata.FactSeries, []string{id})
	if err != nil {
		s.warn("wikidata", ids.TVDB, err)
		return wikidata.WorkFacts{}, false
	}
	found, known := facts[id]
	return found, known
}

// fetchAniList asks AniList for each cour of an anime Skyhook knows by AniList id; a series with none is not asked.
func (s Service) fetchAniList(ctx context.Context, ids IDs) (Series, bool) {
	if s.AniList == nil || len(ids.AniList) == 0 {
		return Series{}, false
	}
	var entries []anilist.Media
	for _, id := range ids.AniList[:min(len(ids.AniList), maxAnimeEntries)] {
		entry, err := s.AniList.Media(ctx, id)
		if err != nil {
			s.warn("anilist", id, err)
			continue
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return Series{}, false
	}
	return FromAniList(entries), true
}

// completeIDs finds TheTVDB's id from TMDB's when only that is known, so a series added from TMDB still gets Skyhook's
// episodes.
func (s Service) completeIDs(ctx context.Context, ids IDs) IDs {
	if ids.TVDB > 0 || ids.TMDB == 0 || s.TMDB == nil || !s.TMDB.Configured() {
		return ids
	}
	series, err := s.TMDB.Series(ctx, ids.TMDB)
	if err != nil {
		s.warn("tmdb", ids.TMDB, err)
		return ids
	}
	ids.TVDB = series.ExternalIDs.TVDB
	if ids.IMDb == "" {
		ids.IMDb = series.ExternalIDs.IMDb
	}
	return ids
}

// IDBridge is Wikidata as a fetch asks it: the ids its item records for a series, and what it says of it.
type IDBridge interface {
	SeriesIDs(ctx context.Context, known wikidata.WorkIDs) (wikidata.WorkIDs, bool, error)
	FactsOf(ctx context.Context, kind wikidata.FactKind, ids []string) (map[string]wikidata.WorkFacts, error)
}

// Warmer is Wikidata read in bulk ahead of the lookups that follow.
type Warmer interface {
	Warm(ctx context.Context, kind wikidata.FactKind, ids []string) error
}

// WarmWikidata reads at once what Wikidata says of every series about to be refreshed whose library asks it, so each
// refresh finds it kept instead of asking on its own.
func (s Service) WarmWikidata(ctx context.Context, due []library.RefreshCandidate) {
	warmer, bulk := s.Wikidata.(Warmer)
	if !bulk {
		return
	}
	var ids []string
	for _, candidate := range due {
		if !slices.Contains(s.order(candidate.CategoryID), settings.SourceWikidata) {
			continue
		}
		if provider, err := s.Reader.ProviderIDs(ctx, candidate.WorkID); err == nil && provider[ProviderTVDB] != "" {
			ids = append(ids, provider[ProviderTVDB])
		}
	}
	if err := warmer.Warm(ctx, wikidata.FactSeries, ids); err != nil {
		s.warn("wikidata", 0, err)
	}
}

// bridgeIDs fills in the ids Wikidata's item records and the series lacks; first in the library's order, Wikidata also
// replaces the ones it disagrees with. TheTVDB's id, which found the item, is never changed.
func (s Service) bridgeIDs(ctx context.Context, ids IDs, order []string) IDs {
	at := slices.Index(order, settings.SourceWikidata)
	if s.Wikidata == nil || at < 0 {
		return ids
	}
	found, ok, err := s.Wikidata.SeriesIDs(ctx, wikidata.WorkIDs{TVDB: ids.TVDB, TMDB: ids.TMDB, IMDb: ids.IMDb})
	if err != nil {
		s.warn("wikidata", ids.TVDB, err)
		return ids
	}
	if !ok {
		return ids
	}
	leads := at == 0
	pick := func(have, theirs int64) int64 {
		if theirs > 0 && (have == 0 || leads) {
			return theirs
		}
		return have
	}
	if ids.TVDB == 0 {
		ids.TVDB = found.TVDB
	}
	ids.TMDB, ids.TVmaze = pick(ids.TMDB, found.TMDB), pick(ids.TVmaze, found.TVmaze)
	if found.IMDb != "" && (ids.IMDb == "" || leads) {
		ids.IMDb = found.IMDb
	}
	if len(ids.AniList) == 0 {
		ids.AniList = found.AniList
	}
	return ids
}

// fetchTMDB asks TMDB for the series and every season's guest stars; TMDB's own id is found from TheTVDB's or IMDb's
// when Skyhook did not carry it.
func (s Service) fetchTMDB(ctx context.Context, ids IDs, recentOnly bool) (Series, bool) {
	if s.TMDB == nil || !s.TMDB.Configured() {
		return Series{}, false
	}
	id := ids.TMDB
	if id == 0 && ids.TVDB > 0 {
		if hit, err := s.TMDB.FindByTVDB(ctx, ids.TVDB); err == nil {
			id = hit.ID
		}
	}
	if id == 0 && ids.IMDb != "" {
		if hit, err := s.TMDB.FindByIMDb(ctx, ids.IMDb); err == nil {
			id = hit.ID
		}
	}
	if id == 0 {
		return Series{}, false
	}
	series, err := s.TMDB.Series(ctx, id)
	if err != nil {
		s.warn("tmdb", id, err)
		return Series{}, false
	}
	newest := 0
	for _, entry := range series.Seasons {
		newest = max(newest, entry.SeasonNumber)
	}
	var wanted []int
	for _, entry := range series.Seasons {
		if len(wanted) == maxCreditSeasons {
			break
		}
		if recentOnly && entry.SeasonNumber < newest-1 {
			continue
		}
		wanted = append(wanted, entry.SeasonNumber)
	}
	seasons := s.fetchSeasons(ctx, id, wanted)
	return FromTMDB(series, seasons, ""), true
}

// seasonWorkers is how many of a series' seasons are asked of TMDB at once; its bucket still paces the calls.
const seasonWorkers = 4

// fetchSeasons reads the seasons' guest stars and episode crew, a few at a time, in the order asked; one that fails is
// skipped.
func (s Service) fetchSeasons(ctx context.Context, id int64, numbers []int) []tmdb.Season {
	answers := make([]*tmdb.Season, len(numbers))
	asked := parallel.Do(ctx, len(numbers), seasonWorkers, func(ctx context.Context, index int) error {
		season, err := s.TMDB.Season(ctx, id, numbers[index])
		if err != nil {
			return err
		}
		answers[index] = &season
		return nil
	})
	for _, err := range asked {
		if err != nil {
			s.warn("tmdb season", id, err)
		}
	}
	var seasons []tmdb.Season
	for _, answer := range answers {
		if answer != nil {
			seasons = append(seasons, *answer)
		}
	}
	return seasons
}

// guardEpisodeCredits drops an episode credit whose episode aired on another date under the numbering basis, or is
// missing: a guest star on the wrong episode is worse than none.
func guardEpisodeCredits(credits []Credit, episodes []Episode) []Credit {
	aired := make(map[Key]string, len(episodes))
	for _, episode := range episodes {
		aired[Key{episode.Season, episode.Number}] = episode.AirDate
	}
	kept := credits[:0:0]
	for _, credit := range credits {
		if credit.Season == 0 && credit.Episode == 0 {
			kept = append(kept, credit)
			continue
		}
		date, found := aired[Key{credit.Season, credit.Episode}]
		if found && (credit.EpisodeAirDate == "" || date == "" || credit.EpisodeAirDate == date) {
			kept = append(kept, credit)
		}
	}
	return kept
}

// findShow asks TVmaze for the series; an id another provider supplied is trusted only while the show's own TheTVDB and
// IMDb ids agree (Skyhook lists the 2013 "Sua and XavpaRov" as TVmaze's id for its 2022 sequel), else rejected is that id.
func (s Service) findShow(ctx context.Context, ids IDs) (show tvmaze.Show, found bool, rejected int64) {
	var err error
	switch {
	case ids.TVmaze > 0:
		show, err = s.TVmaze.Show(ctx, ids.TVmaze)
		if err == nil && contradicts(show, ids) {
			rejected = ids.TVmaze
			if ids.TVDB == 0 {
				return tvmaze.Show{}, false, rejected
			}
			show, err = s.TVmaze.LookupTVDB(ctx, ids.TVDB)
		}
	case ids.TVDB > 0:
		show, err = s.TVmaze.LookupTVDB(ctx, ids.TVDB)
	default:
		return tvmaze.Show{}, false, 0
	}
	if err != nil {
		if !errors.Is(err, tvmaze.ErrNotFound) {
			s.warn("tvmaze", firstNonZero(ids.TVmaze, ids.TVDB), err)
		}
		return tvmaze.Show{}, false, rejected
	}
	return show, true, rejected
}

// contradicts is a TVmaze show naming a TheTVDB or IMDb id other than the one the series is known by.
func contradicts(show tvmaze.Show, ids IDs) bool {
	if show.Externals.TheTVDB != nil && ids.TVDB > 0 && *show.Externals.TheTVDB != ids.TVDB {
		return true
	}
	return show.Externals.IMDb != "" && ids.IMDb != "" && show.Externals.IMDb != ids.IMDb
}

func (s Service) fromShow(ctx context.Context, show tvmaze.Show) Series {
	episodes, err := s.TVmaze.Episodes(ctx, show.ID)
	if err != nil {
		s.warn("tvmaze episodes", show.ID, err)
	}
	aliases, err := s.TVmaze.Aliases(ctx, show.ID)
	if err != nil {
		s.warn("tvmaze aliases", show.ID, err)
	}
	// TMDB's cast is the richer one and TVmaze's bucket is the slowest, so its cast is asked only without a TMDB key.
	var cast []tvmaze.CastMember
	if s.TMDB == nil || !s.TMDB.Configured() {
		if cast, err = s.TVmaze.Cast(ctx, show.ID); err != nil {
			s.warn("tvmaze cast", show.ID, err)
		}
	}
	return FromTVmaze(show, episodes, aliases, cast)
}

func (s Service) warn(source string, id int64, err error) {
	if s.Logger != nil {
		s.Logger.Warn("tv metadata source failed", slog.String("source", source), slog.Int64("id", id), slog.Any("error", err))
	}
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

// AddRequest is Sonarr's add dialog: where the series goes and what starts out wanted.
type AddRequest struct {
	IDs        IDs
	CategoryID int64
	ProfileID  int64
	Monitor    MonitorOption
	Specials   bool
	AddedBy    *int64
}

// AddResult reports what an add stored.
type AddResult struct {
	WorkID   int64
	Created  bool
	Episodes int
	Seasons  int
	// Pruned are the episodes a refresh removed because Skyhook no longer lists them (nothing held on to them).
	Pruned []string
	// Series is the merged record that was stored, for a caller that answers with it.
	Series Series
}

// Add stores a series and a SET item per season. Adding what the library already holds is not an error: the series
// is refreshed and its seasons left as they are (Invariant 71).
func (s Service) Add(ctx context.Context, request AddRequest) (AddResult, error) {
	if !request.Monitor.Valid() {
		return AddResult{}, fmt.Errorf("tv: unknown monitor option %q", request.Monitor)
	}
	series, err := s.fetch(ctx, request.IDs, false, s.order(request.CategoryID))
	if err != nil {
		return AddResult{}, err
	}
	now := s.Clock.Now()
	stored, err := s.Series.Upsert(ctx, now, ToWrite(series, WriteOptions{Monitor: request.Monitor, Specials: request.Specials, Now: now, Grace: s.grace()}))
	if err != nil {
		return AddResult{}, err
	}

	if kindType := s.kindType(request.CategoryID); stored.Created && series.Type == SeriesStandard && kindType != SeriesStandard {
		// The library says what the providers did not: an anime library's series is anime.
		if err := s.SetSeriesType(ctx, stored.WorkID, kindType); err != nil {
			return AddResult{}, err
		}
	}
	monitoring := newMonitors(request.Monitor, request.Specials, now, series.Episodes)
	seasons, err := s.ensureSeasons(ctx, stored, series, monitoring.wants, request.CategoryID, request.ProfileID, request.AddedBy)
	if err != nil {
		return AddResult{}, err
	}
	return AddResult{WorkID: stored.WorkID, Created: stored.Created, Episodes: len(series.Episodes), Seasons: seasons, Series: series}, nil
}

// ApplyMonitor re-applies one of Sonarr's monitor choices to a series the library holds: the episodes it wants become
// monitored and the rest unmonitored, and each season follows. "Existing" wants the episodes with a file and "Missing"
// those without, which only a held series can know.
func (s Service) ApplyMonitor(ctx context.Context, categoryID, workID int64, option MonitorOption, specials bool) error {
	if !option.Valid() {
		return fmt.Errorf("tv: unknown monitor option %q", option)
	}
	now := s.Clock.Now()
	detail, err := s.Reader.Get(ctx, categoryID, workID, now)
	if err != nil {
		return err
	}
	episodes := make([]Episode, len(detail.Episodes))
	for i, held := range detail.Episodes {
		episodes[i] = Episode{Season: held.Season, Number: held.Number, AirDate: held.Date, Special: held.Season == 0}
		if held.AvailableAt > 0 {
			episodes[i].AirUTC = time.Unix(held.AvailableAt, 0).UTC()
		}
	}
	monitoring := newMonitors(option, specials, now, episodes)
	wanted := make(map[int64]bool, len(episodes))
	for i, held := range detail.Episodes {
		switch option {
		case MonitorExisting:
			wanted[held.PartID] = held.Present && (held.Season > 0 || specials)
		case MonitorMissing:
			wanted[held.PartID] = !held.Present && monitoring.wants(episodes[i])
		default:
			wanted[held.PartID] = monitoring.wants(episodes[i])
		}
	}
	return s.Desired.ApplyPartMonitoring(ctx, workID, wanted)
}

// SetEpisodeMonitoring sets the monitored flag of the episodes named by (season, number), leaving any it does not name
// as it is, and each season follows; a Sonarr import carries its own per-episode choices over this way.
func (s Service) SetEpisodeMonitoring(ctx context.Context, categoryID, workID int64, wanted map[Key]bool) error {
	detail, err := s.Reader.Get(ctx, categoryID, workID, s.Clock.Now())
	if err != nil {
		return err
	}
	byPart := make(map[int64]bool, len(wanted))
	for _, held := range detail.Episodes {
		if on, named := wanted[Key{held.Season, held.Number}]; named {
			byPart[held.PartID] = on
		}
	}
	return s.Desired.ApplyPartMonitoring(ctx, workID, byPart)
}

// SetSeriesType records the user's own choice of series type; an empty type returns to what the providers say.
func (s Service) SetSeriesType(ctx context.Context, workID int64, kind SeriesType) error {
	return s.Series.SetManualField(ctx, s.Clock.Now(), workID, "series_type", string(kind))
}

// SetProfile gives every season of a series the quality profile, or returns them to the library's when nil.
func (s Service) SetProfile(ctx context.Context, categoryID, workID int64, profileID *int64) error {
	detail, err := s.Reader.Get(ctx, categoryID, workID, s.Clock.Now())
	if err != nil {
		return err
	}
	for _, season := range detail.Summary.Seasons {
		if err := s.Desired.SetProfileOverride(ctx, season.DesiredItemID, profileID); err != nil {
			return err
		}
	}
	return nil
}

// RefreshRequest names a series the library holds and the library it is in.
type RefreshRequest struct {
	WorkID     int64
	CategoryID int64
	ProfileID  int64
}

// Refresh re-reads a series the library holds. Monitoring is the user's: an episode that is new follows its season, a
// season that is new is monitored when the series already has a monitored one (Sonarr's "monitor new items: all"),
// and specials never are. Nothing existing changes and no part is removed.
func (s Service) Refresh(ctx context.Context, request RefreshRequest) (AddResult, error) {
	provider, err := s.Reader.ProviderIDs(ctx, request.WorkID)
	if err != nil {
		return AddResult{}, err
	}
	ids := IDs{IMDb: provider[ProviderIMDb]}
	ids.TVDB, _ = ParseID(provider[ProviderTVDB])
	ids.TVmaze, _ = ParseID(provider[ProviderTVmaze])
	ids.TMDB, _ = ParseID(provider[ProviderTMDB])
	series, err := s.fetch(ctx, ids, true, s.order(request.CategoryID))
	if err != nil {
		return AddResult{}, err
	}
	manual, err := s.Reader.ManualFields(ctx, request.WorkID)
	if err != nil {
		return AddResult{}, err
	}
	applyAirTimeZone(&series, manual[FieldAirTimeZone])
	if err := s.keepOrdering(ctx, request.WorkID, &series); err != nil {
		return AddResult{}, fmt.Errorf("tv: numbering by the chosen ordering: %w", err)
	}
	monitored, err := s.Reader.SeasonMonitoring(ctx, request.WorkID)
	if err != nil {
		return AddResult{}, err
	}
	seriesWanted := false
	for season, on := range monitored {
		seriesWanted = seriesWanted || (season > 0 && on)
	}
	wants := func(episode Episode) bool {
		if episode.Season == 0 || episode.Special {
			return false
		}
		if on, held := monitored[episode.Season]; held {
			return on
		}
		return seriesWanted && (s.IgnoreNewSeasons == nil || !s.IgnoreNewSeasons(request.CategoryID))
	}
	now := s.Clock.Now()
	write := ToWrite(series, WriteOptions{NewPart: wants, Now: now, Grace: s.grace()})
	stored, err := s.Series.Upsert(ctx, now, write)
	if err != nil {
		return AddResult{}, err
	}
	seasons, err := s.ensureSeasons(ctx, stored, series, wants, request.CategoryID, request.ProfileID, nil)
	if err != nil {
		return AddResult{}, err
	}
	pruned := s.pruneVanished(ctx, request.WorkID, series, write)
	return AddResult{WorkID: stored.WorkID, Episodes: len(series.Episodes), Seasons: seasons, Pruned: pruned, Series: series}, nil
}

// maxPruneShare is the most of a series' episodes one refresh may remove; more means the answer, not the series, changed.
const maxPruneShare = 0.25

// pruneVanished removes the episodes Skyhook, the aired numbering's source, stopped listing, when nothing holds on to
// them. It does nothing unless Skyhook answered and the series is numbered by the aired order, since any other source
// or ordering lists different episodes; a failure is logged and the refresh goes on.
func (s Service) pruneVanished(ctx context.Context, workID int64, series Series, write library.SeriesWrite) []string {
	if !slices.Contains(series.Sources, sourceSkyhook) || len(write.Parts) == 0 {
		return nil
	}
	if basis, err := s.numberedByOrdering(ctx, workID); err != nil || basis != "" {
		return nil
	}
	keep := make(map[string]bool, len(write.Parts))
	for _, part := range write.Parts {
		keep[part.MediaRef] = true
	}
	removed, err := s.Series.PruneParts(ctx, workID, keep, maxPruneShare)
	if err != nil {
		s.warn("prune vanished episodes", workID, err)
		return nil
	}
	if len(removed) > 0 && s.Logger != nil {
		s.Logger.Info("episodes the provider no longer lists were removed", slog.Int64("work_id", workID), slog.Any("episodes", removed))
	}
	return removed
}

func (s Service) ensureSeasons(
	ctx context.Context, stored library.SeriesResult, series Series, wants func(Episode) bool, categoryID, profileID int64, addedBy *int64,
) (int, error) {
	monitored := map[int]bool{}
	seen := map[int]bool{}
	for _, episode := range series.Episodes {
		seen[episode.Season] = true
		if wants(episode) {
			monitored[episode.Season] = true
		}
	}
	// In season order, so the items' ids, and whatever picks the first item, do not depend on map order.
	for _, season := range slices.Sorted(maps.Keys(seen)) {
		_, err := s.Desired.EnsureSeasonItem(ctx, library.SeasonRequest{
			WorkID: stored.WorkID, MediaRef: SeasonRef(season), SortKey: "s" + fmt.Sprintf("%04d", season),
			CategoryID: categoryID, ProfileID: profileID, Monitored: monitored[season], AddedBy: addedBy,
		})
		if err != nil {
			return 0, fmt.Errorf("tv: season %d: %w", season, err)
		}
	}
	return len(seen), nil
}

// IsExcluded reports whether the user deleted this series and asked not to see it again.
func (s Service) IsExcluded(ctx context.Context, ids IDs) bool {
	excluded, err := s.Exclusions.Excluded(ctx, idWrites(ids))
	if err != nil {
		s.warn("exclusion check", ids.TVDB, err)
		return false
	}
	return excluded
}

// Candidate is one series a search found.
type Candidate struct {
	// TVDBID is TheTVDB's id; zero for a show only TMDB lists, which carries TMDBID instead.
	TVDBID   int64
	TMDBID   int64
	Title    string
	Year     int
	Network  string
	Status   Status
	Overview string
	Poster   string
	// AltTitles are other names providers give it (TMDB's original title), which a folder may be named by.
	AltTitles []string
	// Traits say which library it belongs in.
	Traits media.Traits
}

// Search finds series by name through Skyhook, and through TMDB when a key is stored for shows TheTVDB does not list
// (new or announced ones); an empty answer is not an error. A TMDB hit already found at Skyhook, by name and year, is dropped.
func (s Service) Search(ctx context.Context, term string, limit int) ([]Candidate, error) {
	if s.Skyhook == nil && (s.TMDB == nil || !s.TMDB.Configured()) {
		return nil, ErrNoSource
	}
	var candidates []Candidate
	var skyhookErr error
	if s.Skyhook != nil {
		hits, err := s.Skyhook.Search(ctx, term)
		if err != nil {
			skyhookErr = fmt.Errorf("tv: search %q: %w", term, err)
		}
		for _, hit := range hits {
			candidate := Candidate{TVDBID: hit.TVDBID, TMDBID: hit.TMDBID, Title: hit.Title, Year: yearOf(hit.FirstAired), Network: hit.Network, Status: statusOf(hit.Status), Overview: hit.Overview}
			candidate.Traits = media.Traits{
				Genres: hit.Genres, OriginalLanguage: hit.OriginalLanguage, AniList: len(hit.AniListIDs) > 0, Rating: hit.ContentRating,
			}
			if hit.OriginalCountry != "" {
				candidate.Traits.OriginCountries = []string{hit.OriginalCountry}
			}
			for _, image := range hit.Images {
				if imageKind(image.CoverType) == "poster" {
					candidate.Poster = image.URL
					break
				}
			}
			candidates = append(candidates, candidate)
		}
	}
	extra, alternatives := s.searchTMDB(ctx, term, candidates)
	for i := range candidates {
		candidates[i].AltTitles = alternatives[searchKey(candidates[i].Title, candidates[i].Year)]
	}
	if len(candidates)+len(extra) == 0 && skyhookErr != nil {
		return nil, skyhookErr
	}
	reserved := min(len(extra), maxTMDBOnlyHits, limit)
	candidates = append(candidates[:min(len(candidates), limit-reserved)], extra[:reserved]...)
	return candidates, nil
}

// searchTMDB lists TMDB's hits for a term that the earlier candidates do not already cover.
func (s Service) searchTMDB(ctx context.Context, term string, have []Candidate) ([]Candidate, map[string][]string) {
	if s.TMDB == nil || !s.TMDB.Configured() {
		return nil, nil
	}
	hits, err := s.TMDB.Search(ctx, term)
	if err != nil {
		s.warn("tmdb search", 0, err)
		return nil, nil
	}
	alternatives := map[string][]string{}
	covered := make(map[string]bool, len(have))
	for _, candidate := range have {
		covered[searchKey(candidate.Title, candidate.Year)] = true
		if candidate.TMDBID > 0 {
			covered["tmdb:"+strconv.FormatInt(candidate.TMDBID, 10)] = true
		}
	}
	var found []Candidate
	for _, hit := range hits {
		year := yearOf(hit.FirstAirDate)
		if covered[searchKey(hit.Name, year)] || covered["tmdb:"+strconv.FormatInt(hit.ID, 10)] {
			if hit.OriginalName != "" && hit.OriginalName != hit.Name {
				key := searchKey(hit.Name, year)
				alternatives[key] = append(alternatives[key], hit.OriginalName)
			}
			continue
		}
		found = append(found, Candidate{
			TMDBID: hit.ID, Title: hit.Name, Year: year, Overview: hit.Overview, Poster: tmdb.ImageURL(hit.PosterPath),
			Traits: media.Traits{Genres: tmdb.Genres(hit.GenreIDs), OriginalLanguage: hit.OriginalLanguage, OriginCountries: hit.OriginCountry},
		})
	}
	return found, alternatives
}

// searchKey names a hit by title without TheTVDB's "(US)" or "(2019)" disambiguation, and year.
func searchKey(title string, year int) string {
	return SortTitle(trailingBrackets.ReplaceAllString(title, "")) + "|" + strconv.Itoa(year)
}

var trailingBrackets = regexp.MustCompile(`\s*\([^)]*\)\s*$`)

// ParseID reads a provider id out of the decimal string a work stores it as.
func ParseID(externalID string) (int64, bool) {
	id, err := strconv.ParseInt(externalID, 10, 64)
	return id, err == nil && id > 0
}

func (s Service) grace() *library.AvailabilityGrace {
	if s.Grace == nil {
		return nil
	}
	grace := s.Grace()
	return &grace
}

func (s Service) kindType(categoryID int64) SeriesType {
	if s.KindOf == nil {
		return SeriesStandard
	}
	return seriesTypeOfKind(s.KindOf(categoryID))
}

// inOrder lists the sources for merging: Skyhook first, which sets identity and numbering, then the rest as the order ranks
// them, so a lower provider only fills what a higher one left empty.
func inOrder(sources []Series, bySource map[string]Series, order []string) []Series {
	out := make([]Series, 0, len(sources))
	if series, ok := bySource[settings.TVSourceSkyhook]; ok {
		out = append(out, series)
	}
	for _, name := range order {
		if name == settings.TVSourceSkyhook {
			continue
		}
		if series, ok := bySource[name]; ok {
			out = append(out, series)
		}
	}
	return out
}
