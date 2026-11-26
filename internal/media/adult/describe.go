package adult

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
)

var _ works.Describer = catalogSource{}

// Describe implements works.Describer: the scene as the sources merge it, nothing stored.
func (s catalogSource) Describe(ctx context.Context, request media.DescribeRequest) (media.WorkPreview, error) {
	ids, ok := NamedIDs(request.Provider, request.ExternalID)
	if !ok {
		return media.WorkPreview{}, fmt.Errorf("%w: %s %q is not a scene id", media.ErrInvalidAdd, request.Provider, request.ExternalID)
	}
	found, err := s.service.FetchFor(ctx, request.CategoryID, ids, Hint{})
	if errors.Is(err, ErrNoSource) {
		return media.WorkPreview{}, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	if err != nil {
		return media.WorkPreview{}, err
	}
	return PreviewOf(found), nil
}

// PreviewOf is a merged scene as a preview.
func PreviewOf(scene Scene) media.WorkPreview {
	preview := media.WorkPreview{Title: scene.Title, Subtitle: scene.Studio, Overview: scene.Overview, Year: int64(scene.Year), Genres: scene.Tags}
	for _, image := range scene.Images {
		if image.Kind == "poster" {
			preview.PosterURL = image.URL
			break
		}
	}
	addFact := func(key, value string) {
		if value != "" {
			preview.Facts = append(preview.Facts, media.PreviewFact{Key: key, Value: value})
		}
	}
	addFact(media.FactStudio, scene.Studio)
	addFact(media.FactNetwork, scene.Network)
	addFact(media.FactReleased, scene.Date)
	if scene.DurationSec > 0 {
		addFact(media.FactRuntimeMin, strconv.Itoa((scene.DurationSec+30)/60))
	}
	for _, credit := range scene.Credits {
		preview.People = append(preview.People, media.PreviewPerson{Name: credit.Name, Role: credit.Role, ImageURL: credit.Image})
	}
	return preview
}

// DescribeWork implements media.Describing.
func (c Catalog) DescribeWork(ctx context.Context, request media.DescribeRequest) (media.WorkPreview, error) {
	return c.shared().DescribeWork(ctx, request)
}
