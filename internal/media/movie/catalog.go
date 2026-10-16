package movie

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/media"
)

// Catalog is the movie module's add and refresh over the service: media.Adding, with what a library's add dialog needs.
type Catalog struct {
	Service    Service
	Categories interface {
		Category(id int64) (settings.Category, bool)
		ProfileFor(category settings.Category, fallback int64) int64
	}
	DefaultProfileID int64
	// AllowAgain lifts the exclusion of a movie a person removed and now adds on purpose; nil lifts nothing.
	AllowAgain func(ctx context.Context, provider, externalID string) error
}

var _ media.Adding = Catalog{}

// LookupWorks implements media.Adding: the sources' films for a name, "Title (2001)" narrowing it to a year, each marked
// when the library already holds it.
func (c Catalog) LookupWorks(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	if len([]rune(lookup.Query)) < 2 {
		return nil, media.ErrQueryTooShort
	}
	term, year := lookup.Query, 0
	if info, ok := parsemovie.Parse(lookup.Query); ok && info.Year != 0 {
		term, year = info.Title, info.Year
	}
	found, err := c.Service.Search(ctx, term, year, lookup.Limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	candidates := make([]media.AddCandidate, len(found))
	for i, film := range found {
		candidate := media.AddCandidate{
			Kind: "movie", Provider: ProviderTMDB, ExternalID: strconv.FormatInt(film.TMDBID, 10), Title: film.Title,
			Overview: film.Overview, PosterURL: film.Poster, Year: int64(film.Year),
		}
		if film.TMDBID == 0 {
			candidate.Provider, candidate.ExternalID = ProviderIMDb, film.IMDbID
		}
		if id, held, err := c.Service.Series.WorkByProviderID(ctx, candidate.Provider, candidate.ExternalID); err == nil && held {
			candidate.LibraryID = id
		}
		candidates[i] = candidate
	}
	return candidates, nil
}

// AddWork implements media.Adding: the film with Radarr's Monitor choice. It starts no search; the caller searches the
// movie when asked to.
func (c Catalog) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	category, ok := c.Categories.Category(request.CategoryID)
	if !ok {
		return media.AddResult{}, fmt.Errorf("%w: no such library", media.ErrInvalidAdd)
	}
	var ids IDs
	switch request.Provider {
	case ProviderTMDB:
		id, err := strconv.ParseInt(request.ExternalID, 10, 64)
		if err != nil || id <= 0 {
			return media.AddResult{}, fmt.Errorf("%w: %q is not a TMDB id", media.ErrInvalidAdd, request.ExternalID)
		}
		ids.TMDB = id
	case ProviderIMDb:
		ids.IMDb = request.ExternalID
	default:
		return media.AddResult{}, fmt.Errorf("%w: only a TMDB or IMDb id can be added", media.ErrInvalidAdd)
	}
	monitor := MonitorMovieOnly
	if request.Option != "" {
		monitor = MonitorOption(request.Option)
	}
	if !request.Monitored {
		monitor = MonitorNone
	}
	if !monitor.Valid() {
		return media.AddResult{}, fmt.Errorf("%w: %q is not a monitor choice", media.ErrInvalidAdd, request.Option)
	}
	profile := c.Categories.ProfileFor(category, c.DefaultProfileID)
	if request.ProfileID != nil {
		profile = *request.ProfileID
	}
	if c.AllowAgain != nil {
		if err := c.AllowAgain(ctx, request.Provider, request.ExternalID); err != nil {
			return media.AddResult{}, err
		}
	}
	var addedBy *int64
	if request.UserID != 0 {
		addedBy = &request.UserID
	}
	availability := Availability(request.Choices[FieldMinimumAvailability])
	if availability != "" && !availability.Valid() {
		return media.AddResult{}, fmt.Errorf("%w: %q is not a minimum availability", media.ErrInvalidAdd, availability)
	}
	result, err := c.Service.Add(ctx, AddRequest{IDs: ids, CategoryID: request.CategoryID, ProfileID: profile, Monitor: monitor, Availability: availability, AddedBy: addedBy})
	if errors.Is(err, ErrNoSource) {
		return media.AddResult{}, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	if err != nil {
		return media.AddResult{}, err
	}
	return media.AddResult{Kind: "movie", WorkID: result.WorkID, Created: result.Created}, nil
}

// Refresh re-reads one movie from the sources; monitoring and the person's own fields stay as they are.
func (c Catalog) Refresh(ctx context.Context, categoryID, workID int64) error {
	_, err := c.Service.Refresh(ctx, RefreshRequest{WorkID: workID, CategoryID: categoryID})
	if errors.Is(err, ErrNoSource) {
		return fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	return err
}
