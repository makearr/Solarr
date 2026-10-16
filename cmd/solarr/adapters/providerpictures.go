package adapters

import (
	"context"
	"encoding/json"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ProviderPictures is the picture choice of every work whose pictures a provider lists in its "images" field (TV
// series, movies): the posters on offer, and the pick kept over later refreshes as the person's own.
type ProviderPictures struct {
	Reader library.SeriesReader
	Series library.Series
	Clock  clock.Clock
}

type storedImage struct {
	Kind   string `json:"kind"`
	URL    string `json:"url"`
	Source string `json:"source,omitempty"`
}

// WorkPictures lists the posters the providers hold, the thumbnail being the poster itself.
func (p ProviderPictures) WorkPictures(ctx context.Context, workID int64) ([]media.ImageOption, error) {
	images, err := p.Reader.Images(ctx, workID)
	if err != nil {
		return nil, err
	}
	var options []media.ImageOption
	for _, image := range images {
		if image.Kind == "poster" {
			options = append(options, media.ImageOption{URL: image.URL, ThumbURL: image.URL, Source: image.Source, Label: "Poster"})
		}
	}
	return options, nil
}

// PickWorkPicture puts the chosen poster first among the work's pictures as the user's own, keeping the others.
func (p ProviderPictures) PickWorkPicture(ctx context.Context, workID int64, url string) error {
	images, err := p.Reader.Images(ctx, workID)
	if err != nil {
		return err
	}
	chosen := []storedImage{}
	rest := []storedImage{}
	for _, image := range images {
		entry := storedImage{Kind: image.Kind, URL: image.URL, Source: image.Source}
		if image.Kind == "poster" && image.URL == url {
			chosen = append(chosen, entry)
		} else {
			rest = append(rest, entry)
		}
	}
	if len(chosen) == 0 {
		return media.ErrImageNotOffered
	}
	value, err := json.Marshal(append(chosen, rest...))
	if err != nil {
		return err
	}
	return p.Series.SetManualField(ctx, p.Clock.Now(), workID, "images", string(value))
}
