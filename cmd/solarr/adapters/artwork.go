package adapters

import (
	"context"
	"errors"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
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
		CategoryIDs: found.CategoryIDs, PrimaryArtistID: found.PrimaryArtistID,
	}, nil
}

func (a Artwork) GetPersonArtwork(ctx context.Context, personID int64) (apiserver.PersonArtworkRow, error) {
	found, err := a.Artworks.GetForPerson(ctx, personID)
	if err != nil {
		return apiserver.PersonArtworkRow{}, err
	}
	return apiserver.PersonArtworkRow{CacheKey: found.CacheKey, Found: found.Found}, nil
}

// ArtworkChoices satisfies apiserver.ArtworkChoices over music.ArtworkChoice.
type ArtworkChoices struct{ Choice *music.ArtworkChoice }

func (a ArtworkChoices) WorkOptions(ctx context.Context, workID int64) ([]apiserver.ImageOptionRow, error) {
	options, err := a.Choice.WorkOptions(ctx, workID)
	return imageOptionRows(options), err
}

func (a ArtworkChoices) PersonOptions(ctx context.Context, personID int64) ([]apiserver.ImageOptionRow, error) {
	options, err := a.Choice.PersonOptions(ctx, personID)
	return imageOptionRows(options), err
}

func (a ArtworkChoices) PickWork(ctx context.Context, workID int64, imageURL string) error {
	return notOffered(a.Choice.PickWork(ctx, workID, imageURL))
}

func (a ArtworkChoices) PickPerson(ctx context.Context, personID int64, imageURL string) error {
	return notOffered(a.Choice.PickPerson(ctx, personID, imageURL))
}

func notOffered(err error) error {
	if errors.Is(err, music.ErrImageNotOffered) {
		return apiserver.ErrImageNotOffered
	}
	return err
}

func imageOptionRows(options []music.ImageOption) []apiserver.ImageOptionRow {
	rows := make([]apiserver.ImageOptionRow, len(options))
	for index, option := range options {
		rows[index] = apiserver.ImageOptionRow{URL: option.URL, ThumbURL: option.ThumbURL, Source: option.Source, Label: option.Label}
	}
	return rows
}
