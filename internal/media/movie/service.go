package movie

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/metadata/fanart"
	"github.com/makearr/Solarr/internal/metadata/mdblist"
	"github.com/makearr/Solarr/internal/metadata/omdb"
	"github.com/makearr/Solarr/internal/metadata/radarrmeta"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/metadata/tvdb"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrNoSource means no provider could be asked or none knew the movie.
var ErrNoSource = errors.New("movie: no metadata source answered")

// TMDBSource is the TMDB client as the service needs it.
type TMDBSource interface {
	Configured() bool
	Movie(ctx context.Context, id int64) (tmdb.Movie, error)
	SearchMovies(ctx context.Context, term string, year int) ([]tmdb.MovieHit, error)
	FindMovieByIMDb(ctx context.Context, imdbID string) (tmdb.MovieHit, error)
	Collection(ctx context.Context, id int64) (tmdb.CollectionDetail, error)
}

// ProxySource is Radarr's keyless metadata service as the service needs it.
type ProxySource interface {
	Movie(ctx context.Context, tmdbID int64) (radarrmeta.Movie, error)
	ByIMDb(ctx context.Context, imdbID string) (radarrmeta.Movie, error)
	Search(ctx context.Context, term string, year int) ([]radarrmeta.Movie, error)
}

// Service reads movies from the providers and keeps them in the library. Either source may be nil.
type Service struct {
	TMDB  TMDBSource
	Proxy ProxySource
	// Wikidata gives the ids its item records, steering the other providers to the right film; nil asks none.
	Wikidata IDBridge
	// MDBList, OMDb, Fanart and TVDB are asked where the library's order lists them; nil asks none.
	MDBList RatingsSource
	OMDb    PlotSource
	Fanart  ArtSource
	TVDB    TVDBSource
	// Priority is the instance's provider order, PriorityFor a library's own; nil follows the next, then TMDB before Radarr.
	Priority    func() []string
	PriorityFor func(categoryID int64) []string
	// Country picks the certification a movie shows; "US" when it answers nothing.
	Country func() string
	// AvailabilityOf answers a library's default minimum availability; nil takes Radarr's "released".
	AvailabilityOf func(categoryID int64) Availability
	Series         library.Series
	Reader         library.SeriesReader
	Desired        library.Desired
	Franchises     library.Franchises
	Exclusions     library.Exclusions
	Clock          clock.Clock
	Logger         *slog.Logger
}

func (s Service) country() string {
	if s.Country != nil {
		return s.Country()
	}
	return ""
}

func (s Service) warn(source string, id int64, err error) {
	if s.Logger != nil {
		s.Logger.Warn("movie metadata source failed", slog.String("source", source), slog.Int64("id", id), slog.Any("error", err))
	}
}

// Fetch merges every source that knows the movie (ADR-8) in the instance's order; see FetchFor.
func (s Service) Fetch(ctx context.Context, ids IDs) (Movie, error) {
	return s.FetchFor(ctx, 0, ids)
}

// order is the providers a library asks, best first: its own, else the instance's, else TMDB then Radarr's service.
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
	return settings.DefaultMovieSources
}

// FetchFor merges every source the library asks that knows the movie, the first supplying what it holds and each later
// one only filling what is missing. A failing source is skipped; only all failing is an error.
func (s Service) FetchFor(ctx context.Context, categoryID int64, ids IDs) (Movie, error) {
	order := s.order(categoryID)
	ids = s.completeIDs(ctx, s.bridgeIDs(ctx, ids, order))
	answers := map[string]Movie{}
	if ids.TMDB > 0 && s.TMDB != nil && s.TMDB.Configured() && slices.Contains(order, settings.MovieSourceTMDB) {
		answer, err := s.TMDB.Movie(ctx, ids.TMDB)
		switch {
		case err == nil:
			answers[settings.MovieSourceTMDB] = FromTMDB(answer, s.country())
		case !errors.Is(err, tmdb.ErrNotFound):
			s.warn("tmdb", ids.TMDB, err)
		}
	}
	if s.Proxy != nil && (ids.TMDB > 0 || ids.IMDb != "") && slices.Contains(order, settings.MovieSourceRadarr) {
		answer, err := s.askProxy(ctx, ids)
		switch {
		case err == nil:
			answers[settings.MovieSourceRadarr] = FromRadarr(answer, s.country())
		case !errors.Is(err, radarrmeta.ErrNotFound):
			s.warn("radarr", ids.TMDB, err)
		}
	}
	for _, name := range order {
		if answer, ok := s.askMore(ctx, name, ids); ok {
			answers[name] = answer
		}
	}
	var movie Movie
	found := false
	for _, name := range order {
		answer, ok := answers[name]
		switch {
		case !ok:
		case !found:
			movie, found = answer, true
		default:
			movie = Merge(movie, answer)
		}
	}
	if !found {
		return Movie{}, ErrNoSource
	}
	movie.Year = cmp.Or(movie.Year, movie.spareYear)
	if len(movie.Genres) == 0 {
		movie.Genres = movie.spareGenres
	}
	return movie, nil
}

