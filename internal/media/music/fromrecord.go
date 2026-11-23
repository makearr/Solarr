package music

import (
	"context"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/specprovider"
)

// ConsumesRecords implements media.RecordConsuming.
func (Module) ConsumesRecords() bool { return true }

var _ media.RecordConsuming = Module{}

// RecordProvider is a definition-file provider as music's enrichment asks it.
type RecordProvider interface {
	ByID(ctx context.Context, namespace, id string) (specprovider.Record, error)
}

// ArtistFromRecord reads an artist's photo and biography out of a definition's record; music takes nothing else from one.
func ArtistFromRecord(record specprovider.Record) (photoURL, biography string) {
	for _, image := range record.Images {
		if image.URL != "" && (image.Kind == "" || image.Kind == "photo" || image.Kind == "portrait" || image.Kind == "poster") {
			photoURL = image.URL
			break
		}
	}
	return photoURL, record.Overview
}

// DefinitionArtistArt asks each enrichment definition for the photo and biography the earlier source left empty, by MusicBrainz artist id.
func DefinitionArtistArt(next ArtistArt, providers func() []RecordProvider) ArtistArt {
	return func(ctx context.Context, artistMBID string) (string, string, error) {
		photo, biography, err := "", "", error(nil)
		if next != nil {
			photo, biography, err = next(ctx, artistMBID)
		}
		if err != nil || (photo != "" && biography != "") {
			return photo, biography, err
		}
		for _, provider := range providers() {
			record, found := provider.ByID(ctx, "musicbrainz", artistMBID)
			if found != nil {
				continue
			}
			morePhoto, moreBiography := ArtistFromRecord(record)
			if photo == "" {
				photo = morePhoto
			}
			if biography == "" {
				biography = moreBiography
			}
			if photo != "" && biography != "" {
				break
			}
		}
		return photo, biography, nil
	}
}
