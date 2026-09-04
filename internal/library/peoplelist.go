package library

import (
	"context"
	"fmt"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
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
	predicates.AddIf(after.SortName != "" || after.ID != 0,
		"(sort_name > ? OR (sort_name = ? AND id > ?))",
		after.SortName, after.SortName, after.ID)

	query := "SELECT " + personColumns + "\nFROM person" + predicates.Where() +
		"\nORDER BY sort_name, id\nLIMIT ?"
	return query, predicates.Args(pageSize)
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

// ListPeople returns one page of the people list. Filtering by role or media
// type (both named in architecture.md's own GET /people sketch) needs a join
// against credit this pass does not build -- real scope of its own, left for
// when a caller actually needs it (generalise from evidence, not
// anticipation).
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