// askMore asks one of the providers beyond TMDB and Radarr's service; one without a key, or that knows nothing, answers none.
func (s Service) askMore(ctx context.Context, name string, ids IDs) (Movie, bool) {
	report := func(err error, missing ...error) bool {
		for _, quiet := range missing {
			if errors.Is(err, quiet) {
				return false
			}
		}
		if err != nil {
			s.warn(name, ids.TMDB, err)
			return false
		}
		return true
	}
	switch name {
	case settings.SourceWikidata:
		if s.Wikidata != nil && ids.TMDB > 0 {
			id := strconv.FormatInt(ids.TMDB, 10)
			facts, err := s.Wikidata.FactsOf(ctx, wikidata.FactFilm, []string{id})
			found, known := facts[id]
			return FromWikidata(found), report(err) && known
		}
	case settings.MovieSourceMDBList:
		if s.MDBList != nil && s.MDBList.Configured() {
			media, err := s.MDBList.Movie(ctx, ids.TMDB, ids.IMDb)
			return FromMDBList(media), report(err, mdblist.ErrNotFound)
		}
	case settings.MovieSourceOMDb:
		if s.OMDb != nil && s.OMDb.Configured() && ids.IMDb != "" {
			movie, err := s.OMDb.Movie(ctx, ids.IMDb)
			return FromOMDb(movie), report(err, omdb.ErrNotFound)
		}
	case settings.MovieSourceFanart:
		if s.Fanart != nil && s.Fanart.Configured() {
			art, err := s.Fanart.Movie(ctx, ids.TMDB, ids.IMDb)
			return FromFanart(art), report(err, fanart.ErrNotFound)
		}
	case settings.TVSourceTVDB:
		if s.TVDB == nil {
			return Movie{}, false
		}
		remote := ids.IMDb
		if remote == "" && ids.TMDB > 0 {
			remote = strconv.FormatInt(ids.TMDB, 10)
		}
		id, found, err := s.TVDB.MovieByRemoteID(ctx, remote)
		if !report(err, tvdb.ErrNotFound) || !found {
			return Movie{}, false
		}
		movie, err := s.TVDB.Movie(ctx, id)
		return FromTVDB(movie), report(err, tvdb.ErrNotFound)
	}
	return Movie{}, false
}

func (s Service) askProxy(ctx context.Context, ids IDs) (radarrmeta.Movie, error) {
	if ids.TMDB > 0 {
		return s.Proxy.Movie(ctx, ids.TMDB)
	}
	return s.Proxy.ByIMDb(ctx, ids.IMDb)
}

// IDBridge is Wikidata as a fetch asks it: the ids its item records for a film, and what it says of it.
type IDBridge interface {
	MovieIDs(ctx context.Context, tmdb int64, imdb string) (wikidata.WorkIDs, bool, error)
	FactsOf(ctx context.Context, kind wikidata.FactKind, ids []string) (map[string]wikidata.WorkFacts, error)
}

// Warmer is Wikidata read in bulk ahead of the lookups that follow.
type Warmer interface {
	Warm(ctx context.Context, kind wikidata.FactKind, ids []string) error
}

// WarmWikidata reads at once what Wikidata says of every film about to be refreshed whose library asks it, so each
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
		if provider, err := s.Reader.ProviderIDs(ctx, candidate.WorkID); err == nil && provider[ProviderTMDB] != "" {
			ids = append(ids, provider[ProviderTMDB])
		}
	}
	if err := warmer.Warm(ctx, wikidata.FactFilm, ids); err != nil {
		s.warn("wikidata", 0, err)
	}
}

