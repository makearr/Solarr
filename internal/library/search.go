package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// ErrQueryTooShort means the caller should not have searched at all, not that the
// search found nothing (section 21.5: minimum 2 characters, paired with FTS5 there
// because a shorter MATCH is also the FTS5 syntax error this guards against).
var ErrQueryTooShort = errors.New("library: search query is too short")

// Search reads the title_search FTS5 index (migration 0010).
type Search struct {
	store *store.Store
}

// NewSearch builds a reader over an open library store.
func NewSearch(opened *store.Store) Search { return Search{store: opened} }

// SearchResult is one work a query matched, ranked best (lowest score) first.
type SearchResult struct {
	Work  librarydb.Work
	Score float64
}

// Works ranks works whose title or an alias matches query, cross-media-type unless
// mediaType narrows it (section 21.1's "search across everything"). A work appears at
// most once even when several of its titles match. rootFolderIDs and categoryIDs mirror
// WorkFilter's own two visibility halves: nil means that half is not applied, a non-nil
// empty slice matches nothing via that half, and a work matching either is included
// (ADR-27 -- a subscription-discovered work has no root folder yet but does have a
// desired_item.category_id).
func (s Search) Works(
	ctx context.Context, query, mediaType string, rootFolderIDs, categoryIDs []int64, pageSize int,
) ([]SearchResult, error) {
	matchQuery, ok := buildMatchQuery(query)
	if !ok {
		return nil, ErrQueryTooShort
	}

	filterRootFolders := rootFolderIDs != nil
	encodedRootFolderIDs, err := json.Marshal(rootFolderIDs)
	if err != nil {
		return nil, fmt.Errorf("library: encode root folder ids: %w", err)
	}
	filterCategories := categoryIDs != nil
	encodedCategoryIDs, err := json.Marshal(categoryIDs)
	if err != nil {
		return nil, fmt.Errorf("library: encode category ids: %w", err)
	}

	rows, err := librarydb.New(s.store.Read()).SearchWorks(ctx, librarydb.SearchWorksParams{
		Query: matchQuery, MediaType: mediaType, PageSize: int64(pageSize),
		FilterRootFolders: boolToInt(filterRootFolders), RootFolderIds: string(encodedRootFolderIDs),
		FilterCategories: boolToInt(filterCategories), CategoryIds: string(encodedCategoryIDs),
	})
	if err != nil {
		return nil, fmt.Errorf("library: search works: %w", err)
	}

	results := make([]SearchResult, len(rows))
	for index, row := range rows {
		results[index] = SearchResult{
			Work: librarydb.Work{
				ID:               row.ID,
				MediaType:        row.MediaType,
				CanonicalTitle:   row.CanonicalTitle,
				SortTitle:        row.SortTitle,
				OriginalLanguage: row.OriginalLanguage,
				Year:             row.Year,
				RuntimeMs:        row.RuntimeMs,
				AddedAt:          row.AddedAt,
				UpdatedAt:        row.UpdatedAt,
				AddedBy:          row.AddedBy,
			},
			Score: row.Score,
		}
	}
	return results, nil
}

// buildMatchQuery turns raw user input into a safe FTS5 MATCH expression. The whole
// input becomes one quoted phrase, so none of FTS5's own query syntax -- AND/OR/NOT,
// "-token" negation, column filters -- reaches a title that happens to contain those
// characters; only a literal double quote needs escaping. The trailing "*" makes the
// last, possibly still-being-typed, word a prefix match, matching the incremental
// search the 250ms debounce implies. ok is false for input below FTS5's own workable
// minimum, which an empty MATCH would otherwise fail on as a syntax error rather than
// answer with no results.
func buildMatchQuery(raw string) (query string, ok bool) {
	trimmed := strings.TrimSpace(raw)
	if utf8.RuneCountInString(trimmed) < 2 {
		return "", false
	}
	escaped := strings.ReplaceAll(trimmed, `"`, `""`)
	return `"` + escaped + `"*`, true
}

func boolToInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
