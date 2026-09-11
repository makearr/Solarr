package library

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

// workColumns is the sqlc model's field order, which the scan below depends on
// (documentation/sql-and-sqlc.md). Qualified with "work." -- person (joined in
// for workArtistColumn) also has id and added_at columns, which would
// otherwise be ambiguous.
const workColumns = `work.id, work.media_type, work.canonical_title, work.sort_title, work.original_language,
       work.year, work.runtime_ms, work.added_at, work.updated_at, work.added_by`

// workArtistColumn reads work.primary_artist_id (migration 0021) via a plain
// LEFT JOIN -- a correlated subquery here used to force SQLite to sort the
// whole result regardless of index (ADR-28; see notes.md for the story).
const workArtistColumn = `primary_artist.name AS artist`

// workArtistJoin is workArtistColumn's join, appended to every list query's
// FROM clause. "primary_artist" avoids colliding with any other query in this
// file that might alias person differently.
const workArtistJoin = `
LEFT JOIN person primary_artist ON primary_artist.id = work.primary_artist_id`

// workStateColumn reports one monitored desired_item's own state for the
// work (NULL when it has none) -- a scalar subquery rather than a join, so a
// work with more than one desired_item (rare: an edition-specific SET
// alongside the album-level one) still yields exactly one row here, matching
// every other column. ORDER BY id makes the pick deterministic rather than
// SQLite's own arbitrary one.
const workStateColumn = `(SELECT di.state FROM desired_item di
      WHERE di.work_id = work.id AND di.monitored = 1
      ORDER BY di.id LIMIT 1) AS wanted_state`

// WorkSort selects which column the list is ordered by. The zero value sorts
// by title, matching every caller that does not set it.
type WorkSort string

// The columns the list can be ordered by.
const (
	WorkSortTitle WorkSort = ""
	WorkSortYear  WorkSort = "year"
	WorkSortAdded WorkSort = "added"
)

// SortDirection is which way WorkSort orders. The zero value is ascending.
type SortDirection string

// The directions a WorkSort can order in.
const (
	SortAscending  SortDirection = ""
	SortDescending SortDirection = "desc"
)

// WorkFilter is what the library list can narrow on. A zero field is not a filter, so
// it never reaches the SQL (section 21.2).
type WorkFilter struct {
	MediaType string
	Language  string
	YearFrom  int64
	YearTo    int64
	// TitlePrefix matches the start of the sort title, which is already normalised
	// for ordering.
	TitlePrefix string
	// RootFolderIDs restricts the list to works work_root_folder recorded against
	// any of these root folders -- on-disk presence, half of ADR-27 visibility.
	// Nil means this half is not applied; a non-nil empty slice matches nothing
	// via this half. CategoryIDs is the other half (desired_item.category_id, for
	// a work with no root folder yet); a work matching either is included. Both
	// nil together means fully unfiltered -- callers translate "nothing visible"
	// into an empty non-nil slice on at least one of the two themselves.
	RootFolderIDs []int64
	// CategoryIDs restricts the list to works with a desired_item recorded
	// against any of these categories -- see RootFolderIDs' own doc comment.
	CategoryIDs []int64
	// FreshSince is ADR-55's release-date freshness window (todo 4.20),
	// already resolved to a cutoff date ("2006-01-02") by the caller --
	// this package never calls time.Now() (forbidigo). Empty means the
	// filter is not applied. A work_date shorter than a full date (a
	// year- or month-only release) can never match: it always sorts before
	// any real cutoff in the plain string comparison this uses, which is
	// the point -- freshness needs day precision, not a guess from less.
	FreshSince string
	// Sort and Direction pick the list's order (section 21.2's configurable,
	// sortable columns). Artist is not a WorkSort option yet -- work_primary_artist_sort
	// (migration 0021) could support it now, but nothing has asked for it.
	Sort      WorkSort
	Direction SortDirection
	// WantedOnly restricts to works with a monitored desired_item that is
	// not yet COMPLETE -- todo 4.13's follow-up "Wanted" per-library screen,
	// asked for 2026-09-11. Reuses this list's own pagination/sort/visibility
	// rather than a second query.
	WantedOnly bool
}

// WorkCursor is a position in the list: the id always breaks ties, since without it a
// page boundary inside a group sharing a sort value drops rows. Only the field matching
// the active WorkSort is read; the others are ignored.
type WorkCursor struct {
	SortTitle string
	SortYear  int64
	SortAdded int64
	ID        int64
}

