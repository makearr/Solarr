package apiserver

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// WorksReader lists the library. Defined here and implemented by the caller, so
// this package never imports the library store (LibrarySize's own doc comment
// explains the same choice).
type WorksReader interface {
	ListWorks(ctx context.Context, filter WorksFilter, cursor WorksCursor, pageSize int) ([]WorkRow, error)
}

// WorkSort selects which column ListWorks orders by. The zero value is title.
type WorkSort string

// The columns ListWorks can order by.
const (
	WorkSortTitle WorkSort = ""
	WorkSortYear  WorkSort = "year"
	WorkSortAdded WorkSort = "added"
	// WorkSortArtist orders by primary artist, then title.
	WorkSortArtist WorkSort = "artist"
	// WorkSortSearched orders by when the album was last searched.
	WorkSortSearched WorkSort = "searched"
	// WorkSortImported orders by when the newest file arrived; works with no file are left out.
	WorkSortImported WorkSort = "imported"
)

// SortDirection is which way a WorkSort orders. The zero value is ascending.
type SortDirection string

// The directions a WorkSort can order in.
const (
	SortAscending  SortDirection = ""
	SortDescending SortDirection = "desc"
)

// WorksFilter narrows a ListWorks call. RootFolderIDs and CategoryIDs are
// ADR-27 visibility's two halves -- on-disk presence and desired-item
// membership, library.WorkFilter's own nil-vs-empty distinction carried
// through here since getting this wrong here would mean a category filter
// that quietly does nothing or quietly leaks.
type WorksFilter struct {
	RootFolderIDs []int64
	CategoryIDs   []int64
	Sort          WorkSort
	Direction     SortDirection
	// Fresh is ADR-55's release-date window alone (todo 4.20) -- the
	// adapter resolves it against the live clock, never this package
	// (forbidigo: no time.Now() here either).
	Fresh bool
	// Wanted restricts to works with a monitored, not-yet-COMPLETE
	// desired_item -- the per-library Wanted screen, 2026-09-11.
	Wanted bool
	// CutoffUnmet restricts to albums whose files fall short of the library's cutoff (M1d).
	CutoffUnmet bool
}

// WorksCursor is a position in the list. Only the field matching the active
// WorkSort is read; the others are ignored (library.WorkCursor's own convention).
type WorksCursor struct {
	SortTitle    string
	SortYear     int64
	SortAdded    int64
	SortArtist   string
	SortSearched int64
	SortImported int64
	ID           int64
}

// WorkRow is one work as the library reports it.
type WorkRow struct {
	ID        int64
	MediaType string
	Title     string
	SortTitle string
	Year      *int64
	// Artist is the work's own primary artist credit, when it has one.
	Artist *string
	// ArtistSortName is that artist's sort name, empty with none; the artist
	// order's next-page cursor carries it.
	ArtistSortName string
	AddedAt        time.Time
	UpdatedAt      time.Time
	// WantedState is the state of the desired_item ListWorks joined against
	// when the row has a monitored one -- nil when it has none.
	WantedState *string
	// PresentCount, RequiredCount, AvailableFrom and Downloading feed the
	// availability bar under a cover, the same four facts PersonWorkRow
	// carries on the artist page.
	PresentCount  *int64
	RequiredCount *int64
	AvailableFrom *time.Time
	Downloading   bool
	// OnDisk reports a file for this work, for albumStatus' own fallback.
	OnDisk bool
	// LastSearchedAt is nil for an album never searched.
	LastSearchedAt *time.Time
	// Unverified is an album known only by its own tags or folder (D6).
	Unverified bool
	// ArrivedAt is the Unix time the newest file arrived, 0 with none; the imported sort's cursor.
	ArrivedAt int64
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

	rootFolderIDs, categoryIDs, err := s.visibility(access, params.CategoryId)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	sort := WorkSort(derefOr(params.Sort, gen.ListWorksParamsSort("")))
	direction := SortAscending
	if derefOr(params.Dir, gen.ListWorksParamsDir("")) == gen.ListWorksParamsDirDesc {
		direction = SortDescending
	}

	rows, err := s.works.ListWorks(r.Context(),
		WorksFilter{
			RootFolderIDs: rootFolderIDs, CategoryIDs: categoryIDs, Sort: sort, Direction: direction,
			Fresh: derefOr(params.Fresh, false), Wanted: derefOr(params.Wanted, false), CutoffUnmet: derefOr(params.CutoffUnmet, false),
		},
		worksCursorFor(sort, cursor), limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, buildWorkPage(rows, limit, sort))
}

// worksCursorFor decodes the cursor's opaque sort value against whichever
// field the active sort actually reads -- ListWorks' own single paginate.TextCursor
// carries a plain int64-as-string for year/added_at, never a second cursor codec.
func worksCursorFor(sort WorkSort, cursor paginate.TextCursor) WorksCursor {
	switch sort {
	case WorkSortYear:
		value, _ := strconv.ParseInt(cursor.Sort, 10, 64)
		return WorksCursor{SortYear: value, ID: cursor.ID}
	case WorkSortAdded:
		value, _ := strconv.ParseInt(cursor.Sort, 10, 64)
		return WorksCursor{SortAdded: value, ID: cursor.ID}
	case WorkSortSearched:
		value, _ := strconv.ParseInt(cursor.Sort, 10, 64)
		return WorksCursor{SortSearched: value, ID: cursor.ID}
	case WorkSortImported:
		value, _ := strconv.ParseInt(cursor.Sort, 10, 64)
		return WorksCursor{SortImported: value, ID: cursor.ID}
	case WorkSortArtist:
		artist, title, _ := strings.Cut(cursor.Sort, artistCursorSeparator)
		return WorksCursor{SortArtist: artist, SortTitle: title, ID: cursor.ID}
	default:
		return WorksCursor{SortTitle: cursor.Sort, ID: cursor.ID}
	}
}

