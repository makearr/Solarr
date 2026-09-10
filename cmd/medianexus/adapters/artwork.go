package adapters

import (
	"context"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/library"
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
	}, nil
}
