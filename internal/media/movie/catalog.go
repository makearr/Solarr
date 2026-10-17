package movie

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

// Catalog is the movie module's add and refresh over the service: media.Adding, with what a library's add dialog needs.
type Catalog struct {
	Service    Service
	Categories interface {
		Category(id int64) (settings.Category, bool)
		ProfileFor(category settings.Category, fallback int64) int64
	}
	DefaultProfileID int64
	FranchiseStore   library.Franchises
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
		if id, held, err := c.Service.Series.WorkByProviderID(ctx, MediaType, candidate.Provider, candidate.ExternalID); err == nil && held {
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

var _ media.Franchising = Catalog{}

// MonitorFranchise implements media.Franchising: following adds every film of the franchise the library lacks and does
// not exclude, monitored; without a TMDB key only the flag is kept, since only TMDB lists a franchise's films.
func (c Catalog) MonitorFranchise(ctx context.Context, categoryID, franchiseID int64, monitored bool) (int, error) {
	row, err := c.FranchiseStore.Get(ctx, franchiseID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, media.ErrNoFranchise
	}
	if err != nil {
		return 0, err
	}
	category, ok := c.Categories.Category(categoryID)
	if !ok {
		return 0, fmt.Errorf("%w: no such library", media.ErrInvalidAdd)
	}
	added, total := 0, int64(0)
	if monitored && c.Service.TMDB != nil && c.Service.TMDB.Configured() {
		if added, total, err = c.addMissing(ctx, category, categoryID, row); err != nil {
			return 0, err
		}
	}
	if err := c.FranchiseStore.SetMonitored(ctx, franchiseID, monitored, total); err != nil {
		return added, err
	}
	return added, nil
}

// addMissing adds the films TMDB lists in the franchise that the library neither holds nor excludes.
func (c Catalog) addMissing(ctx context.Context, category settings.Category, categoryID int64, row library.FranchiseRow) (added int, total int64, err error) {
	// A franchise with no TMDB id has nothing to list: only its flag is kept.
	id, _ := strconv.ParseInt(row.ExternalID, 10, 64)
	if id <= 0 {
		return 0, 0, nil
	}
	detail, err := c.Service.TMDB.Collection(ctx, id)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	rows, err := c.FranchiseStore.InCategory(ctx, MediaType, categoryID)
	if err != nil {
		return 0, 0, err
	}
	held := map[int64]bool{}
	for _, member := range franchiseMembers(rows, row.ID) {
		ids, err := c.Service.Reader.ProviderIDs(ctx, member.WorkID)
		if err != nil {
			return 0, 0, err
		}
		if tmdbID, err := strconv.ParseInt(ids[ProviderTMDB], 10, 64); err == nil {
			held[tmdbID] = true
		}
	}
	profile := c.Categories.ProfileFor(category, c.DefaultProfileID)
	for _, part := range detail.Parts {
		ids := IDs{TMDB: part.ID}
		if held[part.ID] || c.Service.IsExcluded(ctx, ids) {
			continue
		}
		if _, err := c.Service.Add(ctx, AddRequest{IDs: ids, CategoryID: categoryID, ProfileID: profile, Monitor: MonitorMovieOnly}); err != nil {
			return added, int64(len(detail.Parts)), fmt.Errorf("add %q: %w", part.Title, err)
		}
		added++
	}
	return added, int64(len(detail.Parts)), nil
}

func franchiseMembers(rows []library.FranchiseRow, id int64) []library.FranchiseMember {
	for _, row := range rows {
		if row.ID == id {
			return row.Members
		}
	}
	return nil
}

// FranchiseFacts implements media.Franchising: TMDB's picture and film count of each franchise it names.
func (c Catalog) FranchiseFacts(ctx context.Context, franchises []media.FranchiseRef) map[int64]media.FranchiseFact {
	found := map[int64]media.FranchiseFact{}
	if c.Service.TMDB == nil || !c.Service.TMDB.Configured() {
		return found
	}
	for _, franchise := range franchises {
		id, _ := strconv.ParseInt(franchise.ExternalID, 10, 64)
		if id <= 0 {
			continue
		}
		if detail, err := c.Service.TMDB.Collection(ctx, id); err == nil {
			found[franchise.ID] = media.FranchiseFact{PosterURL: tmdb.ImageURL(detail.PosterPath), Total: len(detail.Parts)}
		}
	}
	return found
}

// MissingFranchiseMembers implements media.Franchising: the films TMDB lists in the franchise that the library lacks and
// does not exclude.
func (c Catalog) MissingFranchiseMembers(ctx context.Context, categoryID, franchiseID int64) ([]media.FranchiseMember, error) {
	row, err := c.FranchiseStore.Get(ctx, franchiseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, media.ErrNoFranchise
	}
	if err != nil {
		return nil, err
	}
	id, _ := strconv.ParseInt(row.ExternalID, 10, 64)
	if id <= 0 || c.Service.TMDB == nil || !c.Service.TMDB.Configured() {
		return nil, nil
	}
	detail, err := c.Service.TMDB.Collection(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	rows, err := c.FranchiseStore.InCategory(ctx, MediaType, categoryID)
	if err != nil {
		return nil, err
	}
	held := map[int64]bool{}
	for _, member := range franchiseMembers(rows, franchiseID) {
		if ids, err := c.Service.Reader.ProviderIDs(ctx, member.WorkID); err == nil {
			if tmdbID, err := strconv.ParseInt(ids[ProviderTMDB], 10, 64); err == nil {
				held[tmdbID] = true
			}
		}
	}
	var missing []media.FranchiseMember
	for _, part := range detail.Parts {
		if held[part.ID] || c.Service.IsExcluded(ctx, IDs{TMDB: part.ID}) {
			continue
		}
		member := media.FranchiseMember{Title: part.Title, Provider: ProviderTMDB, ExternalID: strconv.FormatInt(part.ID, 10), PosterURL: tmdb.ImageURL(part.PosterPath)}
		if len(part.ReleaseDate) >= 4 {
			member.Year, _ = strconv.Atoi(part.ReleaseDate[:4])
		}
		missing = append(missing, member)
	}
	return missing, nil
}
