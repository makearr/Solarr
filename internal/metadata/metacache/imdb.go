package metacache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/metadatadb"
)

// ImdbRating is IMDb's average rating (0..10) and the votes behind it for one title.
type ImdbRating struct {
	Rating float64
	Votes  int64
}

// SetImdbRatings stores a chunk of the dataset in one short transaction; keep a chunk to a few hundred rows (ADR-4).
func (c Cache) SetImdbRatings(ctx context.Context, ratings map[string]ImdbRating) error {
	return c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := metadatadb.New(tx)
		for title, rating := range ratings {
			if err := queries.UpsertImdbRating(ctx, metadatadb.UpsertImdbRatingParams{Tconst: title, Rating: rating.Rating, Votes: rating.Votes}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ImdbRatingOf answers the stored rating of a title (tt id), or ErrNotFound.
func (c Cache) ImdbRatingOf(ctx context.Context, title string) (ImdbRating, error) {
	row, err := metadatadb.New(c.store.Read()).GetImdbRating(ctx, title)
	if errors.Is(err, sql.ErrNoRows) {
		return ImdbRating{}, ErrNotFound
	}
	if err != nil {
		return ImdbRating{}, fmt.Errorf("metacache: imdb rating %s: %w", title, err)
	}
	return ImdbRating{Rating: row.Rating, Votes: row.Votes}, nil
}

// ImdbRatingCount says how many titles the dataset holds; zero means it was never downloaded.
func (c Cache) ImdbRatingCount(ctx context.Context) (int64, error) {
	count, err := metadatadb.New(c.store.Read()).CountImdbRatings(ctx)
	if err != nil {
		return 0, fmt.Errorf("metacache: count imdb ratings: %w", err)
	}
	return count, nil
}