// WorkListRow is one row of the library list plus its primary artist credit
// and, when it has a monitored desired_item, that item's own state.
type WorkListRow struct {
	librarydb.Work
	Artist      sql.NullString
	WantedState sql.NullString
}

// Works reads the library list surface.
type Works struct {
	store *store.Store
}

// NewWorks builds a reader over an open library store.
func NewWorks(opened *store.Store) Works { return Works{store: opened} }

// composeList builds the query and its arguments from the filters actually set. The SQL
// is assembled rather than written in queries/ because an optional predicate in static
// SQL costs the index entirely -- 31 times slower, measured
// (documentation/benchmarks.md). One function, so Explain always describes what List
// runs.
func composeList(filter WorkFilter, after WorkCursor, pageSize int) (string, []any) {
	var predicates store.Predicates
	predicates.AddIf(filter.MediaType != "", "media_type = ?", filter.MediaType)
	predicates.AddIf(filter.Language != "", "original_language = ?", filter.Language)
	predicates.AddIf(filter.YearFrom != 0, "year >= ?", filter.YearFrom)
	predicates.AddIf(filter.YearTo != 0, "year <= ?", filter.YearTo)
	addPrefix(&predicates, filter.TitlePrefix)
	addVisibility(&predicates, filter.RootFolderIDs, filter.CategoryIDs)
	predicates.AddIf(filter.FreshSince != "",
		"EXISTS (SELECT 1 FROM work_date wd WHERE wd.work_id = work.id AND wd.kind = ? AND wd.date >= ?)",
		WorkDateStreet, filter.FreshSince)
	predicates.AddIf(filter.WantedOnly,
		"EXISTS (SELECT 1 FROM desired_item di WHERE di.work_id = work.id AND di.monitored = 1 AND di.state != 'COMPLETE')")

	column, cursorValue := sortColumn(filter.Sort, after)
	operator := ">"
	orderDirection := ""
	if filter.Direction == SortDescending {
		operator = "<"
		orderDirection = " DESC"
	}
	// The id tie-break always advances ascending: it is a distinct sort key from
	// column, not affected by column's own direction, and ORDER BY below keeps it
	// ascending too so the two stay consistent.
	predicates.AddIf(after.ID != 0,
		"("+column+" "+operator+" ? OR ("+column+" = ? AND work.id > ?))",
		cursorValue, cursorValue, after.ID)

	query := "SELECT " + workColumns + ",\n       " + workArtistColumn + ",\n       " + workStateColumn +
		"\nFROM work" + workArtistJoin +
		predicates.Where() + "\nORDER BY " + column + orderDirection + ", work.id\nLIMIT ?"
	return query, predicates.Args(pageSize)
}

// sortColumn maps a WorkSort to its SQL column and the cursor value to compare
// it against. Qualified with "work." -- workArtistColumn's join makes "added_at"
// otherwise ambiguous against person's own column of the same name.
func sortColumn(sort WorkSort, after WorkCursor) (column string, cursorValue any) {
	switch sort {
	case WorkSortYear:
		return "work.year", after.SortYear
	case WorkSortAdded:
		return "work.added_at", after.SortAdded
	default:
		return "work.sort_title", after.SortTitle
	}
}

// addPrefix filters on the start of the sort title as a range, so it stays a range scan
// on the index. LIKE 'x%' would not: SQLite only optimises that with a case-insensitive
// collation the column does not have.
func addPrefix(predicates *store.Predicates, prefix string) {
	if prefix == "" {
		return
	}
	upper, bounded := prefixUpperBound(prefix)
	if !bounded {
		predicates.Add("sort_title >= ?", prefix)
		return
	}
	predicates.Add("(sort_title >= ? AND sort_title < ?)", prefix, upper)
}

// prefixUpperBound is the first string ordering after every string with this prefix.
// A prefix of nothing but 0xff bytes has no such bound, and then the lower one stands
// alone rather than a wrong upper one being invented.
func prefixUpperBound(prefix string) (string, bool) {
	raw := []byte(prefix)
	for index := len(raw) - 1; index >= 0; index-- {
		if raw[index] == 0xff {
			continue
		}
		raw[index]++
		return string(raw[:index+1]), true
	}
	return "", false
}

