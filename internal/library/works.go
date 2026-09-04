package library

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// workColumns is the sqlc model's field order, which the scan below depends on
// (documentation/sql-and-sqlc.md).
const workColumns = `id, media_type, canonical_title, sort_title, original_language,
       year, runtime_ms, added_at, updated_at, added_by`

// workArtistColumn is the work list's one join: the credited artist's own name,
// so the list can show it without a second round-trip per row. Scoped to the
// "artist" role and limited to one row via credit_by_work(work_id, role), so it
// stays an index point-lookup regardless of corpus size (ADR-28) rather than a
// join that could multiply rows out for a work credited to several artists.
const workArtistColumn = `(SELECT p.name FROM credit c JOIN person p ON p.id = c.person_id
        WHERE c.work_id = work.id AND c.role = 'artist' ORDER BY c.ord LIMIT 1) AS artist`

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
	// any of these root folders -- the temporary bridge category filtering uses
	// (migration 0012's comment) until desired_item exists for music. Nil means
	// unfiltered, distinct from an empty non-nil slice, which matches nothing --
	// callers translate "no root folder is visible" into an empty result rather
	// than an unfiltered one themselves.
	RootFolderIDs []int64
	// Sort and Direction pick the list's order (section 21.2's configurable,
	// sortable columns). Artist is deliberately not sortable: it comes from a
	// join with no supporting index, so ordering by it would fall back to
	// sorting the whole result (ADR-28) -- displayed, but not orderable, until
	// there is a real reason to pay for that index.
	Sort      WorkSort
	Direction SortDirection
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

// WorkListRow is one row of the library list plus its primary artist credit.
type WorkListRow struct {
	librarydb.Work
	Artist sql.NullString
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
	addRootFolders(&predicates, filter.RootFolderIDs)

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
		"("+column+" "+operator+" ? OR ("+column+" = ? AND id > ?))",
		cursorValue, cursorValue, after.ID)

	query := "SELECT " + workColumns + ",\n       " + workArtistColumn + "\nFROM work" +
		predicates.Where() + "\nORDER BY " + column + orderDirection + ", id\nLIMIT ?"
	return query, predicates.Args(pageSize)
}

// sortColumn maps a WorkSort to its SQL column and the cursor value to compare
// it against.
func sortColumn(sort WorkSort, after WorkCursor) (column string, cursorValue any) {
	switch sort {
	case WorkSortYear:
		return "year", after.SortYear
	case WorkSortAdded:
		return "added_at", after.SortAdded
	default:
		return "sort_title", after.SortTitle
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

// addRootFolders restricts to works seen under any of ids. nil means the filter
// was not asked for; a non-nil empty slice ("nothing is visible") deliberately
// matches no rows rather than being indistinguishable from unfiltered.
func addRootFolders(predicates *store.Predicates, ids []int64) {
	if ids == nil {
		return
	}
	if len(ids) == 0 {
		predicates.Add("0 = 1")
		return
	}
	placeholders := make([]string, len(ids))
	arguments := make([]any, len(ids))
	for index, id := range ids {
		placeholders[index] = "?"
		arguments[index] = id
	}
	predicates.Add("id IN (SELECT work_id FROM work_root_folder WHERE root_folder_id IN ("+
		strings.Join(placeholders, ",")+"))", arguments...)
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
			&work.UpdatedAt, &work.AddedBy, &work.Artist); err != nil {
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
