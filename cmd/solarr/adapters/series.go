package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// SeriesCatalog satisfies apiserver.SeriesCatalog over the TV service and the library's series reader.
type SeriesCatalog struct {
	Service          tv.Service
	Reader           library.SeriesReader
	Desired          library.Desired
	Categories       Categories
	DefaultProfileID int64
	Clock            clock.Clock
}

// List maps a page of series.
func (c SeriesCatalog) List(ctx context.Context, categoryID int64, after paginate.TextCursor, limit int) ([]gen.SeriesSummary, error) {
	rows, err := c.Reader.List(ctx, categoryID, library.SeriesCursor{SortTitle: after.Sort, ID: after.ID}, int64(limit), c.Clock.Now())
	if err != nil {
		return nil, err
	}
	views := make([]gen.SeriesSummary, len(rows))
	for i, row := range rows {
		views[i] = summaryView(row)
	}
	return views, nil
}

// Get maps one series with its seasons and episodes.
func (c SeriesCatalog) Get(ctx context.Context, categoryID, seriesID int64) (gen.SeriesDetail, error) {
	now := c.Clock.Now()
	row, err := c.Reader.Get(ctx, categoryID, seriesID, now)
	if err != nil {
		return gen.SeriesDetail{}, translateSeriesError(err)
	}
	detail := gen.SeriesDetail{Summary: summaryView(row.Summary), Ids: row.IDs, Seasons: []gen.SeriesSeason{}, Episodes: make([]gen.SeriesEpisode, len(row.Episodes))}
	if len(row.Aliases) > 0 {
		detail.Aliases = &row.Aliases
	}
	if row.RuntimeMs > 0 {
		minutes := int(row.RuntimeMs / 60_000)
		detail.RuntimeMinutes = &minutes
	}
	setString(&detail.OriginalLanguage, row.Language)
	setString(&detail.OriginalCountry, row.Summary.Fields["original_country"])
	setString(&detail.AirTime, row.Summary.Fields["air_time"])
	posters := seasonPosters(row.Summary.Fields["season_images"])
	for _, season := range row.Summary.Seasons {
		view := gen.SeriesSeason{Number: season.Number, Monitored: season.Monitored, Counts: countsView(season.Counts), DesiredItemId: &season.DesiredItemID}
		if url := posters[season.Number]; url != "" {
			view.PosterUrl = &url
		}
		detail.Seasons = append(detail.Seasons, view)
	}
	for i, episode := range row.Episodes {
		detail.Episodes[i] = episodeView(episode, now)
	}
	return detail, nil
}

func episodeView(episode library.SeriesEpisode, now time.Time) gen.SeriesEpisode {
	view := gen.SeriesEpisode{Id: episode.PartID, Season: episode.Season, Number: episode.Number, Monitored: episode.Monitored}
	setString(&view.Title, episode.Title)
	setString(&view.AirDate, episode.Date)
	if episode.AvailableAt > 0 {
		at := time.Unix(episode.AvailableAt, 0).UTC()
		view.AirTime = &at
	}
	if episode.AvailableConfidence != "" {
		confidence := gen.SeriesEpisodeAirConfidence(episode.AvailableConfidence)
		view.AirConfidence = &confidence
	}
	if episode.RuntimeMs > 0 {
		minutes := int(episode.RuntimeMs / 60_000)
		view.RuntimeMinutes = &minutes
	}
	switch {
	case episode.Present:
		view.State = gen.SeriesEpisodeStatePresent
	case !episode.Monitored:
		view.State = gen.SeriesEpisodeStateUnmonitored
	case episode.AvailableAt > now.Unix() || (episode.AvailableAt == 0 && episode.Date > now.UTC().Format("2006-01-02")):
		view.State = gen.SeriesEpisodeStateUnaired
	default:
		view.State = gen.SeriesEpisodeStateMissing
	}
	return view
}

// Lookup searches the sources and marks what the library already holds.
func (c SeriesCatalog) Lookup(ctx context.Context, _ int64, query string, limit int) ([]gen.SeriesCandidate, error) {
	found, err := c.Service.Search(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", apiserver.ErrSeriesSourceUnavailable, err)
	}
	views := make([]gen.SeriesCandidate, len(found))
	for i, candidate := range found {
		view := gen.SeriesCandidate{Provider: tv.ProviderTVDB, ExternalId: strconv.FormatInt(candidate.TVDBID, 10), Title: candidate.Title}
		if candidate.Year > 0 {
			year := int64(candidate.Year)
			view.Year = &year
		}
		setString(&view.Network, candidate.Network)
		setString(&view.Overview, candidate.Overview)
		setString(&view.PosterUrl, candidate.Poster)
		if candidate.Status != "" {
			status := gen.SeriesCandidateStatus(candidate.Status)
			view.Status = &status
		}
		if id, held, err := c.Service.Series.WorkByProviderID(ctx, tv.ProviderTVDB, view.ExternalId); err == nil && held {
			view.LibraryId = &id
		}
		views[i] = view
	}
	return views, nil
}

