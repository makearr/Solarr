package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// The subjects a heart can be on; work_part is a song or an episode, organisation a studio or a network.
const (
	FavoriteWork         = "work"
	FavoriteWorkPart     = "work_part"
	FavoritePerson       = "person"
	FavoriteOrganisation = "organisation"
	FavoriteCollection   = "collection"
	FavoriteTag          = "tag"
)

// IsFavoriteSubject reports whether a heart can be on this kind of thing.
func IsFavoriteSubject(subjectType string) bool {
	switch subjectType {
	case FavoriteWork, FavoriteWorkPart, FavoritePerson, FavoriteOrganisation, FavoriteCollection, FavoriteTag:
		return true
	}
	return false
}

// Favorites keeps each user's hearts, which are not follows: they acquire nothing.
type Favorites struct {
	store *store.Store
	clock clock.Clock
}

// NewFavorites wraps an opened library database.
func NewFavorites(opened *store.Store, cl clock.Clock) Favorites {
	return Favorites{store: opened, clock: cl}
}

// Set hearts the subject for the user, or takes the heart back; either is idempotent.
func (f Favorites) Set(ctx context.Context, userID int64, subjectType string, subjectID int64, favorite bool) error {
	err := f.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if favorite {
			return queries.AddFavorite(ctx, librarydb.AddFavoriteParams{
				UserID: userID, SubjectType: subjectType, SubjectID: subjectID, CreatedAt: f.clock.Now().Unix(),
			})
		}
		return queries.RemoveFavorite(ctx, librarydb.RemoveFavoriteParams{UserID: userID, SubjectType: subjectType, SubjectID: subjectID})
	})
	if err != nil {
		return fmt.Errorf("library: favorite %s %d: %w", subjectType, subjectID, err)
	}
	return nil
}

// IDs answers every subject of one type the user has hearted, so a list can draw its hearts from one request.
func (f Favorites) IDs(ctx context.Context, userID int64, subjectType string) ([]int64, error) {
	ids, err := librarydb.New(f.store.Read()).ListFavoriteIDs(ctx, librarydb.ListFavoriteIDsParams{UserID: userID, SubjectType: subjectType})
	if err != nil {
		return nil, fmt.Errorf("library: list favorites: %w", err)
	}
	return ids, nil
}

// favoriteSQL is the predicate "the user hearted this row"; column is the row's id, userID the viewer (0 none).
func favoriteSQL(subjectType, column string) string {
	return "EXISTS (SELECT 1 FROM favorite fav WHERE fav.user_id = ? AND fav.subject_type = '" + subjectType + "' AND fav.subject_id = " + column + ")"
}