// visibility resolves what a caller may see into ADR-27's two halves -- root
// folder ids (on-disk presence) and category ids (desired_item.category_id,
// for a work with no root folder yet, e.g. a subscription-discovered album) --
// scoped to categoryID when given. A category the caller cannot read answers
// authz.ErrNotVisible, which writeError already maps to 404 (Invariant 37).
func (s *Server) visibility(access authz.Access, categoryID *int64) (rootFolderIDs, categoryIDs []int64, err error) {
	if categoryID != nil {
		if _, err := access.Category(*categoryID, settings.PermissionRead); err != nil {
			return nil, nil, err
		}
		categoryIDs = []int64{*categoryID}
		return s.rootsOf(categoryIDs), categoryIDs, nil
	}
	rootFolderIDs, categoryIDs = s.visibleAt(access, settings.PermissionRead)
	return rootFolderIDs, categoryIDs, nil
}

// visibleAt is every library the caller holds required in, and those libraries' root folders.
func (s *Server) visibleAt(access authz.Access, required settings.Permission) (rootFolderIDs, categoryIDs []int64) {
	categoryIDs = access.VisibleCategoryIDs(required)
	if categoryIDs == nil {
		categoryIDs = []int64{}
	}
	return s.rootsOf(categoryIDs), categoryIDs
}

// rootsOf is the root folders of categoryIDs.
func (s *Server) rootsOf(categoryIDs []int64) (rootFolderIDs []int64) {
	visible := make(map[int64]struct{}, len(categoryIDs))
	for _, id := range categoryIDs {
		visible[id] = struct{}{}
	}

	// Non-nil from here on, even when it stays empty: WorksFilter.RootFolderIDs
	// treats nil as unfiltered, which would leak every category's works the
	// instant a caller has none visible.
	rootFolderIDs = make([]int64, 0)
	for _, root := range s.auth.Settings.Snapshot().CategoryRoots() {
		if _, ok := visible[root.CategoryID]; ok {
			rootFolderIDs = append(rootFolderIDs, root.RootFolderID)
		}
	}
	return rootFolderIDs
}

// buildWorkPage trims the probe row (one more than pageSize) and, if present,
// encodes the cursor from it -- paginate.Build's shape, reimplemented here since
// Build is fixed to the integer-sorted Cursor and works sort on text.
func buildWorkPage(rows []WorkRow, limit int, sort WorkSort) gen.WorkPage {
	items := rows
	var nextCursor string
	if len(rows) > limit {
		items = rows[:limit]
		last := items[len(items)-1]
		nextCursor = paginate.TextCursor{Sort: cursorValueFor(sort, last), ID: last.ID}.Encode()
	}

	views := make([]gen.Work, len(items))
	for index, row := range items {
		views[index] = gen.Work{
			Id: row.ID, MediaType: row.MediaType, Title: row.Title, SortTitle: row.SortTitle,
			Year: yearOf(row.Year), Artist: row.Artist, AddedAt: row.AddedAt, UpdatedAt: row.UpdatedAt,
			WantedState: wantedStateOf(row.WantedState), LastSearchedAt: row.LastSearchedAt,
		}
		if row.Unverified {
			views[index].Unverified = &row.Unverified
		}
	}

	page := gen.WorkPage{Items: views}
	if nextCursor != "" {
		page.NextCursor = &nextCursor
	}
	return page
}

// cursorValueFor is worksCursorFor's inverse: the opaque sort value the next
// page's cursor carries for the row a page ended on.
func cursorValueFor(sort WorkSort, row WorkRow) string {
	switch sort {
	case WorkSortYear:
		return strconv.FormatInt(derefOr(row.Year, 0), 10)
	case WorkSortAdded:
		return strconv.FormatInt(row.AddedAt.Unix(), 10)
	case WorkSortSearched:
		if row.LastSearchedAt == nil {
			return "0"
		}
		return strconv.FormatInt(row.LastSearchedAt.Unix(), 10)
	case WorkSortImported:
		return strconv.FormatInt(row.ArrivedAt, 10)
	case WorkSortArtist:
		return row.ArtistSortName + artistCursorSeparator + row.SortTitle
	default:
		return row.SortTitle
	}
}

// artistCursorSeparator joins the artist order's two sort values inside the one
// text a cursor carries: the ASCII unit separator, which no name contains.
const artistCursorSeparator = "\x1f"

func wantedStateOf(state *string) *gen.WorkWantedState {
	if state == nil {
		return nil
	}
	value := gen.WorkWantedState(*state)
	return &value
}

func yearOf(year *int64) *int {
	if year == nil {
		return nil
	}
	value := int(*year)
	return &value
}
