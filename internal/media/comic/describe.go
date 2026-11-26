package comic

import (
	"context"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
)

var _ works.Describer = catalogSource{}

// Describe implements works.Describer: the series as the providers merge it, nothing stored.
func (s catalogSource) Describe(ctx context.Context, request media.DescribeRequest) (media.WorkPreview, error) {
	found, err := s.service.Fetch(ctx, request.CategoryID, map[string]string{request.Provider: request.ExternalID}, Hint{})
	if errors.Is(err, ErrNoSource) {
		return media.WorkPreview{}, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	if err != nil {
		return media.WorkPreview{}, err
	}
	return PreviewOf(found), nil
}

// PreviewOf is a merged series as a preview, its parts in one group.
func PreviewOf(series Series) media.WorkPreview {
	preview := media.WorkPreview{
		Title: series.Title, Subtitle: series.Publisher, Overview: series.Overview, PosterURL: series.Cover, Status: series.Status,
		Year: int64(series.Year), Genres: series.Genres,
	}
	addFact := func(key, value string) {
		if value != "" {
			preview.Facts = append(preview.Facts, media.PreviewFact{Key: key, Value: value})
		}
	}
	addFact(media.FactPublisher, series.Publisher)
	addFact(media.FactLanguage, series.OriginalLanguage)
	for _, credit := range series.Credits {
		preview.People = append(preview.People, media.PreviewPerson{Name: credit.Name, Role: credit.Role})
	}
	if len(series.Parts) > 0 {
		group := media.PreviewGroup{Number: 1}
		for _, part := range series.Parts {
			group.Parts = append(group.Parts, media.PreviewPart{Number: part.Number, Title: part.Title, Date: part.CoverDate})
		}
		preview.Groups = []media.PreviewGroup{group}
	}
	return preview
}

// DescribeWork implements media.Describing.
func (c Catalog) DescribeWork(ctx context.Context, request media.DescribeRequest) (media.WorkPreview, error) {
	return c.shared().DescribeWork(ctx, request)
}
