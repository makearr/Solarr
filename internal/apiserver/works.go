package apiserver

import (
	"context"
	"net/http"
	"time"

	"github.com/makearr/MediaNexus/internal/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/authz"
	"github.com/makearr/MediaNexus/internal/paginate"
	"github.com/makearr/MediaNexus/internal/settings"
)

// WorksReader lists the library. Defined here and implemented by the caller, so
// this package never imports the library store (LibrarySize's own doc comment
// explains the same choice).
type WorksReader interface {
	ListWorks(ctx context.Context, filter WorksFilter, cursor WorksCursor, pageSize int) ([]WorkRow, error)
}

// WorksFilter narrows a ListWorks call. RootFolderIDs is nil for unfiltered, a
// non-nil empty slice for "the caller has no visible root folder at all" --
// library.WorkFilter's own nil-vs-empty distinction, carried through here since
// getting this wrong here would mean a category filter that quietly does nothing.
type WorksFilter struct {
	RootFolderIDs []int64
}

// WorksCursor is a position in the list.
type WorksCursor struct {
	SortTitle string
	ID        int64
}

// WorkRow is one work as the library reports it.
type WorkRow struct {
	ID        int64
	MediaType string
	Title     string
	SortTitle string
	Year      *int64
	AddedAt   time.Time
	UpdatedAt time.Time
}

// ListWorks returns a page of the library the caller may see (ADR-27). A work
// under no root folder the caller can read is excluded, never 403 (Invariant 37
// applied to a listing rather than a single item).
func (s *Server) ListWorks(w http.ResponseWriter, r *http.Request, params gen.ListWorksParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	cursor, err := paginate.DecodeText(derefOr(params.Cursor, ""))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}
	limit := paginate.ClampLimit(derefOr(params.Limit, 0))

	if s.works == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the library is not available")
		return
	}

	rootFolderIDs, err := s.visibleRootFolders(access, params.CategoryId)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	rows, err := s.works.ListWorks(r.Context(), WorksFilter{RootFolderIDs: rootFolderIDs},
		WorksCursor{SortTitle: cursor.Sort, ID: cursor.ID}, limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, buildWorkPage(rows, limit))
}

// visibleRootFolders resolves what a caller may see into root folder ids,
// scoped to categoryID when given. A category the caller cannot read answers
// authz.ErrNotVisible, which writeError already maps to 404 (Invariant 37).
func (s *Server) visibleRootFolders(access authz.Access, categoryID *int64) ([]int64, error) {
	var categoryIDs []int64
	if categoryID != nil {
		if _, err := access.Category(*categoryID, settings.PermissionRead); err != nil {
			return nil, err
		}
		categoryIDs = []int64{*categoryID}
	} else {
		categoryIDs = access.VisibleCategoryIDs(settings.PermissionRead)
	}

	visible := make(map[int64]struct{}, len(categoryIDs))
	for _, id := range categoryIDs {
		visible[id] = struct{}{}
	}

	// Non-nil from here on, even when it stays empty: WorksFilter.RootFolderIDs
	// treats nil as unfiltered, which would leak every category's works the
	// instant a caller has none visible.
	rootFolderIDs := make([]int64, 0)
	for _, root := range s.auth.Settings.Snapshot().CategoryRoots() {
		if _, ok := visible[root.CategoryID]; ok {
			rootFolderIDs = append(rootFolderIDs, root.RootFolderID)
		}
	}
	return rootFolderIDs, nil
}

// buildWorkPage trims the probe row (one more than pageSize) and, if present,
// encodes the cursor from it -- paginate.Build's shape, reimplemented here since
// Build is fixed to the integer-sorted Cursor and works sort on text.
func buildWorkPage(rows []WorkRow, limit int) gen.WorkPage {
	items := rows
	var nextCursor string
	if len(rows) > limit {
		items = rows[:limit]
		last := items[len(items)-1]
		nextCursor = paginate.TextCursor{Sort: last.SortTitle, ID: last.ID}.Encode()
	}

	views := make([]gen.Work, len(items))
	for index, row := range items {
		views[index] = gen.Work{
			Id: row.ID, MediaType: row.MediaType, Title: row.Title, SortTitle: row.SortTitle,
			Year: yearOf(row.Year), AddedAt: row.AddedAt, UpdatedAt: row.UpdatedAt,
		}
	}

	page := gen.WorkPage{Items: views}
	if nextCursor != "" {
		page.NextCursor = &nextCursor
	}
	return page
}

func yearOf(year *int64) *int {
	if year == nil {
		return nil
	}
	value := int(*year)
	return &value
}
