package library

import (
	"context"
	"fmt"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// workColumns is the sqlc model's field order, which the scan below depends on
// (documentation/sql-and-sqlc.md).
const workColumns = `id, media_type, canonical_title, sort_title, original_language,
       year, runtime_ms, added_at, updated_at, added_by`

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
}

// WorkCursor is a position in the list: the sort title, plus the id to break ties.
// Without the id, a page boundary inside a group sharing a sort title drops rows.
type WorkCursor struct {
	SortTitle string
	ID        int64
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
	predicates.AddIf(after.SortTitle != "" || after.ID != 0,
		"(sort_title > ? OR (sort_title = ? AND id > ?))",
		after.SortTitle, after.SortTitle, after.ID)

	query := "SELECT " + workColumns + "\nFROM work" + predicates.Where() +
		"\nORDER BY sort_title, id\nLIMIT ?"
	return query, predicates.Args(pageSize)
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

// List returns one page of the library list.
func (w Works) List(ctx context.Context, filter WorkFilter, after WorkCursor, pageSize int) ([]librarydb.Work, error) {
	query, arguments := composeList(filter, after, pageSize)

	rows, err := w.store.Read().QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("library: list works: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var works []librarydb.Work
	for rows.Next() {
		var work librarydb.Work
		if err := rows.Scan(&work.ID, &work.MediaType, &work.CanonicalTitle, &work.SortTitle,
			&work.OriginalLanguage, &work.Year, &work.RuntimeMs, &work.AddedAt,
			&work.UpdatedAt, &work.AddedBy); err != nil {
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
