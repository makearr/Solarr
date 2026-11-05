package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// SeriesCatalog is TV's add, refresh and picture choice over the TV service and the library's series reader.
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

func (c SeriesCatalog) shared() works.Catalog {
	return works.Catalog{
		Source: tv.CatalogSource{Service: c.Service}, Categories: c.Categories, DefaultProfileID: c.DefaultProfileID,
		AllowAgain: func(ctx context.Context, provider, externalID string) error {
			return WorkExclusions{Exclusions: c.Service.Exclusions}.Allow(ctx, tv.MediaType, provider, externalID)
		},
	}
}

// LookupWorks implements media.Adding for TV: Skyhook's or TMDB's series for a name, each marked when the library holds it.
func (c SeriesCatalog) LookupWorks(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	return c.shared().LookupWorks(ctx, lookup)
}

// AddWork implements media.Adding for TV: the series with the chosen monitoring, Sonarr's add dialog. It starts no
// search; the caller searches the series when asked to.
func (c SeriesCatalog) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	return c.shared().AddWork(ctx, request)
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

// WorkPictures implements media.Picturing for TV: the posters the providers hold.
func (c SeriesCatalog) WorkPictures(ctx context.Context, seriesID int64) ([]media.ImageOption, error) {
	return c.pictures().WorkPictures(ctx, seriesID)
}

// PickWorkPicture implements media.Picturing for TV: the chosen poster goes first as the user's own.
func (c SeriesCatalog) PickWorkPicture(ctx context.Context, seriesID int64, url string) error {
	return c.pictures().PickWorkPicture(ctx, seriesID, url)
}

func (c SeriesCatalog) pictures() ProviderPictures {
	return ProviderPictures{Reader: c.Reader, Series: c.Service.Series, Clock: c.Clock}
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

// NumberingChoices implements media.Renumbering for TV.
func (c SeriesCatalog) NumberingChoices(ctx context.Context, seriesID int64) ([]media.Choice, string, error) {
	return c.Service.NumberingChoices(ctx, seriesID)
}

// PlanNumbering implements media.Renumbering for TV.
func (c SeriesCatalog) PlanNumbering(ctx context.Context, seriesID int64, basis string) (media.NumberingPlan, error) {
	return c.Service.PlanNumbering(ctx, seriesID, basis)
}

// ApplyNumbering implements media.Renumbering for TV, refreshing the series in its first library afterwards.
func (c SeriesCatalog) ApplyNumbering(ctx context.Context, seriesID int64, basis string) (media.NumberingPlan, error) {
	categoryID, err := c.Reader.FirstCategory(ctx, seriesID)
	if err != nil {
		return media.NumberingPlan{}, err
	}
	profile := c.DefaultProfileID
	if category, ok := c.Categories.Category(categoryID); ok {
		profile = c.Categories.ProfileFor(category, profile)
	}
	return c.Service.ApplyNumbering(ctx, tv.RefreshRequest{WorkID: seriesID, CategoryID: categoryID, ProfileID: profile}, basis)
}
