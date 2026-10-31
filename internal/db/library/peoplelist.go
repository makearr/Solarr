package library

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

// PersonFields are the fields a custom filter of a people list may name, role among the given ones.
func PersonFields(roles []string) map[string]listfilter.Field {
	return map[string]listfilter.Field{
		"name":         {Kind: listfilter.Text},
		"country":      {Kind: listfilter.Text},
		"gender":       {Kind: listfilter.Text},
		"nationality":  {Kind: listfilter.Text},
		"height":       {Kind: listfilter.Number},
		"weight":       {Kind: listfilter.Number},
		"measurements": {Kind: listfilter.Text},
		"hair_colour":  {Kind: listfilter.Text},
		"ethnicity":    {Kind: listfilter.Text},
		"status":       {Kind: listfilter.Enum, Choices: PersonStatuses},
		"tattoos":      {Kind: listfilter.Text},
		"piercings":    {Kind: listfilter.Text},
		"monitored":    {Kind: listfilter.Bool},
		"photo":        {Kind: listfilter.Bool},
		"works":        {Kind: listfilter.Number},
		"wanted":       {Kind: listfilter.Number},
		"complete":     {Kind: listfilter.Number},
		"role":         {Kind: listfilter.Enum, Choices: roles},
	}
}

// People sort keys.
const (
	PeopleByName   = "name"
	PeopleByWorks  = "works"
	PeopleByLatest = "latest"
	PeopleByAdded  = "added"
)

// PeopleFilter is what the one people list narrows on, for every media type and for all libraries at once.
type PeopleFilter struct {
	// Visible are the libraries the viewer may read; nil is everyone, for a caller with no viewer.
	Visible []int64
	// Category narrows to one library; 0 is every visible one.
	Category int64
	// Roles narrow to people credited in any of them; empty is any role.
	Roles     []string
	Monitored *bool
	// NameContains narrows to people whose name contains it, any case.
	NameContains string
	Where        []listfilter.Condition
	Sort         string
	Desc         bool
}

// PeopleCursor is a position in the list: the sort value (the name, or a number) and the id that breaks ties.
type PeopleCursor struct {
	Name  string
	Value int64
	ID    int64
	// Set is false for the first page.
	Set bool
}

// PersonEntry is one person of the list with what they did in the libraries it covers.
type PersonEntry struct {
	ID             int64
	Name           string
	SortName       string
	Disambiguation string
	Kind           string
	Country        string
	Born           string
	Died           string
	ImageURL       string
	Monitored      bool
	AddedAt        int64
	Roles          []string
	Works          int64
	Latest         int64
	// Character is the first part they played there, for a card's subtitle.
	Character string
	// Wanted and Complete count their monitored works there and the complete ones; Downloading is one in flight.
	Wanted      int64
	Complete    int64
	Downloading bool
	// NoArtwork says no picture is cached for them, so a grid need not ask for one.
	NoArtwork bool
}

// sqlPart is a fragment of SQL with its positional arguments.
type sqlPart struct {
	sql  string
	args []any
}

// peopleScope is the libraries a people list covers: the visible ones, narrowed to one when asked; nil is every one.
func peopleScope(filter PeopleFilter) []int64 {
	if filter.Category == 0 {
		return filter.Visible
	}
	if filter.Visible != nil && !slices.Contains(filter.Visible, filter.Category) {
		return []int64{}
	}
	return []int64{filter.Category}
}

// inScope renders "column IN (scope)", nothing for every library, and a false condition for none.
func inScope(column string, scope []int64) sqlPart {
	fragment, arguments, applies := membershipFragment(column+" IN (%s)", scope)
	if !applies {
		return sqlPart{sql: "1 = 1"}
	}
	return sqlPart{sql: fragment, args: arguments}
}

func placeholders(values []string) (string, []any) {
	arguments := make([]any, len(values))
	for i, value := range values {
		arguments[i] = value
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(values)), ","), arguments
}

// rollupRoles narrows person_library rows to the asked roles, or to the any-role row.
func rollupRoles(alias string, roles []string) sqlPart {
	if len(roles) == 0 {
		return sqlPart{sql: alias + ".role = ''"}
	}
	marks, arguments := placeholders(roles)
	return sqlPart{sql: alias + ".role IN (" + marks + ")", args: arguments}
}

