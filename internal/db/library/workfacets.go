package library

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/metadata/metaresolve"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

// The sources a work facet is read from, as media.Facet declares them.
const (
	FacetColumn         = "column"
	FacetField          = "field"
	FacetOrganisation   = "organisation"
	FacetCredit         = "credit"
	FacetClassification = "classification"
	FacetGenreTree      = "genre_tree"
)

// facetColumns are the work columns a facet may read; the media registry refuses any other.
var facetColumns = []string{"year", "subtype", "runtime_ms", "original_language"}

// WorkFacet is one facet of a media type as the works list reads and filters it.
type WorkFacet struct {
	Key string
	// Source is one of the Facet* sources and Name what it names there: a column, a field, a role, a kind, a tag source.
	Source string
	Name   string
	// Kind is what a custom filter compares it as, and Choices an enum's values.
	Kind    listfilter.Kind
	Choices []string
	// Sortable lets the list order by it.
	Sortable bool
	// Badge is an organisation facet whose logo marks the work's tile.
	Badge bool
}

// FacetFields adds the filterable facets to a list's own custom-filter fields; a facet wins over a base field of
// the same key.
func FacetFields(base map[string]listfilter.Field, facets []WorkFacet) map[string]listfilter.Field {
	fields := make(map[string]listfilter.Field, len(base)+len(facets))
	for key, field := range base {
		fields[key] = field
	}
	for _, facet := range facets {
		fields[facet.Key] = listfilter.Field{Kind: facet.Kind, Choices: facet.Choices}
	}
	return fields
}

// facetCondition is one custom-filter condition on a facet as a predicate over work; false for a facet it cannot read.
func facetCondition(condition listfilter.Condition, facet WorkFacet) (string, []any, bool) {
	switch facet.Source {
	case FacetColumn:
		if !slices.Contains(facetColumns, facet.Name) {
			return "", nil, false
		}
		expression := "COALESCE(work." + facet.Name + ", 0)"
		if facet.Kind == listfilter.Text || facet.Kind == listfilter.Enum {
			expression = "COALESCE(work." + facet.Name + ", '')"
		}
		fragment, arguments := condition.SQL(facet.Kind, expression)
		return fragment, arguments, true
	case FacetField:
		expression := "COALESCE((SELECT f.value FROM work_field f WHERE f.work_id = work.id AND f.field = ? AND f.value != ''" +
			" ORDER BY " + metaresolve.OrderSQL("f") + " LIMIT 1), '')"
		fragment, arguments := condition.SQL(facet.Kind, expression)
		return fragment, append([]any{facet.Name}, arguments...), true
	case FacetOrganisation:
		return existsValue(condition, "FROM work_organisation fo JOIN organisation fon ON fon.id = fo.organisation_id"+
			" WHERE fo.work_id = work.id AND fo.role = ?", "fon.name", facet.Name)
	case FacetCredit:
		return existsValue(condition, "FROM credit fcr JOIN person fcp ON fcp.id = fcr.person_id"+
			" WHERE fcr.work_id = work.id AND fcr.role = ?", "fcp.name", facet.Name)
	case FacetClassification:
		return existsValue(condition, "FROM work_classification fc WHERE fc.work_id = work.id AND fc.kind = ?", "fc.value", facet.Name)
	case FacetGenreTree:
		return existsValue(condition, "FROM tag_assignment ft JOIN tag ftn ON ftn.id = ft.tag_id"+
			" WHERE ft.subject_type = 'work' AND ft.subject_id = work.id AND ftn.genre_tree = ?", "ftn.name", facet.Name)
	}
	return "", nil, false
}

