package library

import (
	"context"
	"fmt"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

// personColumns is the sqlc model's field order, which the scan below depends
// on (documentation/sql-and-sqlc.md), same discipline as workColumns.
const personColumns = `id, name, sort_name, disambiguation, kind, gender, country,
       born, died, fields_json, monitored, added_at`

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
	predicates.AddIf(after.SortName != "" || after.ID != 0,
		"(sort_name > ? OR (sort_name = ? AND id > ?))",
		after.SortName, after.SortName, after.ID)

	query := "SELECT " + personColumns + "\nFROM person" + predicates.Where() +
		"\nORDER BY sort_name, id\nLIMIT ?"
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
	predicates.Add(clause+")", arguments...)
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
		predicates.Add("sort_name >= ?", prefix)
		return
	}
	predicates.Add("(sort_name >= ? AND sort_name < ?)", prefix, upper)
}

// ListPeople returns one page of the people list. Filtering by media type
// (named in architecture.md's own GET /people sketch) needs a join this pass
// does not build -- real scope of its own, left for when a caller actually
// needs it (generalise from evidence, not anticipation).
func (p People) ListPeople(ctx context.Context, filter PersonFilter, after PersonCursor, pageSize int) ([]librarydb.Person, error) {
	query, arguments := composePeopleList(filter, after, pageSize)

	rows, err := p.store.Read().QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("library: list people: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var people []librarydb.Person
	for rows.Next() {
		var person librarydb.Person
		if err := rows.Scan(&person.ID, &person.Name, &person.SortName, &person.Disambiguation,
			&person.Kind, &person.Gender, &person.Country, &person.Born, &person.Died,
			&person.FieldsJson, &person.Monitored, &person.AddedAt); err != nil {
			return nil, fmt.Errorf("library: scan person: %w", err)
		}
		people = append(people, person)
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

	rows, err := p.store.Read().QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, arguments...)
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
