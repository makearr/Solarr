package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ReadingPosition is where one user stopped reading a file: a page number for a comic, an EPUB location, a PDF page.
type ReadingPosition struct {
	Position  string
	Progress  float64
	UpdatedAt int64
}

// ReadingPositions keeps each user's place in each file.
type ReadingPositions struct{ store *store.Store }

// NewReadingPositions builds it over an open library store.
func NewReadingPositions(opened *store.Store) ReadingPositions {
	return ReadingPositions{store: opened}
}

// Get is the user's place in the file; found is false when they have none.
func (r ReadingPositions) Get(ctx context.Context, userID, mediaObjectID int64) (ReadingPosition, bool, error) {
	row, err := librarydb.New(r.store.Read()).GetReadingPosition(ctx, librarydb.GetReadingPositionParams{UserID: userID, MediaObjectID: mediaObjectID})
	if errors.Is(err, sql.ErrNoRows) {
		return ReadingPosition{}, false, nil
	}
	if err != nil {
		return ReadingPosition{}, false, fmt.Errorf("library: reading position of file %d: %w", mediaObjectID, err)
	}
	return ReadingPosition{Position: row.Position, Progress: row.Progress, UpdatedAt: row.UpdatedAt}, true, nil
}

// Set keeps the user's place in the file, replacing the last.
func (r ReadingPositions) Set(ctx context.Context, userID, mediaObjectID int64, position ReadingPosition) error {
	err := r.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetReadingPosition(ctx, librarydb.SetReadingPositionParams{
			UserID: userID, MediaObjectID: mediaObjectID, Position: position.Position, Progress: position.Progress, UpdatedAt: position.UpdatedAt,
		})
	})
	if err != nil {
		return fmt.Errorf("library: keep reading position of file %d: %w", mediaObjectID, err)
	}
	return nil
}
