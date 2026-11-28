package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// ReadingPlaces keeps each user's place in each file for the reader.
type ReadingPlaces struct{ Store library.ReadingPositions }

var _ apiserver.ReadingPlaces = ReadingPlaces{}

// Get implements apiserver.ReadingPlaces.
func (r ReadingPlaces) Get(ctx context.Context, userID, mediaObjectID int64) (apiserver.ReadingPlace, bool, error) {
	place, found, err := r.Store.Get(ctx, userID, mediaObjectID)
	return apiserver.ReadingPlace{Position: place.Position, Progress: place.Progress, UpdatedAt: place.UpdatedAt}, found, err
}

// Set implements apiserver.ReadingPlaces.
func (r ReadingPlaces) Set(ctx context.Context, userID, mediaObjectID int64, place apiserver.ReadingPlace) error {
	return r.Store.Set(ctx, userID, mediaObjectID, library.ReadingPosition{Position: place.Position, Progress: place.Progress, UpdatedAt: place.UpdatedAt})
}
