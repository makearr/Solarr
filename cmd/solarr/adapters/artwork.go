package adapters

import (
	"context"
	"errors"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
)

// Artwork satisfies apiserver.ArtworkReader over library.Artworks.
type Artwork struct{ Artworks library.Artworks }

func (a Artwork) GetWorkArtwork(ctx context.Context, workID int64) (apiserver.WorkArtworkRow, error) {
	found, err := a.Artworks.GetForWork(ctx, workID)
	if err != nil {
		return apiserver.WorkArtworkRow{}, err
	}
	return apiserver.WorkArtworkRow{
		CacheKey: found.CacheKey, Found: found.Found, RootFolderIDs: found.RootFolderIDs,
		CategoryIDs: found.CategoryIDs, LeadPersonID: found.LeadPersonID,
	}, nil
}

func (a Artwork) GetPersonArtwork(ctx context.Context, personID int64) (apiserver.PersonArtworkRow, error) {
	found, err := a.Artworks.GetForPerson(ctx, personID)
	if err != nil {
		return apiserver.PersonArtworkRow{}, err
	}
	return apiserver.PersonArtworkRow{CacheKey: found.CacheKey, Found: found.Found}, nil
}

// ArtworkChoices satisfies apiserver.ArtworkChoices: a work's pictures through its media type's Picturing, a person's
// through music's choice, the one type with pictures of people to choose from.
type ArtworkChoices struct {
	Works     library.Works
	Picturing func(mediaType string) (media.Picturing, bool)
	People    *music.ArtworkChoice
}

func (a ArtworkChoices) picturing(ctx context.Context, workID int64) (media.Picturing, error) {
	mediaType, err := a.Works.MediaTypeOf(ctx, workID)
	if err != nil {
		return nil, err
	}
	picturing, ok := a.Picturing(mediaType)
	if !ok {
		return nil, apiserver.ErrNotBuiltForMediaType
	}
	return picturing, nil
}

func (a ArtworkChoices) WorkOptions(ctx context.Context, workID int64) ([]apiserver.ImageOptionRow, error) {
	picturing, err := a.picturing(ctx, workID)
	if err != nil {
		return nil, err
	}
	options, err := picturing.WorkPictures(ctx, workID)
	return imageOptionRows(options), notOffered(err)
}

func (a ArtworkChoices) PersonOptions(ctx context.Context, personID int64) ([]apiserver.ImageOptionRow, error) {
	options, err := a.People.PersonOptions(ctx, personID)
	return imageOptionRows(options), err
}

func (a ArtworkChoices) PickWork(ctx context.Context, workID int64, imageURL string) error {
	picturing, err := a.picturing(ctx, workID)
	if err != nil {
		return err
	}
	return notOffered(picturing.PickWorkPicture(ctx, workID, imageURL))
}

func (a ArtworkChoices) PickPerson(ctx context.Context, personID int64, imageURL string) error {
	return notOffered(a.People.PickPerson(ctx, personID, imageURL))
}

// notOffered names the two refusals the server answers itself; anything else is the providers failing.
func notOffered(err error) error {
	switch {
	case errors.Is(err, media.ErrImageNotOffered):
		return apiserver.ErrImageNotOffered
	case errors.Is(err, media.ErrNotWired):
		return apiserver.ErrNotBuiltForMediaType
	}
	return err
}

func imageOptionRows(options []media.ImageOption) []apiserver.ImageOptionRow {
	rows := make([]apiserver.ImageOptionRow, len(options))
	for index, option := range options {
		rows[index] = apiserver.ImageOptionRow{URL: option.URL, ThumbURL: option.ThumbURL, Source: option.Source, Label: option.Label}
	}
	return rows
}