// bridgeIDs fills in the ids Wikidata's item records and the film lacks; first in the library's order, Wikidata also
// replaces the IMDb id it disagrees with. TMDB's id, when known, is what found the item and is never changed.
func (s Service) bridgeIDs(ctx context.Context, ids IDs, order []string) IDs {
	at := slices.Index(order, settings.SourceWikidata)
	if s.Wikidata == nil || at < 0 {
		return ids
	}
	found, ok, err := s.Wikidata.MovieIDs(ctx, ids.TMDB, ids.IMDb)
	if err != nil {
		s.warn("wikidata", ids.TMDB, err)
		return ids
	}
	if !ok {
		return ids
	}
	if ids.TMDB == 0 {
		ids.TMDB = found.TMDB
	}
	if found.IMDb != "" && (ids.IMDb == "" || at == 0) {
		ids.IMDb = found.IMDb
	}
	return ids
}

// completeIDs finds TMDB's id from an IMDb id when only that is known and TMDB can map it.
func (s Service) completeIDs(ctx context.Context, ids IDs) IDs {
	if ids.TMDB > 0 || ids.IMDb == "" || s.TMDB == nil || !s.TMDB.Configured() {
		return ids
	}
	hit, err := s.TMDB.FindMovieByIMDb(ctx, ids.IMDb)
	if err != nil {
		if !errors.Is(err, tmdb.ErrNotFound) {
			s.warn("tmdb", 0, err)
		}
		return ids
	}
	ids.TMDB = hit.ID
	return ids
}

// Candidate is one movie a search found.
type Candidate struct {
	TMDBID   int64
	IMDbID   string
	Title    string
	Year     int
	Overview string
	Poster   string
}

// Search finds movies by name through TMDB, else Radarr's service; an empty answer is not an error.
func (s Service) Search(ctx context.Context, term string, year, limit int) ([]Candidate, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, nil
	}
	var failure error
	if s.TMDB != nil && s.TMDB.Configured() {
		hits, err := s.TMDB.SearchMovies(ctx, term, year)
		if err == nil {
			candidates := make([]Candidate, 0, len(hits))
			for _, hit := range hits {
				candidates = append(candidates, Candidate{
					TMDBID: hit.ID, Title: hit.Title, Year: yearOf(hit.ReleaseDate), Overview: hit.Overview, Poster: tmdb.ImageURL(hit.PosterPath),
				})
			}
			return capped(candidates, limit), nil
		}
		failure = err
		s.warn("tmdb", 0, err)
	}
	if s.Proxy != nil {
		hits, err := s.Proxy.Search(ctx, term, year)
		if err == nil {
			candidates := make([]Candidate, 0, len(hits))
			for _, hit := range hits {
				candidates = append(candidates, Candidate{TMDBID: hit.TMDBID, IMDbID: hit.IMDbID, Title: hit.Title, Year: hit.Year, Overview: hit.Overview, Poster: posterOf(hit.Images)})
			}
			return capped(candidates, limit), nil
		}
		failure = err
		s.warn("radarr", 0, err)
	}
	if failure != nil {
		return nil, fmt.Errorf("movie: search %q: %w", term, failure)
	}
	return nil, ErrNoSource
}

func posterOf(images []radarrmeta.Image) string {
	for _, image := range images {
		if strings.EqualFold(image.CoverType, "poster") {
			return image.URL
		}
	}
	return ""
}

func capped(candidates []Candidate, limit int) []Candidate {
	if limit > 0 && len(candidates) > limit {
		return candidates[:limit]
	}
	return candidates
}

// AddRequest is one movie a person asked to hold.
type AddRequest struct {
	IDs        IDs
	CategoryID int64
	ProfileID  int64
	Monitor    MonitorOption
	// Availability is the person's own choice of when the movie is wanted; empty takes the library's.
	Availability Availability
	AddedBy      *int64
	// Fallback is stored when no metadata source knows the movie; nil refuses the add then.
	Fallback *Movie
}

// AddResult is what an add stored.
type AddResult struct {
	WorkID  int64
	ItemID  int64
	Created bool
	Movie   Movie
}

// availability is the minimum availability a movie of the library starts with.
func (s Service) availability(categoryID int64, chosen Availability) Availability {
	if chosen.Valid() {
		return chosen
	}
	if s.AvailabilityOf != nil {
		if library := s.AvailabilityOf(categoryID); library.Valid() {
			return library
		}
	}
	return DefaultAvailability
}