// addVisibility restricts to works visible via either root folder (on-disk
// presence) or category (desired_item.category_id, for a work with no root
// folder yet) -- ADR-27's two halves, OR'd together. Either side nil means
// that half was not asked for and contributes nothing; both nil together
// leaves the list unfiltered. A non-nil empty slice on a side that applies
// matches no rows via that side, same as before this had two halves.
//
// EXISTS, correlated on work.id, not "id IN (SELECT ...)": the IN form drove
// SQLite to abandon the sort index entirely (ADR-28; see notes.md).
func addVisibility(predicates *store.Predicates, rootFolderIDs, categoryIDs []int64) {
	fragment, arguments, applies := visibilityFragment("work.id", rootFolderIDs, categoryIDs)
	if applies {
		predicates.Add(fragment, arguments...)
	}
}

// visibilityFragment is addVisibility's own OR-of-two-halves logic, factored
// out so a second real caller (peoplelist.go's Artists-level query, credit-
// correlated rather than directly against work.id) shares it instead of
// re-deriving the same easy-to-get-subtly-wrong combination.
func visibilityFragment(workIDExpr string, rootFolderIDs, categoryIDs []int64) (fragment string, arguments []any, applies bool) {
	rootFragment, rootArgs, rootApplies := membershipFragment(
		"EXISTS (SELECT 1 FROM work_root_folder wrf WHERE wrf.work_id = "+workIDExpr+" AND wrf.root_folder_id IN (%s))",
		rootFolderIDs)
	categoryFragment, categoryArgs, categoryApplies := membershipFragment(
		"EXISTS (SELECT 1 FROM desired_item di WHERE di.work_id = "+workIDExpr+" AND di.category_id IN (%s))",
		categoryIDs)

	switch {
	case !rootApplies && !categoryApplies:
		return "", nil, false
	case rootApplies && !categoryApplies:
		return rootFragment, rootArgs, true
	case !rootApplies && categoryApplies:
		return categoryFragment, categoryArgs, true
	default:
		return "(" + rootFragment + " OR " + categoryFragment + ")",
			append(append([]any{}, rootArgs...), categoryArgs...), true
	}
}

// membershipFragment builds one "IN (...)" clause (template holding one %s for
// the placeholder list) for ids. applies is false when ids is nil (the caller
// did not ask for this filter at all); an empty non-nil ids deliberately
// renders as a clause matching nothing, not as "unfiltered".
func membershipFragment(template string, ids []int64) (fragment string, arguments []any, applies bool) {
	if ids == nil {
		return "", nil, false
	}
	if len(ids) == 0 {
		return "0 = 1", nil, true
	}
	placeholders := make([]string, len(ids))
	arguments = make([]any, len(ids))
	for index, id := range ids {
		placeholders[index] = "?"
		arguments[index] = id
	}
	return fmt.Sprintf(template, strings.Join(placeholders, ",")), arguments, true
}

// List returns one page of the library list.
func (w Works) List(ctx context.Context, filter WorkFilter, after WorkCursor, pageSize int) ([]WorkListRow, error) {
	query, arguments := composeList(filter, after, pageSize)

	rows, err := w.store.Read().QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("library: list works: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var works []WorkListRow
	for rows.Next() {
		var work WorkListRow
		if err := rows.Scan(&work.ID, &work.MediaType, &work.CanonicalTitle, &work.SortTitle,
			&work.OriginalLanguage, &work.Year, &work.RuntimeMs, &work.AddedAt,
			&work.UpdatedAt, &work.AddedBy, &work.Artist, &work.WantedState); err != nil {
			return nil, fmt.Errorf("library: scan work: %w", err)
		}
		works = append(works, work)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: list works: %w", err)
	}
	return works, nil
}

// ExplainList returns SQLite's plan for the query List would run, so a test can assert
// that a filter reaches an index rather than only that it returns the right rows.
func (w Works) ExplainList(ctx context.Context, filter WorkFilter, after WorkCursor, pageSize int) (string, error) {
	query, arguments := composeList(filter, after, pageSize)

	rows, err := w.store.Read().QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, arguments...)
	if err != nil {
		return "", fmt.Errorf("library: explain: %w", err)
	}
	defer func() { _ = rows.Close() }()

	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			return "", fmt.Errorf("library: scan plan: %w", err)
		}
		plan += detail + "\n"
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("library: explain: %w", err)
	}
	return plan, nil
}
