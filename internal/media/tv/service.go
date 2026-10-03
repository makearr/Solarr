package tv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/platform/clock"
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
	Series  library.Series
	Reader  library.SeriesReader
	Desired library.Desired
	Clock   clock.Clock
	Logger  *slog.Logger
}

// Fetch merges every source that knows the series (ADR-8): Skyhook sets identity and numbering, TMDB adds credits and
// artwork, TVmaze air times. A failing source is skipped; only all failing is an error.
func (s Service) Fetch(ctx context.Context, ids IDs) (Series, error) {
	return s.fetch(ctx, ids, false)
}

// fetch is Fetch; recentOnly asks TMDB for the guest stars of the newest seasons only, which is all a refresh needs.
func (s Service) fetch(ctx context.Context, ids IDs, recentOnly bool) (Series, error) {
	ids = s.completeIDs(ctx, ids)
	var sources []Series
	if ids.TVDB > 0 && s.Skyhook != nil {
		series, err := s.Skyhook.Series(ctx, ids.TVDB)
		if err != nil {
			s.warn("skyhook", ids.TVDB, err)
		} else {
			sources = append(sources, FromSkyhook(series))
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
	rich, haveRich := s.fetchTMDB(ctx, ids, recentOnly)
	if haveRich {
		sources = append(sources, rich)
	}
	if anime, found := s.fetchAniList(ctx, ids); found {
		sources = append(sources, anime)
	}
	if s.TVmaze != nil {
		if show, found := s.findShow(ctx, ids); found {
			sources = append(sources, s.fromShow(ctx, show))
		}
	}
	if len(sources) == 0 {
		return Series{}, ErrNoSource
	}
	merged := Merge(sources...)
	if haveRich {
		if len(merged.Episodes) == 0 && len(rich.TMDBEpisodes) > 0 {
			merged.Episodes, merged.Basis = rich.TMDBEpisodes, ProviderTMDB+":default"
		}
		PreferRicher(&merged, rich)
		merged.Credits = guardEpisodeCredits(merged.Credits, merged.Episodes)
	}
	return merged, nil
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
	var seasons []tmdb.Season
	newest := 0
	for _, entry := range series.Seasons {
		newest = max(newest, entry.SeasonNumber)
	}
	for _, entry := range series.Seasons {
		if len(seasons) == maxCreditSeasons {
			break
		}
		if recentOnly && entry.SeasonNumber < newest-1 {
			continue
		}
		season, err := s.TMDB.Season(ctx, id, entry.SeasonNumber)
		if err != nil {
			s.warn("tmdb season", id, err)
			continue
		}
		seasons = append(seasons, season)
	}
	return FromTMDB(series, seasons, ""), true
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

func (s Service) findShow(ctx context.Context, ids IDs) (tvmaze.Show, bool) {
	var show tvmaze.Show
	var err error
	switch {
	case ids.TVmaze > 0:
		show, err = s.TVmaze.Show(ctx, ids.TVmaze)
	case ids.TVDB > 0:
		show, err = s.TVmaze.LookupTVDB(ctx, ids.TVDB)
	default:
		return tvmaze.Show{}, false
	}
	if err != nil {
		if !errors.Is(err, tvmaze.ErrNotFound) {
			s.warn("tvmaze", firstNonZero(ids.TVmaze, ids.TVDB), err)
		}
		return tvmaze.Show{}, false
	}
	return show, true
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
	cast, err := s.TVmaze.Cast(ctx, show.ID)
	if err != nil {
		s.warn("tvmaze cast", show.ID, err)
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
	// Series is the merged record that was stored, for a caller that answers with it.
	Series Series
}

// Add stores a series and a SET item per season. Adding what the library already holds is not an error: the series
// is refreshed and its seasons left as they are (Invariant 71).
func (s Service) Add(ctx context.Context, request AddRequest) (AddResult, error) {
	if !request.Monitor.Valid() {
		return AddResult{}, fmt.Errorf("tv: unknown monitor option %q", request.Monitor)
	}
	series, err := s.Fetch(ctx, request.IDs)
	if err != nil {
		return AddResult{}, err
	}
	now := s.Clock.Now()
	stored, err := s.Series.Upsert(ctx, now, ToWrite(series, WriteOptions{Monitor: request.Monitor, Specials: request.Specials, Now: now}))
	if err != nil {
		return AddResult{}, err
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
	series, err := s.fetch(ctx, ids, true)
	if err != nil {
		return AddResult{}, err
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
		return seriesWanted
	}
	now := s.Clock.Now()
	stored, err := s.Series.Upsert(ctx, now, ToWrite(series, WriteOptions{NewPart: wants, Now: now}))
	if err != nil {
		return AddResult{}, err
	}
	seasons, err := s.ensureSeasons(ctx, stored, series, wants, request.CategoryID, request.ProfileID, nil)
	if err != nil {
		return AddResult{}, err
	}
	return AddResult{WorkID: stored.WorkID, Episodes: len(series.Episodes), Seasons: seasons, Series: series}, nil
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
	for season := range seen {
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
			for _, image := range hit.Images {
				if imageKind(image.CoverType) == "poster" {
					candidate.Poster = image.URL
					break
				}
			}
			candidates = append(candidates, candidate)
		}
	}
	extra := s.searchTMDB(ctx, term, candidates)
	if len(candidates)+len(extra) == 0 && skyhookErr != nil {
		return nil, skyhookErr
	}
	reserved := min(len(extra), maxTMDBOnlyHits, limit)
	candidates = append(candidates[:min(len(candidates), limit-reserved)], extra[:reserved]...)
	return candidates, nil
}

// searchTMDB lists TMDB's hits for a term that the earlier candidates do not already cover.
func (s Service) searchTMDB(ctx context.Context, term string, have []Candidate) []Candidate {
	if s.TMDB == nil || !s.TMDB.Configured() {
		return nil
	}
	hits, err := s.TMDB.Search(ctx, term)
	if err != nil {
		s.warn("tmdb search", 0, err)
		return nil
	}
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
			continue
		}
		found = append(found, Candidate{TMDBID: hit.ID, Title: hit.Name, Year: year, Overview: hit.Overview, Poster: tmdb.ImageURL(hit.PosterPath)})
	}
	return found
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
