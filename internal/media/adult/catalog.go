package adult

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parseadult"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Catalog is the adult module's add and refresh: the shared works.Catalog over the providers.
type Catalog struct {
	Service          Service
	Categories       Categories
	DefaultProfileID int64
	// AllowAgain lifts the exclusion of a scene a person removed and now adds on purpose; nil lifts nothing.
	AllowAgain func(ctx context.Context, provider, externalID string) error
}

var _ media.Adding = Catalog{}

func (c Catalog) shared() works.Catalog {
	return works.Catalog{Source: catalogSource{c.Service}, Categories: c.Categories, DefaultProfileID: c.DefaultProfileID, AllowAgain: c.AllowAgain}
}

// LookupWorks implements media.Adding.
func (c Catalog) LookupWorks(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	return c.shared().LookupWorks(ctx, lookup)
}

// AddWork implements media.Adding: the scene with Whisparr's Monitor choice.
func (c Catalog) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	return c.shared().AddWork(ctx, request)
}

// catalogSource is works.Source for scenes.
type catalogSource struct{ service Service }

// Lookup implements works.Source: the providers' scenes for the words typed, a "[Studio] Title" or "Studio - Title"
// narrowing it to a studio.
func (s catalogSource) Lookup(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	query := Query{Title: strings.TrimSpace(lookup.Query)}
	if info, ok := parseadult.ParseName(query.Title); ok && info.Studio != "" {
		query = Query{Title: info.Title, Studio: info.Studio, Date: info.Date}
	}
	found, err := s.service.Search(ctx, lookup.CategoryID, query, lookup.Limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	candidates := make([]media.AddCandidate, 0, len(found))
	for _, scene := range found {
		provider, externalID := CandidateID(scene.IDs)
		if externalID == "" {
			continue
		}
		var subtitle []string
		if scene.Studio != "" {
			subtitle = append(subtitle, scene.Studio)
		}
		if len(scene.Performers) > 0 {
			subtitle = append(subtitle, strings.Join(scene.Performers[:min(3, len(scene.Performers))], ", "))
		}
		candidates = append(candidates, media.AddCandidate{
			Kind: MediaType, Provider: provider, ExternalID: externalID, Title: scene.Title, Subtitle: strings.Join(subtitle, " - "),
			Overview: scene.Overview, PosterURL: scene.Cover, Year: int64(scene.Year), Traits: scene.Traits,
		})
	}
	return candidates, nil
}

// Held implements works.Source.
func (s catalogSource) Held(ctx context.Context, provider, externalID string) (int64, bool) {
	ids, ok := NamedIDs(provider, externalID)
	if !ok {
		return 0, false
	}
	return s.service.WorkByIDs(ctx, ids)
}

// Monitor implements works.Source: Whisparr's two choices.
func (catalogSource) Monitor() (string, string, func(string) bool) {
	return string(MonitorScene), string(MonitorNone), func(option string) bool { return MonitorOption(option).Valid() }
}

// Add implements works.Source.
func (s catalogSource) Add(ctx context.Context, request works.Add) (media.AddResult, error) {
	ids, ok := NamedIDs(request.Provider, request.ExternalID)
	if !ok {
		return media.AddResult{}, fmt.Errorf("%w: %s %q is not a scene id", media.ErrInvalidAdd, request.Provider, request.ExternalID)
	}
	var fallback *Scene
	if request.Fallback != nil {
		fallback = &Scene{IDs: ids, Title: request.Fallback.Title, Year: request.Fallback.Year}
	}
	result, err := s.service.Add(ctx, AddRequest{
		IDs: ids, CategoryID: request.CategoryID, ProfileID: request.ProfileID, Monitor: MonitorOption(request.Option),
		AddedBy: request.AddedBy, Fallback: fallback,
	})
	if errors.Is(err, ErrNoSource) {
		return media.AddResult{}, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	if err != nil {
		return media.AddResult{}, err
	}
	return media.AddResult{Kind: MediaType, WorkID: result.WorkID, Created: result.Created}, nil
}

// Refresh re-reads one scene from the sources; monitoring and the person's own fields stay as they are.
func (c Catalog) Refresh(ctx context.Context, categoryID, workID int64) error {
	_, err := c.Service.Refresh(ctx, categoryID, workID)
	if errors.Is(err, ErrNoSource) {
		return fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	return err
}

// NewFiles is the media.PlacingFiles: the shared one with the scene a person names.
func NewFiles(resolver Resolver, unidentified library.Unidentified, clock clock.Clock, allowAgain func(ctx context.Context, provider, externalID string) error) works.Files {
	return works.Files{
		Series: resolver.Service.Series, Unidentified: unidentified, Clock: clock, AllowAgain: allowAgain,
		Resolve: func(ctx context.Context, categoryID int64, placement media.Placement) (int64, error) {
			ids, ok := NamedIDs(placement.Provider, placement.ExternalID)
			if !ok {
				return 0, fmt.Errorf("%w: %s %q is not a scene id", media.ErrInvalidAdd, placement.Provider, placement.ExternalID)
			}
			return resolver.ByIDs(ctx, categoryID, ids, Hint{}), nil
		},
	}
}