// Add stores a series in a library with the chosen monitoring.
func (c SeriesCatalog) Add(ctx context.Context, userID int64, request gen.AddSeriesRequest) (gen.AddSeriesResult, error) {
	category, ok := c.Categories.Category(request.CategoryId)
	if !ok {
		return gen.AddSeriesResult{}, apiserver.ErrSeriesNotFound
	}
	provider := derefString(request.Provider, tv.ProviderTVDB)
	id, valid := tv.ParseID(request.ExternalId)
	if provider != tv.ProviderTVDB || !valid {
		return gen.AddSeriesResult{}, fmt.Errorf("%w: only a numeric tvdb id can be added", apiserver.ErrDiscoverUnsupportedMediaType)
	}
	monitor := tv.MonitorAll
	if request.Monitor != nil {
		monitor = tv.MonitorOption(*request.Monitor)
	}
	profile := c.Categories.ProfileFor(category, c.DefaultProfileID)
	if request.QualityProfileId != nil {
		profile = *request.QualityProfileId
	}
	result, err := c.Service.Add(ctx, tv.AddRequest{
		IDs: tv.IDs{TVDB: id}, CategoryID: request.CategoryId, ProfileID: profile, Monitor: monitor,
		Specials: request.MonitorSpecials != nil && *request.MonitorSpecials, AddedBy: &userID,
	})
	if err != nil {
		if errors.Is(err, tv.ErrNoSource) {
			return gen.AddSeriesResult{}, fmt.Errorf("%w: %w", apiserver.ErrSeriesSourceUnavailable, err)
		}
		return gen.AddSeriesResult{}, err
	}
	return gen.AddSeriesResult{SeriesId: result.WorkID, Created: result.Created, Episodes: result.Episodes, Seasons: result.Seasons}, nil
}

// Refresh re-reads one series from the sources; monitoring and season items stay as they are.
func (c SeriesCatalog) Refresh(ctx context.Context, categoryID, seriesID int64) (gen.AddSeriesResult, error) {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return gen.AddSeriesResult{}, translateSeriesError(err)
	}
	ids, err := c.Reader.ProviderIDs(ctx, seriesID)
	if err != nil {
		return gen.AddSeriesResult{}, err
	}
	tvdb, _ := tv.ParseID(ids[tv.ProviderTVDB])
	maze, _ := tv.ParseID(ids[tv.ProviderTVmaze])
	result, err := c.Service.Add(ctx, tv.AddRequest{IDs: tv.IDs{TVDB: tvdb, TVmaze: maze}, CategoryID: categoryID, Monitor: tv.MonitorNone})
	if err != nil {
		if errors.Is(err, tv.ErrNoSource) {
			return gen.AddSeriesResult{}, fmt.Errorf("%w: %w", apiserver.ErrSeriesSourceUnavailable, err)
		}
		return gen.AddSeriesResult{}, err
	}
	return gen.AddSeriesResult{SeriesId: result.WorkID, Created: false, Episodes: result.Episodes, Seasons: result.Seasons}, nil
}

// SetEpisodeMonitored monitors or unmonitors one episode of a series in the library.
func (c SeriesCatalog) SetEpisodeMonitored(ctx context.Context, categoryID, seriesID, partID int64, monitored bool) error {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return translateSeriesError(err)
	}
	err := c.Desired.SetPartMonitored(ctx, seriesID, partID, monitored)
	if errors.Is(err, library.ErrPartNotFound) {
		return apiserver.ErrSeriesNotFound
	}
	return err
}

// SetSeasonMonitored monitors or unmonitors a whole season.
func (c SeriesCatalog) SetSeasonMonitored(ctx context.Context, categoryID, seriesID int64, season int, monitored bool) error {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return translateSeriesError(err)
	}
	found, err := c.Desired.SetSeasonMonitored(ctx, seriesID, tv.SeasonRef(season), monitored)
	if err != nil {
		return err
	}
	if !found {
		return apiserver.ErrSeriesNotFound
	}
	return nil
}

func translateSeriesError(err error) error {
	if errors.Is(err, library.ErrSeriesNotFound) {
		return apiserver.ErrSeriesNotFound
	}
	return err
}

func summaryView(row library.SeriesSummary) gen.SeriesSummary {
	view := gen.SeriesSummary{
		Id: row.WorkID, Title: row.Title, SortTitle: row.SortTitle, Monitored: row.Monitored, Counts: countsView(row.Counts), AddedAt: row.AddedAt,
	}
	for _, season := range row.Seasons {
		if season.Number > 0 {
			view.Seasons++
		}
	}
	if row.Year > 0 {
		view.Year = &row.Year
	}
	if status := row.Fields["status"]; status != "" {
		typed := gen.SeriesSummaryStatus(status)
		view.Status = &typed
	}
	setString(&view.Network, row.Fields["network"])
	setString(&view.Overview, row.Description)
	setString(&view.PosterUrl, posterOf(row.Fields["images"]))
	if row.NextAvailable > 0 {
		next := time.Unix(row.NextAvailable, 0).UTC()
		view.NextAiring = &next
	}
	if row.ProfileID > 0 {
		view.QualityProfileId = &row.ProfileID
	}
	if len(row.Genres) > 0 {
		view.Genres = &row.Genres
	}
	return view
}

func countsView(counts library.SeriesCounts) gen.SeriesCounts {
	return gen.SeriesCounts{Total: int(counts.Total), Monitored: int(counts.Monitored), Present: int(counts.Present), AiredMissing: int(counts.AiredMissing)}
}

func posterOf(imagesJSON string) string {
	var images []tv.Image
	if json.Unmarshal([]byte(imagesJSON), &images) != nil {
		return ""
	}
	for _, image := range images {
		if image.Kind == "poster" {
			return image.URL
		}
	}
	return ""
}

func seasonPosters(seasonsJSON string) map[int]string {
	var seasons []tv.Season
	posters := map[int]string{}
	if json.Unmarshal([]byte(seasonsJSON), &seasons) == nil {
		for _, season := range seasons {
			posters[season.Number] = season.Poster
		}
	}
	return posters
}

func setString(into **string, value string) {
	if value != "" {
		*into = &value
	}
}

func derefString(value *string, fallback string) string {
	if value == nil || *value == "" {
		return fallback
	}
	return *value
}
