package movie

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
)

var _ works.Describer = catalogSource{}

// previewCastLimit caps the cast a preview lists; the library item page holds the whole of it.
const previewCastLimit = 24

// Describe implements works.Describer: the film as the sources merge it, nothing stored.
func (s catalogSource) Describe(ctx context.Context, request media.DescribeRequest) (media.WorkPreview, error) {
	var ids IDs
	switch request.Provider {
	case ProviderTMDB:
		id, err := strconv.ParseInt(request.ExternalID, 10, 64)
		if err != nil || id <= 0 {
			return media.WorkPreview{}, fmt.Errorf("%w: %q is not a TMDB id", media.ErrInvalidAdd, request.ExternalID)
		}
		ids.TMDB = id
	case ProviderIMDb:
		ids.IMDb = request.ExternalID
	default:
		return media.WorkPreview{}, fmt.Errorf("%w: only a TMDB or IMDb id can be described", media.ErrInvalidAdd)
	}
	film, err := s.catalog.Service.FetchFor(ctx, request.CategoryID, ids)
	if errors.Is(err, ErrNoSource) {
		return media.WorkPreview{}, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	if err != nil {
		return media.WorkPreview{}, err
	}
	return PreviewOf(film), nil
}

// PreviewOf is a merged film as a preview.
func PreviewOf(film Movie) media.WorkPreview {
	preview := media.WorkPreview{Title: film.Title, Subtitle: film.Studio, Tagline: film.Tagline, Overview: film.Overview, Year: int64(film.Year), Genres: film.Genres}
	for _, image := range film.Images {
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
	addFact(media.FactStudio, film.Studio)
	if film.RuntimeMin > 0 {
		addFact(media.FactRuntimeMin, strconv.Itoa(film.RuntimeMin))
	}
	addFact(media.FactCertification, film.Certification)
	addFact(media.FactLanguage, film.OriginalLanguage)
	for _, day := range []string{film.Digital, film.Physical, film.InCinemas, film.Premiere} {
		if day != "" {
			addFact(media.FactReleased, day)
			break
		}
	}
	if film.Collection != nil {
		addFact(media.FactCollection, film.Collection.Name)
	}
	addFact(media.FactWebsite, film.Website)
	for _, rating := range film.Ratings {
		preview.Ratings = append(preview.Ratings, media.PreviewRating{Provider: rating.Provider, Score: rating.Score, Votes: rating.Votes})
	}
	cast := 0
	for _, credit := range film.Credits {
		switch credit.Role {
		case RoleActor:
			if cast >= previewCastLimit {
				continue
			}
			cast++
		case RoleDirector, RoleWriter:
		default:
			continue
		}
		preview.People = append(preview.People, media.PreviewPerson{Name: credit.Name, Role: credit.Role, Character: credit.Character, ImageURL: credit.Image})
	}
	return preview
}

// DescribeWork implements media.Describing.
func (c Catalog) DescribeWork(ctx context.Context, request media.DescribeRequest) (media.WorkPreview, error) {
	return c.shared().DescribeWork(ctx, request)
}