// facetSortColumn is the text a facet sort orders a work by: its value, or the first of several by name. Names come
// from the compiled descriptors, never from a request, and are quoted all the same.
func facetSortColumn(facet WorkFacet) string {
	name := "'" + strings.ReplaceAll(facet.Name, "'", "''") + "'"
	switch facet.Source {
	case FacetColumn:
		if slices.Contains(facetColumns, facet.Name) {
			return "COALESCE(CAST(work." + facet.Name + " AS TEXT), '') COLLATE NOCASE"
		}
	case FacetField:
		return "COALESCE((SELECT sf.value FROM work_field sf WHERE sf.work_id = work.id AND sf.field = " + name +
			" AND sf.value != '' ORDER BY " + metaresolve.OrderSQL("sf") + " LIMIT 1), '') COLLATE NOCASE"
	case FacetOrganisation:
		return "COALESCE((SELECT MIN(son.name) FROM work_organisation so JOIN organisation son ON son.id = so.organisation_id" +
			" WHERE so.work_id = work.id AND so.role = " + name + "), '') COLLATE NOCASE"
	case FacetCredit:
		return "COALESCE((SELECT MIN(scp.name) FROM credit scr JOIN person scp ON scp.id = scr.person_id" +
			" WHERE scr.work_id = work.id AND scr.role = " + name + "), '') COLLATE NOCASE"
	case FacetClassification:
		return "COALESCE((SELECT MIN(sc.value) FROM work_classification sc WHERE sc.work_id = work.id AND sc.kind = " + name + "), '') COLLATE NOCASE"
	case FacetGenreTree:
		return "COALESCE((SELECT MIN(stn.name) FROM tag_assignment st JOIN tag stn ON stn.id = st.tag_id" +
			" WHERE st.subject_type = 'work' AND st.subject_id = work.id AND stn.genre_tree = " + name + "), '') COLLATE NOCASE"
	}
	return "''"
}

// existsValue is existsText over a subquery with one argument: "has a value matching", and for the negative
// operators "has no value matching".
func existsValue(condition listfilter.Condition, from, column, argument string) (string, []any, bool) {
	negative := condition.Op == "not_contains" || condition.Op == "is_not"
	positive := condition
	switch condition.Op {
	case "not_contains":
		positive.Op = "contains"
	case "is_not":
		positive.Op = "is"
	}
	fragment, arguments := positive.SQL(listfilter.Text, column)
	exists := "EXISTS (SELECT 1 " + from + " AND " + fragment + ")"
	if negative {
		exists = "NOT " + exists
	}
	return exists, append([]any{argument}, arguments...), true
}

// addFacetConditions adds the conditions naming a facet; the others are left to the list's own fields.
func addFacetConditions(predicates *store.Predicates, conditions []listfilter.Condition, facets []WorkFacet) []listfilter.Condition {
	var rest []listfilter.Condition
	for _, condition := range conditions {
		index := slices.IndexFunc(facets, func(facet WorkFacet) bool { return facet.Key == condition.Field })
		if index < 0 {
			rest = append(rest, condition)
			continue
		}
		if fragment, arguments, ok := facetCondition(condition, facets[index]); ok {
			predicates.Add(fragment, arguments...)
		}
	}
	return rest
}

