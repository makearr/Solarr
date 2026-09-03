package apiserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/makearr/MediaNexus/internal/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/authz"
)

// ErrQueryTooShort reports a search query too short to run (section 21.5's 2-character
// floor). A sentinel here rather than library.ErrQueryTooShort: this package never
// imports the library store (WorksReader's doc comment explains why), so the adapter
// that implements SearchReader translates the domain error into this one.
var ErrQueryTooShort = errors.New("apiserver: search query is too short")

// searchDefaultLimit and searchMaxLimit bound an interactive search box, deliberately
// smaller than paginate.DefaultLimit/MaxLimit (50/200), which are sized for browsing a
// full list rather than a live-typing dropdown.
const (
	searchDefaultLimit = 20
	searchMaxLimit     = 50
)

// SearchReader ranks the library by title. Defined here and implemented by the caller,
// so this package never imports the library store (WorksReader's doc comment explains
// the same choice).
type SearchReader interface {
	SearchWorks(ctx context.Context, filter SearchFilter, limit int) ([]SearchResultRow, error)
}

// SearchFilter narrows a SearchWorks call. RootFolderIDs carries ADR-27 visibility the
// same way WorksFilter.RootFolderIDs does: nil is unfiltered, a non-nil empty slice
// means the caller can see no root folder at all and must match nothing.
type SearchFilter struct {
	Query         string
	MediaType     string
	RootFolderIDs []int64
}

// SearchResultRow is one work a query matched, ranked best (lowest score) first.
type SearchResultRow struct {
	Work  WorkRow
	Score float64
}

// clampSearchLimit keeps a requested search page size within the interactive-box bounds.
func clampSearchLimit(requested int) int {
	switch {
	case requested <= 0:
		return searchDefaultLimit
	case requested > searchMaxLimit:
		return searchMaxLimit
	default:
		return requested
	}
}

// Search answers a ranked, category-filtered title search (section 21.5, ADR-27). A
// capped array, not a cursor-paginated page: SearchReader.SearchWorks re-runs the whole
// ranking on every call, so a stable cursor over it would not mean anything.
func (s *Server) Search(w http.ResponseWriter, r *http.Request, params gen.SearchParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	if s.search == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "search is not available")
		return
	}

	rootFolderIDs, err := s.visibleRootFolders(access, nil)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	limit := clampSearchLimit(derefOr(params.Limit, 0))
	rows, err := s.search.SearchWorks(r.Context(), SearchFilter{
		Query: params.Q, MediaType: derefOr(params.MediaType, ""), RootFolderIDs: rootFolderIDs,
	}, limit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	writeJSON(w, s.logger, http.StatusOK, buildSearchResults(rows))
}

// buildSearchResults never returns nil, so a query matching nothing encodes as "[]"
// rather than "null".
func buildSearchResults(rows []SearchResultRow) []gen.SearchResult {
	results := make([]gen.SearchResult, len(rows))
	for index, row := range rows {
		results[index] = gen.SearchResult{
			Work: gen.Work{
				Id: row.Work.ID, MediaType: row.Work.MediaType, Title: row.Work.Title,
				SortTitle: row.Work.SortTitle, Year: yearOf(row.Work.Year),
				AddedAt: row.Work.AddedAt, UpdatedAt: row.Work.UpdatedAt,
			},
			Score: float32(row.Score),
		}
	}
	return results
}
