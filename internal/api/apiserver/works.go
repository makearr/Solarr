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
	// The Radarr-style columns: size on disk, quality profile, status, genre and tag.
	WorkSortSize    WorkSort = "size"
	WorkSortProfile WorkSort = "profile"
	WorkSortStatus  WorkSort = "status"
	WorkSortGenre   WorkSort = "genre"
	WorkSortTag     WorkSort = "tag"
)

// Facet answers the facet key of a "facet:<key>" sort, empty for a base one.
func (s WorkSort) Facet() string {
	key, _ := strings.CutPrefix(string(s), "facet:")
	if key == string(s) {
		return ""
	}
	return key
}

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
	// TagIDs are tags a work must carry (or one below each), ExcludedTagIDs tags it must not: the list's chips.
	TagIDs, ExcludedTagIDs []int64
	// AboveRating keeps the works the library's maximum rating does not admit.
	AboveRating bool
	// MediaType keeps only works of one media type, "" all (a mixed library's type chip).
	MediaType string
	// Where is the custom filter as the JSON the request carried; malformed answers ErrInvalidChoice.
	Where string
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
	SortNumber   int64
	SortText     string
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
	// SortNumber and SortText are the value the size/profile and status/genre/tag orders ranked the row by.
	SortNumber int64
	SortText   string
	// ArtistCover is an album shown by its artist's image, having no cover of its own.
	ArtistCover bool
	// NoArtwork says no cached picture answers for it; a provider poster may still.
	NoArtwork   bool
	Monitored   bool
	Description string
	SizeBytes   int64
	// LeadID is the person the work is listed under, 0 with none.
	LeadID int64
	// Facets are the media type's own fields of the work, by facet key.
	Facets map[string][]string
	// PresentTotal, RequiredTotal and Upgradable sum the work's monitored groups.
	PresentTotal, RequiredTotal, Upgradable int64
	// PosterURL is the work's poster at its provider, empty for a work with none (an album's cover is its artwork).
	PosterURL string
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
	if cursor.Sort == "" && cursor.ID == 0 && params.From != nil && derefOr(params.Dir, gen.ListWorksParamsDir("")) != gen.ListWorksParamsDirDesc {
		cursor = startAtLetter(WorkSort(derefOr(params.Sort, "")), *params.From)
	}

	if s.works == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the library is not available")
		return
	}

	rootFolderIDs, categoryIDs, err := s.libraryVisibility(access, params.CategoryId, derefOr(params.WholeLibrary, false), settings.PermissionRead)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	sort := WorkSort(derefOr(params.Sort, ""))
	direction := SortAscending
	if derefOr(params.Dir, gen.ListWorksParamsDir("")) == gen.ListWorksParamsDirDesc {
		direction = SortDescending
	}

	carry, without, err := parseTagChips(derefOr(params.Tags, ""))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "tags must be tag ids, a negative one to leave out")
		return
	}
	rows, err := s.works.ListWorks(r.Context(),
		WorksFilter{
			RootFolderIDs: rootFolderIDs, CategoryIDs: categoryIDs, Sort: sort, Direction: direction,
			Fresh: derefOr(params.Fresh, false), Wanted: derefOr(params.Wanted, false), CutoffUnmet: derefOr(params.CutoffUnmet, false),
			Where: derefOr(params.Where, ""), TagIDs: carry, ExcludedTagIDs: without, AboveRating: derefOr(params.AboveRating, false),
			MediaType: derefOr(params.MediaType, ""),
		},
		worksCursorFor(sort, cursor), limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, buildWorkPage(rows, limit, sort, s.wall.Now()))
}

// startAtLetter is the cursor that begins a title or artist ordering at the letter: everything sorting after it, which is
// every name starting with it. The id is below any real one, which is also what marks the cursor as set.
func startAtLetter(sort WorkSort, letter string) paginate.TextCursor {
	switch sort {
	case WorkSortArtist:
		return paginate.TextCursor{Sort: letter + artistCursorSeparator, ID: -1}
	case WorkSortTitle, "title":
		return paginate.TextCursor{Sort: letter, ID: -1}
	}
	return paginate.TextCursor{}
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
	case WorkSortSize, WorkSortProfile:
		value, _ := strconv.ParseInt(cursor.Sort, 10, 64)
		return WorksCursor{SortNumber: value, ID: cursor.ID}
	case WorkSortStatus, WorkSortGenre, WorkSortTag:
		return WorksCursor{SortText: cursor.Sort, ID: cursor.ID}
	case WorkSortArtist:
		artist, title, _ := strings.Cut(cursor.Sort, artistCursorSeparator)
		return WorksCursor{SortArtist: artist, SortTitle: title, ID: cursor.ID}
	}
	if sort.Facet() != "" {
		return WorksCursor{SortText: cursor.Sort, ID: cursor.ID}
	}
	return WorksCursor{SortTitle: cursor.Sort, ID: cursor.ID}
}