// Add stores a movie and its one wanted item. Adding what the library already holds is not an error: the movie is
// refreshed and its item left as it is (Invariant 71).
func (s Service) Add(ctx context.Context, request AddRequest) (AddResult, error) {
	monitor := request.Monitor
	if monitor == "" {
		monitor = MonitorMovieOnly
	}
	if !monitor.Valid() {
		return AddResult{}, fmt.Errorf("movie: unknown monitor option %q", monitor)
	}
	movie, err := s.FetchFor(ctx, request.CategoryID, request.IDs)
	if errors.Is(err, ErrNoSource) && request.Fallback != nil {
		movie, err = *request.Fallback, nil
	}
	if err != nil {
		return AddResult{}, err
	}
	now := s.Clock.Now()
	availability := s.availability(request.CategoryID, request.Availability)
	stored, err := s.Series.Upsert(ctx, now, ToWrite(movie, WriteOptions{Availability: availability, Now: now, Country: s.country()}))
	if err != nil {
		return AddResult{}, err
	}
	if request.Availability.Valid() && request.Availability != s.availability(request.CategoryID, "") {
		if err := s.Series.SetManualField(ctx, now, stored.WorkID, FieldMinimumAvailability, string(request.Availability)); err != nil {
			return AddResult{}, err
		}
	}
	item, err := s.Desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: stored.WorkID, CategoryID: request.CategoryID, ProfileID: request.ProfileID,
		Monitored: monitor != MonitorNone, AddedBy: request.AddedBy, RequiredWithoutParts: 1,
	})
	if err != nil {
		return AddResult{}, fmt.Errorf("movie: wanted item: %w", err)
	}
	return AddResult{WorkID: stored.WorkID, ItemID: item.ID, Created: stored.Created, Movie: movie}, nil
}

// RefreshRequest names a movie the library holds.
type RefreshRequest struct {
	WorkID     int64
	CategoryID int64
}

// Refresh reads a movie the library holds again. Monitoring and the person's own fields are untouched.
func (s Service) Refresh(ctx context.Context, request RefreshRequest) (AddResult, error) {
	provider, err := s.Reader.ProviderIDs(ctx, request.WorkID)
	if err != nil {
		return AddResult{}, err
	}
	ids := IDs{IMDb: provider[ProviderIMDb]}
	ids.TMDB, _ = strconv.ParseInt(provider[ProviderTMDB], 10, 64)
	movie, err := s.FetchFor(ctx, request.CategoryID, ids)
	if err != nil {
		return AddResult{}, err
	}
	manual, err := s.Reader.ManualFields(ctx, request.WorkID)
	if err != nil {
		return AddResult{}, err
	}
	now := s.Clock.Now()
	availability := s.availability(request.CategoryID, Availability(manual[FieldMinimumAvailability]))
	stored, err := s.Series.Upsert(ctx, now, ToWrite(movie, WriteOptions{Availability: availability, Now: now, Country: s.country()}))
	if err != nil {
		return AddResult{}, err
	}
	return AddResult{WorkID: stored.WorkID, Movie: movie}, nil
}

// ApplyMonitor sets a movie's monitoring as Radarr's Monitor choice says. "Movie and collection" also monitors the
// franchise it belongs to, which the franchise pass then fills with the other films.
func (s Service) ApplyMonitor(ctx context.Context, _, workID int64, option MonitorOption) error {
	if !option.Valid() {
		return fmt.Errorf("movie: unknown monitor option %q", option)
	}
	items, err := s.Desired.AlbumItemsForWork(ctx, workID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := s.Desired.SetItemMonitored(ctx, item.ID, option != MonitorNone); err != nil {
			return err
		}
	}
	if option == MonitorMovieAndCollection {
		return s.Franchises.SetMonitoredOf(ctx, workID, true)
	}
	return nil
}

// IsExcluded reports whether the user deleted this movie and asked not to see it again.
func (s Service) IsExcluded(ctx context.Context, ids IDs) bool {
	excluded, err := s.Exclusions.Excluded(ctx, idWrites(ids))
	if err != nil {
		s.warn("exclusion check", ids.TMDB, err)
		return false
	}
	return excluded
}