// FacetValues reads the facets of a page of works: work id to facet key to its values, one for a single-valued facet.
func (w Works) FacetValues(ctx context.Context, facets []WorkFacet, workIDs []int64) (map[int64]map[string][]string, error) {
	values := make(map[int64]map[string][]string, len(workIDs))
	if len(workIDs) == 0 || len(facets) == 0 {
		return values, nil
	}
	add := func(workID int64, key, value string) {
		if value == "" {
			return
		}
		if values[workID] == nil {
			values[workID] = map[string][]string{}
		}
		if !slices.Contains(values[workID][key], value) {
			values[workID][key] = append(values[workID][key], value)
		}
	}
	ids, idArgs := idList(workIDs)
	bySource := map[string][]WorkFacet{}
	for _, facet := range facets {
		bySource[facet.Source] = append(bySource[facet.Source], facet)
	}
	reader := w.store.Read()
	for _, facet := range bySource[FacetColumn] {
		if !slices.Contains(facetColumns, facet.Name) {
			continue
		}
		err := eachRow(ctx, reader, "SELECT id, CAST(COALESCE("+facet.Name+", '') AS TEXT) FROM work WHERE id IN ("+ids+")", idArgs,
			func(id int64, value string) { add(id, facet.Key, value) })
		if err != nil {
			return nil, err
		}
	}
	if fields := bySource[FacetField]; len(fields) > 0 {
		keyOf := map[string]string{}
		var names []any
		for _, facet := range fields {
			keyOf[facet.Name] = facet.Key
			names = append(names, facet.Name)
		}
		query := "SELECT work_id, field, value FROM (SELECT f.work_id, f.field, f.value, ROW_NUMBER() OVER (PARTITION BY f.work_id, f.field ORDER BY " +
			metaresolve.OrderSQL("f") + ") AS rank FROM work_field f WHERE f.value != '' AND f.work_id IN (" + ids + ") AND f.field IN (" +
			marks(len(names)) + ")) WHERE rank = 1"
		err := eachTriple(ctx, reader, query, append(slices.Clone(idArgs), names...), func(id int64, field, value string) { add(id, keyOf[field], value) })
		if err != nil {
			return nil, err
		}
	}
	for source, query := range map[string]string{
		FacetOrganisation: "SELECT wo.work_id, wo.role, o.name FROM work_organisation wo JOIN organisation o ON o.id = wo.organisation_id" +
			" WHERE wo.work_id IN (%s) AND wo.role IN (%s) ORDER BY wo.work_id, o.name",
		FacetCredit: "SELECT DISTINCT cr.work_id, cr.role, p.name FROM credit cr JOIN person p ON p.id = cr.person_id" +
			" WHERE cr.work_id IN (%s) AND cr.role IN (%s) ORDER BY cr.work_id, p.name",
		FacetClassification: "SELECT work_id, kind, value FROM work_classification WHERE work_id IN (%s) AND kind IN (%s) ORDER BY work_id, value",
		FacetGenreTree: "SELECT DISTINCT ta.subject_id, t.genre_tree, t.name FROM tag_assignment ta JOIN tag t ON t.id = ta.tag_id" +
			" WHERE ta.subject_type = 'work' AND ta.subject_id IN (%s) AND t.genre_tree IN (%s) ORDER BY ta.subject_id, t.name",
	} {
		named := bySource[source]
		if len(named) == 0 {
			continue
		}
		keysOf := map[string][]string{}
		var names []any
		for _, facet := range named {
			if len(keysOf[facet.Name]) == 0 {
				names = append(names, facet.Name)
			}
			keysOf[facet.Name] = append(keysOf[facet.Name], facet.Key)
		}
		err := eachTriple(ctx, reader, fmt.Sprintf(query, ids, marks(len(names))), append(slices.Clone(idArgs), names...),
			func(id int64, name, value string) {
				for _, key := range keysOf[name] {
					add(id, key, value)
				}
			})
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

// Badge is the organisation whose logo marks a work's tile.
type Badge struct{ Name, Logo string }

// Badges answer each work's organisation in the first badge facet's role, for the works whose organisation has a logo.
func (w Works) Badges(ctx context.Context, facets []WorkFacet, workIDs []int64) (map[int64]Badge, error) {
	badges := map[int64]Badge{}
	at := slices.IndexFunc(facets, func(facet WorkFacet) bool { return facet.Badge && facet.Source == FacetOrganisation })
	if at < 0 || len(workIDs) == 0 {
		return badges, nil
	}
	ids, idArgs := idList(workIDs)
	query := "SELECT wo.work_id, o.name, CAST(COALESCE(json_extract(o.fields_json, '$.logo'), '') AS TEXT) FROM work_organisation wo" +
		" JOIN organisation o ON o.id = wo.organisation_id WHERE wo.work_id IN (" + ids + ") AND wo.role = ? ORDER BY wo.work_id, o.name"
	err := eachTriple(ctx, w.store.Read(), query, append(idArgs, facets[at].Name), func(id int64, name, logo string) {
		if _, have := badges[id]; !have && logo != "" {
			badges[id] = Badge{Name: name, Logo: logo}
		}
	})
	if err != nil {
		return nil, err
	}
	return badges, nil
}

func idList(ids []int64) (string, []any) {
	arguments := make([]any, len(ids))
	for i, id := range ids {
		arguments[i] = id
	}
	return marks(len(ids)), arguments
}

func marks(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

type rowReader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func eachRow(ctx context.Context, reader rowReader, query string, arguments []any, each func(int64, string)) error {
	rows, err := reader.QueryContext(ctx, query, arguments...)
	if err != nil {
		return fmt.Errorf("library: facets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var value string
		if err := rows.Scan(&id, &value); err != nil {
			return fmt.Errorf("library: facets: %w", err)
		}
		each(id, value)
	}
	return rows.Err()
}

func eachTriple(ctx context.Context, reader rowReader, query string, arguments []any, each func(int64, string, string)) error {
	rows, err := reader.QueryContext(ctx, query, arguments...)
	if err != nil {
		return fmt.Errorf("library: facets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var name, value string
		if err := rows.Scan(&id, &name, &value); err != nil {
			return fmt.Errorf("library: facets: %w", err)
		}
		each(id, name, value)
	}
	return rows.Err()
}
