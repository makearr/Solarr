package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// GenreTree answers the genre tree's genres the names mean, by name or alias, and every genre above them, lower-cased;
// a name the genre tree does not know is left out. It only reads.
func (p People) GenreTree(ctx context.Context, genreTree string, names []string) (map[string]bool, error) {
	queries := librarydb.New(p.store.Read())
	tree := map[string]bool{}
	for _, name := range names {
		ids, err := queries.ListTagsByAlias(ctx, librarydb.ListTagsByAliasParams{Alias: slugify(name), GenreTree: genreTree})
		if err != nil {
			return nil, fmt.Errorf("library: genre %q: %w", name, err)
		}
		if tag, err := queries.GetTagBySlug(ctx, genreTreeSlug(genreTree, name)); err == nil {
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

// TagsInCategories answers every tag the libraries' works carry, and every tag above one, with how many of those works
// carry it or a tag below it: what a library's tag filter offers.
func (w Works) TagsInCategories(ctx context.Context, categoryIDs []int64) ([]TagNode, error) {
	if len(categoryIDs) == 0 {
		return nil, nil
	}
	marks, arguments := idList(categoryIDs)
	rows, err := w.store.Read().QueryContext(ctx,
		"SELECT t.id, t.name, COALESCE(t.parent_id, 0), t.genre_tree, COUNT(DISTINCT a.subject_id)"+
			" FROM tag_assignment a JOIN tag_closure c ON c.descendant_id = a.tag_id JOIN tag t ON t.id = c.ancestor_id"+
			" WHERE a.subject_type = 'work' AND a.subject_id IN (SELECT d.work_id FROM desired_item d WHERE d.category_id IN ("+marks+"))"+
			" GROUP BY t.id ORDER BY t.name COLLATE NOCASE, t.id", arguments...)
	if err != nil {
		return nil, fmt.Errorf("library: tags of libraries %v: %w", categoryIDs, err)
	}
	defer func() { _ = rows.Close() }()
	var tags []TagNode
	for rows.Next() {
		var tag TagNode
		if err := rows.Scan(&tag.ID, &tag.Name, &tag.ParentID, &tag.GenreTree, &tag.Works); err != nil {
			return nil, fmt.Errorf("library: tags of libraries: %w", err)
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}
