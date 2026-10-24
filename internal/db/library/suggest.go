package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// GenreTree answers the taxonomy's genres the names mean, by name or alias, and every genre above them, lower-cased;
// a name the taxonomy does not know is left out. It only reads.
func (p People) GenreTree(ctx context.Context, taxonomy string, names []string) (map[string]bool, error) {
	queries := librarydb.New(p.store.Read())
	tree := map[string]bool{}
	for _, name := range names {
		ids, err := queries.ListTagsByAlias(ctx, librarydb.ListTagsByAliasParams{Alias: slugify(name), Taxonomy: taxonomy})
		if err != nil {
			return nil, fmt.Errorf("library: genre %q: %w", name, err)
		}
		if tag, err := queries.GetTagBySlug(ctx, taxonomySlug(taxonomy, name)); err == nil {
			ids = append(ids, tag.ID)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("library: genre %q: %w", name, err)
		}
		for _, id := range ids {
			above, err := queries.ListTagAncestorNames(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("library: genres above %q: %w", name, err)
			}
			for _, genre := range above {
				tree[strings.ToLower(genre)] = true
			}
		}
	}
	return tree, nil
}

// WorkTraits are the facts a library's rules read of a work it holds.
type WorkTraits struct {
	WorkID           int64
	Title, MediaType string
	Language         string
	Country          string
	AniList          bool
	RatingAge        int64
	// Genres are its genres and every genre above them, lower-cased.
	Genres map[string]bool
}

// TraitsInCategory answers the facts of every work a library holds, by title.
func (w Works) TraitsInCategory(ctx context.Context, categoryID int64) ([]WorkTraits, error) {
	queries := librarydb.New(w.store.Read())
	rows, err := queries.ListWorkTraitsInCategory(ctx, categoryID)
	if err != nil {
		return nil, fmt.Errorf("library: traits of library %d: %w", categoryID, err)
	}
	genres, err := queries.ListWorkGenreTreesInCategory(ctx, categoryID)
	if err != nil {
		return nil, fmt.Errorf("library: genres of library %d: %w", categoryID, err)
	}
	byWork := map[int64]map[string]bool{}
	for _, genre := range genres {
		if byWork[genre.WorkID] == nil {
			byWork[genre.WorkID] = map[string]bool{}
		}
		byWork[genre.WorkID][strings.ToLower(genre.Name)] = true
	}
	traits := make([]WorkTraits, len(rows))
	for i, row := range rows {
		traits[i] = WorkTraits{
			WorkID: row.ID, Title: row.CanonicalTitle, MediaType: row.MediaType, Language: row.Language, Country: row.Country,
			AniList: row.Anilist == 1, RatingAge: row.RatingAge, Genres: byWork[row.ID],
		}
	}
	return traits, nil
}