// peopleQuery holds the expressions every part of the list is built from.
type peopleQuery struct {
	scope  []int64
	roles  []string
	member sqlPart
	works  sqlPart
	latest sqlPart
}

func newPeopleQuery(filter PeopleFilter) peopleQuery {
	scope := peopleScope(filter)
	category := inScope("pl.category_id", scope)
	roles := rollupRoles("pl", filter.Roles)
	member := sqlPart{
		sql:  "EXISTS (SELECT 1 FROM person_library pl WHERE pl.person_id = p.id AND " + roles.sql + " AND " + category.sql + ")",
		args: append(slices.Clone(roles.args), category.args...),
	}
	// Someone followed into a library belongs there before any work credits them.
	followed := inScope("s.category_id", scope)
	member.sql = "(" + member.sql + " OR (p.monitored = 1 AND EXISTS (SELECT 1 FROM subscription s WHERE s.subject_type = 'person'" +
		" AND s.subject_id = p.id AND s.enabled = 1 AND " + followed.sql + ")))"
	member.args = append(member.args, followed.args...)
	rows := " FROM person_library pl WHERE pl.person_id = p.id AND " + roles.sql + " AND " + category.sql
	rowArgs := append(slices.Clone(roles.args), category.args...)
	query := peopleQuery{
		scope: scope, roles: filter.Roles, member: member,
		works:  sqlPart{sql: "CAST(COALESCE((SELECT SUM(pl.works)" + rows + "), 0) AS INTEGER)", args: rowArgs},
		latest: sqlPart{sql: "CAST(COALESCE((SELECT MAX(pl.latest)" + rows + "), 0) AS INTEGER)", args: rowArgs},
	}
	if len(filter.Roles) > 1 {
		// A person in two of the asked roles on one work counts it once.
		marks, roleArgs := placeholders(filter.Roles)
		wanted := inScope("d.category_id", scope)
		query.works = sqlPart{
			sql: "CAST((SELECT COUNT(DISTINCT c.work_id) FROM credit c WHERE c.person_id = p.id AND c.role IN (" + marks + ")" +
				" AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = c.work_id AND " + wanted.sql + ")) AS INTEGER)",
			args: append(roleArgs, wanted.args...),
		}
	}
	return query
}

// coverage counts the person's wanted works in scope and the complete ones, from their monitored SET items.
func (q peopleQuery) coverage(complete bool) sqlPart {
	scope := inScope("di.category_id", q.scope)
	state := ""
	if complete {
		state = " AND di.state IN ('COMPLETE', 'UPGRADABLE')"
	}
	return sqlPart{
		sql: "CAST((SELECT COUNT(DISTINCT di.work_id) FROM credit c JOIN desired_item di ON di.work_id = c.work_id AND di.monitored = 1" +
			" AND di.satisfaction_kind = 'SET' AND " + scope.sql + " WHERE c.person_id = p.id" + state + ") AS INTEGER)",
		args: scope.args,
	}
}

func (q peopleQuery) inScopeCredit(alias string) sqlPart {
	scope := inScope("d.category_id", q.scope)
	return sqlPart{sql: "EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = " + alias + ".work_id AND " + scope.sql + ")", args: scope.args}
}