// visibility resolves what a caller may see into ADR-27's two halves -- root
// folder ids (on-disk presence) and category ids (desired_item.category_id,
// for a work with no root folder yet, e.g. a subscription-discovered album) --
// scoped to categoryID when given. A category the caller cannot read answers
// authz.ErrNotVisible, which writeError already maps to 404 (Invariant 37).
func (s *Server) visibility(access authz.Access, categoryID *int64) (rootFolderIDs, categoryIDs []int64, err error) {
	return s.visibilityAt(access, categoryID, settings.PermissionRead)
}

// visibilityAt is visibility for an action needing required rather than read.
func (s *Server) visibilityAt(
	access authz.Access, categoryID *int64, required settings.Permission,
) (rootFolderIDs, categoryIDs []int64, err error) {
	if categoryID != nil {
		if _, err := access.Category(*categoryID, required); err != nil {
			return nil, nil, err
		}
		categoryIDs = []int64{*categoryID}
		return s.rootsOf(categoryIDs), categoryIDs, nil
	}
	rootFolderIDs, categoryIDs = s.visibleAt(access, required)
	return rootFolderIDs, categoryIDs, nil
}

// libraryVisibility is visibilityAt, widened to every media type of the library when whole is set (its All list).
func (s *Server) libraryVisibility(
	access authz.Access, categoryID *int64, whole bool, required settings.Permission,
) (rootFolderIDs, categoryIDs []int64, err error) {
	if !whole || categoryID == nil {
		return s.visibilityAt(access, categoryID, required)
	}
	for _, category := range s.auth.Settings.Snapshot().LibraryCategories(*categoryID) {
		if _, err := access.Category(category.ID, required); err != nil {
			return nil, nil, err
		}
		categoryIDs = append(categoryIDs, category.ID)
	}
	if len(categoryIDs) == 0 {
		return s.visibilityAt(access, categoryID, required)
	}
	return s.rootsOf(categoryIDs), categoryIDs, nil
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
func buildWorkPage(rows []WorkRow, limit int, sort WorkSort, now time.Time) gen.WorkPage {
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
		if row.ArtistCover {
			views[index].ArtistCover = &row.ArtistCover
		}
		if row.NoArtwork {
			views[index].NoArtwork = &row.NoArtwork
		}
		// What Radarr's poster, overview and table show (2026-09-30); status is the poster wall's own colour.
		status := gen.WorkStatus(albumStatus(PersonWorkRow{
			WantedState: row.WantedState, OnDisk: row.OnDisk, AvailableFrom: row.AvailableFrom,
			Downloading: row.Downloading, Unverified: row.Unverified,
		}, now))
		monitored, size := row.Monitored, row.SizeBytes
		views[index].Status, views[index].Monitored, views[index].SizeBytes = &status, &monitored, &size
		views[index].PresentCount, views[index].RequiredCount = row.PresentCount, row.RequiredCount
		if row.LeadID != 0 {
			views[index].LeadId = &row.LeadID
		}
		facets := row.Facets
		if facets == nil {
			facets = map[string][]string{}
		}
		views[index].Facets = &facets
		views[index].PosterUrl = strPtr(row.PosterURL)
		views[index].Totals = &struct {
			Present    int64 `json:"present"`
			Required   int64 `json:"required"`
			Upgradable int64 `json:"upgradable"`
		}{Present: row.PresentTotal, Required: row.RequiredTotal, Upgradable: row.Upgradable}
		if row.Description != "" {
			overview := overviewOf(row.Description)
			views[index].Overview = &overview
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
	case WorkSortSize, WorkSortProfile:
		return strconv.FormatInt(row.SortNumber, 10)
	case WorkSortStatus, WorkSortGenre, WorkSortTag:
		return row.SortText
	case WorkSortArtist:
		return row.ArtistSortName + artistCursorSeparator + row.SortTitle
	default:
		if sort.Facet() != "" {
			return row.SortText
		}
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
