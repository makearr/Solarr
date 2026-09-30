package library

import (
	"context"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// personColumns is the sqlc model's field order, which the scan below depends
// on (documentation/sql-and-sqlc.md), same discipline as workColumns.
const personColumns = `id, name, sort_name, disambiguation, kind, gender, country,
       born, died, fields_json, monitored, added_at`

// personCoverageColumns are the counts the availability bar under an
// artist's picture is drawn from (2026-09-19): how many of the albums
// credited to them are wanted, how many of those are complete, and whether
// anything of theirs is downloading right now. Albums only: a track's own SINGLE item is not an
// album. Correlated subqueries for workStateColumn's reason -- a join would multiply the row.
const personCoverageColumns = `CAST((SELECT COUNT(*) FROM credit c
      JOIN desired_item di ON di.work_id = c.work_id AND di.monitored = 1 AND di.satisfaction_kind = 'SET'
      WHERE c.person_id = person.id) AS INTEGER) AS albums_wanted,
   CAST((SELECT COUNT(*) FROM credit c
      JOIN desired_item di ON di.work_id = c.work_id AND di.monitored = 1 AND di.satisfaction_kind = 'SET'
      WHERE c.person_id = person.id AND di.state IN ('COMPLETE', 'UPGRADABLE')) AS INTEGER) AS albums_complete,
   EXISTS (SELECT 1 FROM credit c
      JOIN desired_item di ON di.work_id = c.work_id
      JOIN acquisition a ON a.desired_item_id = di.id
      WHERE c.person_id = person.id
        AND a.state NOT IN ('IMPORTED', 'CLEANED', 'FAILED', 'CANCELLED')) AS downloading`

// PersonListRow is one person plus that coverage.
type PersonListRow struct {
	librarydb.Person
	AlbumsWanted   int64
	AlbumsComplete int64
	Downloading    bool
}

// PersonFilter is what the people list can narrow on. A zero field is not a
// filter (WorkFilter's own convention).
type PersonFilter struct {
	Monitored *bool
	// NamePrefix matches the start of the sort name, same range-scan reasoning
	// as WorkFilter.TitlePrefix.
	NamePrefix string
	// Role restricts to persons credited with exactly this role on a work
	// visible via RootFolderIDs/CategoryIDs (ADR-54's Artists level: role
	// "artist"). Empty means unfiltered -- the existing /people list's own
	// shape, kept working unchanged; the two visibility fields are then
	// ignored too.
	Role          string
	RootFolderIDs []int64
	CategoryIDs   []int64
	// Descending lists Z to A: a table header clicked twice (2026-09-25).
	Descending bool
}

// PersonCursor is a position in the people list: sort name, plus the id to
// break ties (WorkCursor's own reasoning).
type PersonCursor struct {
	SortName string
	ID       int64
}

// composePeopleList mirrors composeList (works.go): assembled rather than
// static SQL, so an optional predicate does not cost the sort_name index.
func composePeopleList(filter PersonFilter, after PersonCursor, pageSize int) (string, []any) {
	var predicates store.Predicates
	if filter.Monitored != nil {
		monitored := int64(0)
		if *filter.Monitored {
			monitored = 1
		}
		predicates.Add("monitored = ?", monitored)
	}
	addNamePrefix(&predicates, filter.NamePrefix)
	addArtistCredit(&predicates, filter.Role, filter.RootFolderIDs, filter.CategoryIDs)
	operator, direction := ">", ""
	if filter.Descending {
		operator, direction = "<", " DESC"
	}
	predicates.AddIf(after.SortName != "" || after.ID != 0,
		"(sort_name COLLATE NOCASE "+operator+" ? OR (sort_name COLLATE NOCASE = ? AND id "+operator+" ?))",
		after.SortName, after.SortName, after.ID)

	query := "SELECT " + personColumns + ",\n       " + personCoverageColumns +
		"\nFROM person" + predicates.Where() +
		"\nORDER BY sort_name COLLATE NOCASE" + direction + ", id" + direction + "\nLIMIT ?"
	return query, predicates.Args(pageSize)
}

// addArtistCredit restricts to persons credited role on a work visible via
// root folder or category (ADR-27), for ADR-54's Artists level. A correlated
// EXISTS, the same reasoning addVisibility's own doc comment gives: SQLite
// must be able to drive the outer scan from person's own sort_name index
// (person_sort) without a subquery derailing that (see notes.md).
func addArtistCredit(predicates *store.Predicates, role string, rootFolderIDs, categoryIDs []int64) {
	if role == "" {
		return
	}
	clause := "EXISTS (SELECT 1 FROM credit c JOIN work w ON w.id = c.work_id" +
		" WHERE c.person_id = person.id AND c.role = ?"
	arguments := []any{role}
	if fragment, visibilityArgs, applies := visibilityFragment("w.id", rootFolderIDs, categoryIDs); applies {
		clause += " AND " + fragment
		arguments = append(arguments, visibilityArgs...)
	}
	clause += ")"
	// A person monitored in one of these libraries belongs here before any work credits them.
	if len(categoryIDs) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(categoryIDs)), ",")
		clause = "(" + clause + " OR (person.monitored = 1 AND EXISTS (SELECT 1 FROM subscription s" +
			" WHERE s.subject_type = 'person' AND s.subject_id = person.id AND s.enabled = 1" +
			" AND s.category_id IN (" + placeholders + "))))"
		for _, id := range categoryIDs {
			arguments = append(arguments, id)
		}
	}
	predicates.Add(clause, arguments...)
}

// addNamePrefix is addPrefix (works.go), over sort_name instead of sort_title
// -- kept separate rather than shared since the two filter different tables
// and a generic version would need to thread the column name through anyway.
func addNamePrefix(predicates *store.Predicates, prefix string) {
	if prefix == "" {
		return
	}
	upper, bounded := prefixUpperBound(prefix)
	if !bounded {
		predicates.Add("sort_name COLLATE NOCASE >= ?", prefix)
		return
	}
	predicates.Add("(sort_name COLLATE NOCASE >= ? AND sort_name COLLATE NOCASE < ?)", prefix, upper)
}

// ListPeople returns one page of the people list. Filtering by media type
// (named in architecture.md's own GET /people sketch) needs a join this pass
// does not build -- real scope of its own, left for when a caller actually
// needs it (generalise from evidence, not anticipation).
func (p People) ListPeople(ctx context.Context, filter PersonFilter, after PersonCursor, pageSize int) ([]PersonListRow, error) {
	query, arguments := composePeopleList(filter, after, pageSize)

	rows, err := p.store.Read().QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("library: list people: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var people []PersonListRow
	for rows.Next() {
		var row PersonListRow
		person := &row.Person
		if err := rows.Scan(&person.ID, &person.Name, &person.SortName, &person.Disambiguation,
			&person.Kind, &person.Gender, &person.Country, &person.Born, &person.Died,
			&person.FieldsJson, &person.Monitored, &person.AddedAt,
			&row.AlbumsWanted, &row.AlbumsComplete, &row.Downloading); err != nil {
			return nil, fmt.Errorf("library: scan person: %w", err)
		}
		people = append(people, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: list people: %w", err)
	}
	return people, nil
}

// ExplainPeopleList returns SQLite's plan for the query ListPeople would run
// (Works.ExplainList's own reasoning: a test can assert the plan reaches an
// index instead of only that it returns the right rows).
func (p People) ExplainPeopleList(ctx context.Context, filter PersonFilter, after PersonCursor, pageSize int) (string, error) {
	query, arguments := composePeopleList(filter, after, pageSize)
	return p.store.ExplainQueryPlan(ctx, query, arguments...)
}