// condition is one custom-filter condition as a predicate over the person p.
func (q peopleQuery) condition(condition listfilter.Condition) sqlPart {
	switch condition.Field {
	case "name":
		fragment, arguments := condition.SQL(listfilter.Text, "p.name")
		return sqlPart{fragment, arguments}
	case "country":
		fragment, arguments := condition.SQL(listfilter.Text, "COALESCE(p.country, '')")
		return sqlPart{fragment, arguments}
	case "gender":
		fragment, arguments := condition.SQL(listfilter.Text, "COALESCE(p.gender, '')")
		return sqlPart{fragment, arguments}
	case "height", "weight":
		fragment, arguments := condition.SQL(listfilter.Number, "COALESCE("+personAttribute(condition.Field+"_"+map[string]string{"height": "cm", "weight": "kg"}[condition.Field])+", 0)")
		return sqlPart{fragment, arguments}
	case "nationality":
		fragment, arguments := condition.SQL(listfilter.Text, "COALESCE("+personAttribute("nationality")+", p.country, '')")
		return sqlPart{fragment, arguments}
	case "measurements", "ethnicity", "tattoos", "piercings", "hair_colour":
		key := map[string]string{"hair_colour": "hair_color"}[condition.Field]
		if key == "" {
			key = condition.Field
		}
		fragment, arguments := condition.SQL(listfilter.Text, "COALESCE("+personAttribute(key)+", '')")
		return sqlPart{fragment, arguments}
	case "status":
		fragment, arguments := condition.SQL(listfilter.Enum, "COALESCE("+personAttribute("status")+", CASE WHEN COALESCE(p.died, '') <> '' THEN 'deceased' ELSE 'active' END)")
		return sqlPart{fragment, arguments}
	case "monitored":
		fragment, arguments := condition.SQL(listfilter.Bool, "p.monitored = 1")
		return sqlPart{fragment, arguments}
	case "photo":
		fragment, arguments := condition.SQL(listfilter.Bool,
			"(COALESCE(json_extract(p.fields_json, '$.image_url'), '') <> '' OR EXISTS (SELECT 1 FROM artwork a WHERE a.subject_type = 'person' AND a.subject_id = p.id AND a.cache_key <> ''))")
		return sqlPart{fragment, arguments}
	case "works", "wanted", "complete":
		expression := q.works
		if condition.Field != "works" {
			expression = q.coverage(condition.Field == "complete")
		}
		fragment, arguments := condition.SQL(listfilter.Number, expression.sql)
		return sqlPart{fragment, append(slices.Clone(expression.args), arguments...)}
	default:
		scope := inScope("pl.category_id", q.scope)
		holds := "EXISTS (SELECT 1 FROM person_library pl WHERE pl.person_id = p.id AND pl.role = ? AND " + scope.sql + ")"
		if condition.Op == "is_not" {
			holds = "NOT " + holds
		}
		return sqlPart{holds, append([]any{condition.Value}, scope.args...)}
	}
}

// composePeople assembles one page: driven by person's own sort-name index, each row checked against the rollup.
func composePeople(filter PeopleFilter, after PeopleCursor, pageSize int) (string, []any) {
	query := newPeopleQuery(filter)
	var predicates store.Predicates
	predicates.AddIf(filter.Visible != nil || filter.Category != 0 || len(filter.Roles) > 0, query.member.sql, query.member.args...)
	if filter.Monitored != nil {
		predicates.Add("p.monitored = ?", boolInt(*filter.Monitored))
	}
	if filter.NameContains != "" {
		like := containsPattern(filter.NameContains)
		predicates.Add("(LOWER(p.name) LIKE ? ESCAPE '\\' OR LOWER(p.sort_name) LIKE ? ESCAPE '\\')", like, like)
	}
	for _, condition := range filter.Where {
		part := query.condition(condition)
		predicates.Add(part.sql, part.args...)
	}

	direction, compare := "", ">"
	if filter.Desc {
		direction, compare = " DESC", "<"
	}
	var key sqlPart
	switch filter.Sort {
	case PeopleByWorks:
		key = query.works
	case PeopleByLatest:
		key = query.latest
	case PeopleByAdded:
		key = sqlPart{sql: "p.added_at"}
	default:
		key = sqlPart{sql: "p.sort_name COLLATE NOCASE"}
	}
	order := key.sql + direction + ", p.id" + direction
	if after.Set {
		var value any = after.Value
		if key.sql == "p.sort_name COLLATE NOCASE" {
			value = after.Name
		}
		predicates.Add("("+key.sql+" "+compare+" ? OR ("+key.sql+" = ? AND p.id "+compare+" ?))",
			append(append(append(append(slices.Clone(key.args), value), key.args...), value), after.ID)...)
	}

	roleScope := inScope("pl.category_id", query.scope)
	character := query.inScopeCredit("c")
	wanted, complete := query.coverage(false), query.coverage(true)
	downloadScope := inScope("di.category_id", query.scope)
	columns := []sqlPart{
		{sql: "p.id, p.name, p.sort_name, COALESCE(p.disambiguation, ''), COALESCE(p.kind, ''), COALESCE(p.country, ''), " +
			"COALESCE(p.born, ''), COALESCE(p.died, ''), COALESCE(json_extract(p.fields_json, '$.image_url'), ''), p.monitored, p.added_at"},
		{sql: "COALESCE((SELECT GROUP_CONCAT(DISTINCT pl.role) FROM person_library pl WHERE pl.person_id = p.id AND pl.role <> '' AND " +
			roleScope.sql + "), '')", args: roleScope.args},
		query.works,
		query.latest,
		{sql: "COALESCE((SELECT c.character FROM credit c WHERE c.person_id = p.id AND c.character <> '' AND " + character.sql +
			" ORDER BY c.ord LIMIT 1), '')", args: character.args},
		wanted,
		complete,
		{sql: "EXISTS (SELECT 1 FROM credit c JOIN desired_item di ON di.work_id = c.work_id AND " + downloadScope.sql +
			" JOIN acquisition a ON a.desired_item_id = di.id WHERE c.person_id = p.id" +
			" AND a.state NOT IN ('IMPORTED', 'CLEANED', 'FAILED', 'CANCELLED'))", args: downloadScope.args},
		{sql: "NOT EXISTS (SELECT 1 FROM artwork aw WHERE aw.subject_type = 'person' AND aw.subject_id = p.id AND aw.cache_key <> '')"},
	}
	var selectList []string
	var arguments []any
	for _, column := range columns {
		selectList = append(selectList, column.sql)
		arguments = append(arguments, column.args...)
	}
	sql := "SELECT " + strings.Join(selectList, ",\n       ") + "\nFROM person p" + predicates.Where() +
		"\nORDER BY " + order + "\nLIMIT ?"
	return sql, append(arguments, predicates.Args(append(slices.Clone(key.args), pageSize)...)...)
}

