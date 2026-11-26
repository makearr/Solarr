package book

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
)

var _ works.Describer = catalogSource{}

// Describe implements works.Describer: the book as the sources merge it, nothing stored.
func (s catalogSource) Describe(ctx context.Context, request media.DescribeRequest) (media.WorkPreview, error) {
	ids, ok := s.service.Format.NamedIDs(request.Provider, request.ExternalID)
	if !ok {
		return media.WorkPreview{}, fmt.Errorf("%w: %s %q is not a book id", media.ErrInvalidAdd, request.Provider, request.ExternalID)
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

// PreviewOf is a merged book as a preview.
func PreviewOf(book Book) media.WorkPreview {
	preview := media.WorkPreview{Title: book.Title, Overview: book.Overview, Year: int64(book.Year), Genres: book.Genres}
	if authors := book.Authors(); len(authors) > 0 {
		preview.Subtitle = authors[0]
	}
	for _, image := range book.Images {
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
	addFact(media.FactPublisher, book.Publisher)
	addFact(media.FactLanguage, book.Language)
	addFact(media.FactReleased, book.Published)
	if book.RuntimeMin > 0 {
		addFact(media.FactRuntimeMin, strconv.Itoa(book.RuntimeMin))
	}
	if book.Series != nil {
		addFact(media.FactCollection, book.Series.Name)
	}
	for _, rating := range book.Ratings {
		preview.Ratings = append(preview.Ratings, media.PreviewRating{Provider: rating.Provider, Score: rating.Score, Votes: rating.Votes})
	}
	for _, credit := range book.Credits {
		preview.People = append(preview.People, media.PreviewPerson{Name: credit.Name, Role: credit.Role, ImageURL: credit.Image})
	}
	return preview
}

// DescribeWork implements media.Describing.
func (c Catalog) DescribeWork(ctx context.Context, request media.DescribeRequest) (media.WorkPreview, error) {
	return c.shared().DescribeWork(ctx, request)
}
