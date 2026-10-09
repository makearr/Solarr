package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// SeriesCatalog satisfies apiserver.SeriesCatalog over the TV service and the library's series reader.
type SeriesCatalog struct {
	Service          tv.Service
	Reader           library.SeriesReader
	Desired          library.Desired
	Categories       Categories
	DefaultProfileID int64
	Clock            clock.Clock
	// Remover sends a deleted series' files to the recycle bin.
	Remover FileRemover
}

// LookupWorks implements media.Adding for TV: the sources' series, each marked when the library already holds it.
func (c SeriesCatalog) LookupWorks(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	if len([]rune(lookup.Query)) < 2 {
		return nil, apiserver.ErrQueryTooShort
	}
	found, err := c.Service.Search(ctx, lookup.Query, lookup.Limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", apiserver.ErrSeriesSourceUnavailable, err)
	}
	candidates := make([]media.AddCandidate, len(found))
	for i, series := range found {
		candidate := media.AddCandidate{
			Kind: "series", Provider: tv.ProviderTVDB, ExternalID: strconv.FormatInt(series.TVDBID, 10), Title: series.Title,
			Subtitle: series.Network, Overview: series.Overview, PosterURL: series.Poster, Status: string(series.Status), Year: int64(series.Year),
		}
		if series.TVDBID == 0 {
			candidate.Provider, candidate.ExternalID = tv.ProviderTMDB, strconv.FormatInt(series.TMDBID, 10)
		}
		if id, held, err := c.Service.Series.WorkByProviderID(ctx, candidate.Provider, candidate.ExternalID); err == nil && held {
			candidate.LibraryID = id
		}
		candidates[i] = candidate
	}
	return candidates, nil
}

// AddWork implements media.Adding for TV: the series with the chosen monitoring, Sonarr's add dialog. It starts no
// search; the caller searches the series when asked to.
func (c SeriesCatalog) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	category, ok := c.Categories.Category(request.CategoryID)
	if !ok {
		return media.AddResult{}, apiserver.ErrSeriesNotFound
	}
	id, valid := tv.ParseID(request.ExternalID)
	if (request.Provider != tv.ProviderTVDB && request.Provider != tv.ProviderTMDB) || !valid {
		return media.AddResult{}, fmt.Errorf("%w: only a numeric tvdb or tmdb id can be added", apiserver.ErrDiscoverUnsupportedMediaType)
	}
	ids := tv.IDs{TVDB: id}
	if request.Provider == tv.ProviderTMDB {
		ids = tv.IDs{TMDB: id}
	}
	monitor := tv.MonitorAll
	if request.Option != "" {
		monitor = tv.MonitorOption(request.Option)
	}
	if !request.Monitored {
		monitor = tv.MonitorNone
	}
	profile := c.Categories.ProfileFor(category, c.DefaultProfileID)
	if request.ProfileID != nil {
		profile = *request.ProfileID
	}
	if err := (WorkExclusions{Exclusions: c.Service.Exclusions}).Allow(ctx, tv.MediaType, request.Provider, request.ExternalID); err != nil {
		return media.AddResult{}, err
	}
	userID := request.UserID
	result, err := c.Service.Add(ctx, tv.AddRequest{
		IDs: ids, CategoryID: request.CategoryID, ProfileID: profile, Monitor: monitor, Specials: request.Specials, AddedBy: &userID,
	})
	if errors.Is(err, tv.ErrNoSource) {
		return media.AddResult{}, fmt.Errorf("%w: %w", apiserver.ErrSeriesSourceUnavailable, err)
	}
	if err != nil {
		return media.AddResult{}, err
	}
	return media.AddResult{Kind: "series", WorkID: result.WorkID}, nil
}

// Refresh re-reads one series from the sources; monitoring and season items stay as they are.
func (c SeriesCatalog) Refresh(ctx context.Context, categoryID, seriesID int64) error {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return translateSeriesError(err)
	}
	profile := c.DefaultProfileID
	if category, ok := c.Categories.Category(categoryID); ok {
		profile = c.Categories.ProfileFor(category, profile)
	}
	_, err := c.Service.Refresh(ctx, tv.RefreshRequest{WorkID: seriesID, CategoryID: categoryID, ProfileID: profile})
	if errors.Is(err, tv.ErrNoSource) {
		return fmt.Errorf("%w: %w", apiserver.ErrSeriesSourceUnavailable, err)
	}
	return err
}

// PosterOptions lists the posters the providers hold for a series, the thumbnail being the poster itself.
func (c SeriesCatalog) PosterOptions(ctx context.Context, categoryID, seriesID int64) ([]apiserver.ImageOptionRow, error) {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return nil, translateSeriesError(err)
	}
	images, err := c.Reader.Images(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	var options []apiserver.ImageOptionRow
	for _, image := range images {
		if image.Kind == "poster" {
			options = append(options, apiserver.ImageOptionRow{URL: image.URL, ThumbURL: image.URL, Source: image.Source, Label: "Poster"})
		}
	}
	return options, nil
}

// SetPoster puts the chosen poster first among the series' pictures as the user's own, keeping the other pictures.
func (c SeriesCatalog) SetPoster(ctx context.Context, categoryID, seriesID int64, url string) error {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return translateSeriesError(err)
	}
	images, err := c.Reader.Images(ctx, seriesID)
	if err != nil {
		return err
	}
	chosen := []tv.Image{}
	rest := []tv.Image{}
	for _, image := range images {
		entry := tv.Image{Kind: image.Kind, URL: image.URL, Source: image.Source}
		if image.Kind == "poster" && image.URL == url {
			chosen = append(chosen, entry)
		} else {
			rest = append(rest, entry)
		}
	}
	if len(chosen) == 0 {
		return apiserver.ErrImageNotOffered
	}
	value, err := json.Marshal(append(chosen, rest...))
	if err != nil {
		return err
	}
	return c.Service.Series.SetManualField(ctx, c.Clock.Now(), seriesID, "images", string(value))
}

func translateSeriesError(err error) error {
	if errors.Is(err, library.ErrSeriesNotFound) {
		return apiserver.ErrSeriesNotFound
	}
	return err
}

func setString(into **string, value string) {
	if value != "" {
		*into = &value
	}
}