// List answers one page of people: of one library, or of every library the viewer may see.
func (p People) List(ctx context.Context, filter PeopleFilter, after PeopleCursor, pageSize int) ([]PersonEntry, error) {
	query, arguments := composePeople(filter, after, pageSize)
	rows, err := p.store.Read().QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("library: list people: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var people []PersonEntry
	for rows.Next() {
		var entry PersonEntry
		var monitored int64
		var roles string
		if err := rows.Scan(&entry.ID, &entry.Name, &entry.SortName, &entry.Disambiguation, &entry.Kind, &entry.Country, &entry.Born, &entry.Died, &entry.ImageURL, &monitored, &entry.AddedAt,
			&roles, &entry.Works, &entry.Latest, &entry.Character, &entry.Wanted, &entry.Complete, &entry.Downloading, &entry.NoArtwork); err != nil {
			return nil, fmt.Errorf("library: scan person: %w", err)
		}
		entry.Monitored = monitored == 1
		if roles != "" {
			entry.Roles = strings.Split(roles, ",")
			slices.Sort(entry.Roles)
		}
		people = append(people, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: list people: %w", err)
	}
	return people, nil
}

// ExplainList answers SQLite's plan for the query List would run, so a test can assert it reaches an index.
func (p People) ExplainList(ctx context.Context, filter PeopleFilter, after PeopleCursor, pageSize int) (string, error) {
	query, arguments := composePeople(filter, after, pageSize)
	return p.store.ExplainQueryPlan(ctx, query, arguments...)
}

// visiblePerson is the rule a person exists by for a viewer: a library they may read credits or follows them.
func visiblePerson(personIDExpr string, visible []int64) sqlPart {
	if visible == nil {
		return sqlPart{sql: "1 = 1"}
	}
	member := inScope("pl.category_id", visible)
	followed := inScope("s.category_id", visible)
	return sqlPart{
		sql: "(EXISTS (SELECT 1 FROM person_library pl WHERE pl.person_id = " + personIDExpr + " AND " + member.sql + ")" +
			" OR EXISTS (SELECT 1 FROM subscription s WHERE s.subject_type = 'person' AND s.subject_id = " + personIDExpr +
			" AND s.enabled = 1 AND " + followed.sql + "))",
		args: append(slices.Clone(member.args), followed.args...),
	}
}

// personAttribute is the SQL for one attribute of a person: what they set by hand, else what the provider says; NULL when neither.
func personAttribute(key string) string {
	return "COALESCE(NULLIF(json_extract(p.fields_json, '$.attributes_hand." + key + "'), ''), NULLIF(json_extract(p.fields_json, '$.attributes." + key + "'), ''))"
}
