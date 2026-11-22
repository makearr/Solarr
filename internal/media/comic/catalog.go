package comic

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
)

// Catalog is the type's add dialog and refresh over a Service.
type Catalog struct {
	Service          Service
	Categories       works.Categories
	DefaultProfileID int64
	// AllowAgain lifts the exclusion of a series a person removed and now adds on purpose; nil lifts nothing.
	AllowAgain func(ctx context.Context, provider, externalID string) error
}

// AddKindSeries is the one thing the type is added by, the key of its add kind.
const AddKindSeries = "series"

var _ media.Adding = Catalog{}

func (c Catalog) shared() works.Catalog {
	return works.Catalog{
		Source: catalogSource{c.Service}, Categories: c.Categories, DefaultProfileID: c.DefaultProfileID, AllowAgain: c.AllowAgain,
		Overrides: c.Service.Desired,
	}
}

// LookupWorks implements media.Adding.
func (c Catalog) LookupWorks(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	return c.shared().LookupWorks(ctx, lookup)
}

// AddWork implements media.Adding.
func (c Catalog) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	return c.shared().AddWork(ctx, request)
}

// Refresh reads one series again; monitoring and the person's own fields stay as they are.
func (c Catalog) Refresh(ctx context.Context, categoryID, workID int64) error {
	_, err := c.Service.Refresh(ctx, categoryID, workID)
	if errors.Is(err, ErrNoSource) {
		return fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	return err
}

// catalogSource is works.Source for the comic type.
type catalogSource struct{ service Service }

// Lookup implements works.Source.
func (s catalogSource) Lookup(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	found, err := s.service.Search(ctx, lookup.CategoryID, strings.TrimSpace(lookup.Query), lookup.Limit)
	if errors.Is(err, ErrNoSource) {
		return nil, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	if err != nil {
		return nil, err
	}
	candidates := make([]media.AddCandidate, 0, len(found))
	for _, one := range found {
		subtitle := one.Publisher
		if one.Count > 0 {
			unit := "issues"
			if isMangaKind(s.service.kind(lookup.CategoryID)) {
				unit = "volumes"
			}
			subtitle = strings.TrimPrefix(subtitle+", "+strconv.Itoa(one.Count)+" "+unit, ", ")
		}
		candidates = append(candidates, media.AddCandidate{
			Kind: AddKindSeries, Provider: one.Provider, ExternalID: one.ID, Title: one.Title, Subtitle: subtitle,
			Overview: one.Overview, PosterURL: one.Cover, Year: int64(one.Year),
			Traits: media.Traits{AniList: one.IDs[ProviderAniListManga] != ""},
		})
	}
	return candidates, nil
}

// Held implements works.Source.
func (s catalogSource) Held(ctx context.Context, provider, externalID string) (int64, bool) {
	return s.service.WorkByIDs(ctx, map[string]string{provider: externalID})
}

// Monitor implements works.Source: Sonarr's wording.
func (catalogSource) Monitor() (string, string, func(string) bool) {
	return string(MonitorAll), string(MonitorNone), func(option string) bool { return MonitorOption(option).Valid() }
}

// Add implements works.Source.
func (s catalogSource) Add(ctx context.Context, request works.Add) (media.AddResult, error) {
	ids := map[string]string{request.Provider: request.ExternalID}
	var hint Hint
	var fallback *Series
	if request.Fallback != nil {
		for provider, id := range request.Fallback.ExternalIDs {
			if ids[provider] == "" {
				ids[provider] = id
			}
		}
		hint = Hint{Title: request.Fallback.Title, Year: request.Fallback.Year}
		fallback = &Series{Title: request.Fallback.Title, Year: request.Fallback.Year, IDs: ids}
	}
	result, err := s.service.Add(ctx, AddRequest{
		IDs: ids, Hint: hint, CategoryID: request.CategoryID, ProfileID: request.ProfileID, Monitor: MonitorOption(request.Option),
		AddedBy: request.AddedBy, Fallback: fallback,
	})
	if errors.Is(err, ErrNoSource) {
		return media.AddResult{}, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	if err != nil {
		return media.AddResult{}, err
	}
	return media.AddResult{Kind: AddKindSeries, WorkID: result.WorkID, Created: result.Created}, nil
}
