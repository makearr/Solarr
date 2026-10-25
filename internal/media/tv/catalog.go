package tv

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
)

// CatalogSource is works.Source for TV: series found at Skyhook or TMDB, held by their TheTVDB or TMDB id, added with
// Sonarr's monitor options.
type CatalogSource struct{ Service Service }

var _ works.Source = CatalogSource{}

// Lookup implements works.Source.
func (c CatalogSource) Lookup(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	found, err := c.Service.Search(ctx, lookup.Query, lookup.Limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	candidates := make([]media.AddCandidate, len(found))
	for i, series := range found {
		candidates[i] = media.AddCandidate{
			Kind: "series", Provider: ProviderTVDB, ExternalID: strconv.FormatInt(series.TVDBID, 10), Title: series.Title,
			Subtitle: series.Network, Overview: series.Overview, PosterURL: series.Poster, Status: string(series.Status), Year: int64(series.Year),
			Traits: series.Traits,
		}
		if series.TVDBID == 0 {
			candidates[i].Provider, candidates[i].ExternalID = ProviderTMDB, strconv.FormatInt(series.TMDBID, 10)
		}
	}
	return candidates, nil
}

// Held implements works.Source.
func (c CatalogSource) Held(ctx context.Context, provider, externalID string) (int64, bool) {
	id, held, err := c.Service.Series.WorkByProviderID(ctx, MediaType, provider, externalID)
	return id, err == nil && held
}

// Monitor implements works.Source: Sonarr's options, every episode when none is chosen.
func (CatalogSource) Monitor() (string, string, func(string) bool) {
	return string(MonitorAll), string(MonitorNone), func(option string) bool { return MonitorOption(option).Valid() }
}

// Add implements works.Source: the series by TheTVDB's id or TMDB's, with its specials when asked.
func (c CatalogSource) Add(ctx context.Context, request works.Add) (media.AddResult, error) {
	id, valid := ParseID(request.ExternalID)
	if (request.Provider != ProviderTVDB && request.Provider != ProviderTMDB) || !valid {
		return media.AddResult{}, fmt.Errorf("%w: only a numeric tvdb or tmdb id can be added", media.ErrInvalidAdd)
	}
	ids := IDs{TVDB: id}
	if request.Provider == ProviderTMDB {
		ids = IDs{TMDB: id}
	}
	result, err := c.Service.Add(ctx, AddRequest{
		IDs: ids, CategoryID: request.CategoryID, ProfileID: request.ProfileID, Monitor: MonitorOption(request.Option),
		Specials: request.Specials, AddedBy: request.AddedBy,
	})
	if errors.Is(err, ErrNoSource) {
		return media.AddResult{}, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	if err != nil {
		return media.AddResult{}, err
	}
	return media.AddResult{Kind: "series", WorkID: result.WorkID, Created: result.Created}, nil
}
