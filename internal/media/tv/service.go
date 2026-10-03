package tv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
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
}

// maxCreditSeasons bounds the per-season calls guest stars and episode crew cost.
const maxCreditSeasons = 40

// Service reads series from the providers and stores them.
type Service struct {
	Skyhook SeriesSource
	TVmaze  ShowSource
	// TMDB is nil or unconfigured until the user gives a key; TV then works on the other two.
	TMDB    RichSource
	Series  library.Series
	Desired library.Desired
	Clock   clock.Clock
	Logger  *slog.Logger
}

// Fetch merges every source that knows the series (ADR-8): Skyhook sets identity and numbering, TMDB adds credits and
// artwork, TVmaze air times. A failing source is skipped; only all failing is an error.
func (s Service) Fetch(ctx context.Context, ids IDs) (Series, error) {
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
		}
	}
	rich, haveRich := s.fetchTMDB(ctx, ids)
	if haveRich {
		sources = append(sources, rich)
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
		PreferRicher(&merged, rich)
		merged.Credits = guardEpisodeCredits(merged.Credits, merged.Episodes)
	}
	return merged, nil
}

// fetchTMDB asks TMDB for the series and every season's guest stars; TMDB's own id is found from TheTVDB's or IMDb's
// when Skyhook did not carry it.
func (s Service) fetchTMDB(ctx context.Context, ids IDs) (Series, bool) {
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
	for _, entry := range series.Seasons {
		if len(seasons) == maxCreditSeasons {
			break
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

	seasons, err := s.ensureSeasons(ctx, stored, series, request)
	if err != nil {
		return AddResult{}, err
	}
	return AddResult{WorkID: stored.WorkID, Created: stored.Created, Episodes: len(series.Episodes), Seasons: seasons, Series: series}, nil
}

func (s Service) ensureSeasons(ctx context.Context, stored library.SeriesResult, series Series, request AddRequest) (int, error) {
	monitored := map[int]bool{}
	seen := map[int]bool{}
	monitoring := newMonitors(request.Monitor, request.Specials, s.Clock.Now(), series.Episodes)
	for _, episode := range series.Episodes {
		seen[episode.Season] = true
		if monitoring.wants(episode) {
			monitored[episode.Season] = true
		}
	}
	for season := range seen {
		_, err := s.Desired.EnsureSeasonItem(ctx, library.SeasonRequest{
			WorkID: stored.WorkID, MediaRef: SeasonRef(season), SortKey: "s" + fmt.Sprintf("%04d", season),
			CategoryID: request.CategoryID, ProfileID: request.ProfileID, Monitored: monitored[season], AddedBy: request.AddedBy,
		})
		if err != nil {
			return 0, fmt.Errorf("tv: season %d: %w", season, err)
		}
	}
	return len(seen), nil
}

// Candidate is one series a search found.
type Candidate struct {
	TVDBID   int64
	Title    string
	Year     int
	Network  string
	Status   Status
	Overview string
	Poster   string
}

// Search finds series by name through Skyhook; an empty answer is not an error.
func (s Service) Search(ctx context.Context, term string, limit int) ([]Candidate, error) {
	if s.Skyhook == nil {
		return nil, ErrNoSource
	}
	hits, err := s.Skyhook.Search(ctx, term)
	if err != nil {
		return nil, fmt.Errorf("tv: search %q: %w", term, err)
	}
	candidates := make([]Candidate, 0, min(len(hits), limit))
	for _, hit := range hits {
		if len(candidates) == limit {
			break
		}
		candidate := Candidate{TVDBID: hit.TVDBID, Title: hit.Title, Year: yearOf(hit.FirstAired), Network: hit.Network, Status: statusOf(hit.Status), Overview: hit.Overview}
		for _, image := range hit.Images {
			if imageKind(image.CoverType) == "poster" {
				candidate.Poster = image.URL
				break
			}
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

// ParseID reads a provider id out of the decimal string a work stores it as.
func ParseID(externalID string) (int64, bool) {
	id, err := strconv.ParseInt(externalID, 10, 64)
	return id, err == nil && id > 0
}
