package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

// ErrPersonNotFound reports a person with no credit in the library asked about.
var ErrPersonNotFound = errors.New("library: person not found")

// TVPersonFilter narrows a TV library's people list: roles (any when empty) and the start of the name.
type TVPersonFilter struct {
	CategoryID int64
	Roles      []string
	NamePrefix string
	// Monitored narrows to monitored (true) or unmonitored (false) people; nil is both.
	Monitored *bool
	// Sort is "name" (the default) or "series" (series credited); Desc turns either round.
	Sort string
	Desc bool
	// Where are the conditions of a custom filter, all of which must hold.
	Where []listfilter.Condition
}

// TVPeopleCursor is a position in the people list: the sort value (the sort name, or the series count) and the id.
type TVPeopleCursor struct {
	SortName string
	Count    int64
	ID       int64
	// Set is false for the first page.
	Set bool
}

// TVPersonFields are the fields a custom filter of the cast list may name.
var TVPersonFields = map[string]listfilter.Field{
	"name":      {Kind: listfilter.Text},
	"series":    {Kind: listfilter.Number},
	"monitored": {Kind: listfilter.Bool},
	"photo":     {Kind: listfilter.Bool},
	"role": {Kind: listfilter.Enum, Choices: []string{
		"actor", "guest_star", "creator", "showrunner", "director", "writer", "executive_producer", "producer", "composer",
	}},
}

// TVPersonRow is one person of a TV library with what they did there.
type TVPersonRow struct {
	ID          int64
	Name        string
	SortName    string
	ImageURL    string
	Monitored   bool
	Roles       []string
	SeriesCount int64
	// Character is the first part they played in this library, for the card's subtitle.
	Character string
}

// inLibrary restricts a credit alias to TV works the category holds; a work is visible by desired-item membership.
const inLibrary = `JOIN work w ON w.id = %[1]s.work_id AND w.media_type = 'tv'
      AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id AND d.category_id = ?)`

func roleFilter(alias string, roles []string) (string, []any) {
	if len(roles) == 0 {
		return "", nil
	}
	arguments := make([]any, len(roles))
	for i, role := range roles {
		arguments[i] = role
	}
	return " AND " + alias + ".role IN (" + strings.TrimSuffix(strings.Repeat("?,", len(roles)), ",") + ")", arguments
}

// People lists one page of the people credited on a TV library's series, by sort name with the id as the tie-break.
func (r SeriesReader) People(ctx context.Context, filter TVPersonFilter, after TVPeopleCursor, pageSize int) ([]TVPersonRow, error) {
	roleSQL, roleArgs := roleFilter("c", filter.Roles)
	join := fmt.Sprintf(inLibrary, "c")
	countExpression := "(SELECT COUNT(DISTINCT c.work_id) FROM credit c " + join + " WHERE c.person_id = p.id" + roleSQL + ")"
	countArgs := append([]any{filter.CategoryID}, roleArgs...)
	var predicates store.Predicates
	predicates.Add("EXISTS (SELECT 1 FROM credit c "+join+" WHERE c.person_id = p.id"+roleSQL+")",
		append([]any{filter.CategoryID}, roleArgs...)...)
	addTVNamePrefix(&predicates, filter.NamePrefix)
	if filter.Monitored != nil {
		predicates.Add("p.monitored = ?", boolInt(*filter.Monitored))
	}
	for _, condition := range filter.Where {
		fragment, arguments := r.personCondition(condition, filter.CategoryID, join, countExpression, countArgs)
		predicates.Add(fragment, arguments...)
	}
	var order string
	direction, compare := "", ">"
	if filter.Desc {
		direction, compare = " DESC", "<"
	}
	switch filter.Sort {
	case "series":
		order = "series_count" + direction + ", p.id" + direction
		if after.Set {
			predicates.Add("("+countExpression+" "+compare+" ? OR ("+countExpression+" = ? AND p.id "+compare+" ?))",
				append(append(append(slices.Clone(countArgs), after.Count), countArgs...), after.Count, after.ID)...)
		}
	default:
		order = "p.sort_name COLLATE NOCASE" + direction + ", p.id" + direction
		if after.Set {
			predicates.Add("(p.sort_name COLLATE NOCASE "+compare+" ? OR (p.sort_name COLLATE NOCASE = ? AND p.id "+compare+" ?))",
				after.SortName, after.SortName, after.ID)
		}
	}

	query := `SELECT p.id, p.name, p.sort_name, COALESCE(json_extract(p.fields_json, '$.image_url'), ''), p.monitored,
       CAST(` + countExpression + ` AS INTEGER) AS series_count,
       COALESCE((SELECT GROUP_CONCAT(DISTINCT c.role) FROM credit c ` + join + ` WHERE c.person_id = p.id), ''),
       COALESCE((SELECT c.character FROM credit c ` + join + ` WHERE c.person_id = p.id AND c.character != ''` + roleSQL + `
                 ORDER BY c.ord LIMIT 1), '')
FROM person p` + predicates.Where() + `
ORDER BY ` + order + `
LIMIT ?`
	arguments := []any{filter.CategoryID}
	arguments = append(arguments, roleArgs...)
	arguments = append(arguments, filter.CategoryID, filter.CategoryID)
	arguments = append(arguments, roleArgs...)
	arguments = append(arguments, predicates.Args(pageSize)...)

	rows, err := r.store.Read().QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("library: list tv people: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var people []TVPersonRow
	for rows.Next() {
		var row TVPersonRow
		var roles string
		var monitored int64
		if err := rows.Scan(&row.ID, &row.Name, &row.SortName, &row.ImageURL, &monitored, &row.SeriesCount, &roles, &row.Character); err != nil {
			return nil, fmt.Errorf("library: scan tv person: %w", err)
		}
		row.Monitored = monitored == 1
		row.Roles = strings.Split(roles, ",")
		slices.Sort(row.Roles)
		people = append(people, row)
	}
	return people, rows.Err()
}

// personCondition is one custom-filter condition as a predicate over a person p and the credits of the library.
func (r SeriesReader) personCondition(condition listfilter.Condition, categoryID int64, join, countExpression string, countArgs []any) (string, []any) {
	switch condition.Field {
	case "name":
		return condition.SQL(listfilter.Text, "p.name")
	case "monitored":
		return condition.SQL(listfilter.Bool, "p.monitored = 1")
	case "photo":
		return condition.SQL(listfilter.Bool, "COALESCE(json_extract(p.fields_json, '$.image_url'), '') <> ''")
	case "series":
		fragment, arguments := condition.SQL(listfilter.Number, countExpression)
		return fragment, append(slices.Clone(countArgs), arguments...)
	default:
		holds := "EXISTS (SELECT 1 FROM credit c " + join + " WHERE c.person_id = p.id AND c.role = ?)"
		if condition.Op == "is_not" {
			holds = "NOT " + holds
		}
		return holds, []any{categoryID, condition.Value}
	}
}

func addTVNamePrefix(predicates *store.Predicates, prefix string) {
	if prefix == "" {
		return
	}
	upper, bounded := prefixUpperBound(prefix)
	if !bounded {
		predicates.Add("p.sort_name COLLATE NOCASE >= ?", prefix)
		return
	}
	predicates.Add("(p.sort_name COLLATE NOCASE >= ? AND p.sort_name COLLATE NOCASE < ?)", prefix, upper)
}

// TVPersonCredit is what a person did on one series; Episodes lists the "s03:e04" parts of an episode-scoped role.
type TVPersonCredit struct {
	WorkID    int64
	Title     string
	Year      int64
	Role      string
	Character string
	Episodes  []string
}

// TVPersonDetail is one person with their credits in a library.
type TVPersonDetail struct {
	ID        int64
	Name      string
	Born      string
	Died      string
	Country   string
	ImageURL  string
	Biography string
	Monitored bool
	// IDs maps provider to the person's id there ("tmdb", "imdb").
	IDs     map[string]string
	Credits []TVPersonCredit
}

// Person answers one person's page; ErrPersonNotFound when they have no credit in the category.
func (r SeriesReader) Person(ctx context.Context, categoryID, personID int64) (TVPersonDetail, error) {
	queries := librarydb.New(r.store.Read())
	person, err := queries.GetPerson(ctx, personID)
	if errors.Is(err, sql.ErrNoRows) {
		return TVPersonDetail{}, ErrPersonNotFound
	}
	if err != nil {
		return TVPersonDetail{}, fmt.Errorf("library: tv person: %w", err)
	}
	join := fmt.Sprintf(inLibrary, "c")
	rows, err := r.store.Read().QueryContext(ctx,
		`SELECT c.work_id, w.canonical_title, COALESCE(w.year, 0), c.role, c.character, COALESCE(p.media_ref, '')
FROM credit c `+join+`
LEFT JOIN work_part p ON p.id = c.part_id AND c.part_id != 0
WHERE c.person_id = ?
ORDER BY w.sort_title, w.id, c.role, c.character, p.sort_key`, categoryID, personID)
	if err != nil {
		return TVPersonDetail{}, fmt.Errorf("library: tv person credits: %w", err)
	}
	defer func() { _ = rows.Close() }()
	detail := TVPersonDetail{
		ID: person.ID, Name: person.Name, Born: person.Born.String, Died: person.Died.String, Country: person.Country.String,
		Monitored: person.Monitored == 1, Biography: biographyFromFields(person.FieldsJson), ImageURL: imageFromFields(person.FieldsJson),
	}
	for rows.Next() {
		var credit TVPersonCredit
		var ref string
		if err := rows.Scan(&credit.WorkID, &credit.Title, &credit.Year, &credit.Role, &credit.Character, &ref); err != nil {
			return TVPersonDetail{}, fmt.Errorf("library: scan tv person credit: %w", err)
		}
		last := len(detail.Credits) - 1
		if last >= 0 && detail.Credits[last].WorkID == credit.WorkID && detail.Credits[last].Role == credit.Role && detail.Credits[last].Character == credit.Character {
			if ref != "" {
				detail.Credits[last].Episodes = append(detail.Credits[last].Episodes, ref)
			}
			continue
		}
		if ref != "" {
			credit.Episodes = []string{ref}
		}
		detail.Credits = append(detail.Credits, credit)
	}
	if err := rows.Err(); err != nil {
		return TVPersonDetail{}, fmt.Errorf("library: tv person credits: %w", err)
	}
	if len(detail.Credits) == 0 {
		return TVPersonDetail{}, ErrPersonNotFound
	}
	externals, err := queries.ListPersonExternalIDs(ctx, personID)
	if err != nil {
		return TVPersonDetail{}, fmt.Errorf("library: tv person ids: %w", err)
	}
	detail.IDs = make(map[string]string, len(externals))
	for _, external := range externals {
		detail.IDs[external.Provider] = external.ExternalID
	}
	return detail, nil
}

// PersonOtherWork is a work of another media type the person is credited on, found through the shared person.
type PersonOtherWork struct {
	WorkID    int64
	MediaType string
	Title     string
	Year      int64
	Role      string
}

// maxOtherWorks bounds the works listed from other libraries.
const maxOtherWorks = 100

// OtherWorks lists what the person did outside TV: the albums of a music artist who is also an actor.
func (r SeriesReader) OtherWorks(ctx context.Context, personID int64) ([]PersonOtherWork, error) {
	rows, err := r.store.Read().QueryContext(ctx,
		`SELECT DISTINCT w.id, w.media_type, w.canonical_title, COALESCE(w.year, 0), c.role
FROM credit c JOIN work w ON w.id = c.work_id
WHERE c.person_id = ? AND c.edition_id = 0 AND c.part_id = 0 AND w.media_type <> 'tv'
ORDER BY w.media_type, COALESCE(w.year, 0) DESC, w.id LIMIT ?`, personID, maxOtherWorks)
	if err != nil {
		return nil, fmt.Errorf("library: person other works: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var works []PersonOtherWork
	for rows.Next() {
		var work PersonOtherWork
		if err := rows.Scan(&work.WorkID, &work.MediaType, &work.Title, &work.Year, &work.Role); err != nil {
			return nil, fmt.Errorf("library: scan person other work: %w", err)
		}
		works = append(works, work)
	}
	return works, rows.Err()
}

func imageFromFields(raw sql.NullString) string {
	if !raw.Valid {
		return ""
	}
	var fields personFields
	if json.Unmarshal([]byte(raw.String), &fields) != nil {
		return ""
	}
	return fields.ImageURL
}

// TVNetworkRow is a network or streamer with how many of the library's series air on it.
type TVNetworkRow struct {
	ID          int64
	Name        string
	Country     string
	LogoURL     string
	SeriesCount int64
}

// Networks lists the networks of a TV library's series, most series first.
func (r SeriesReader) Networks(ctx context.Context, categoryID int64) ([]TVNetworkRow, error) {
	rows, err := r.store.Read().QueryContext(ctx,
		`SELECT o.id, o.name, COALESCE(o.country, ''), COALESCE(json_extract(o.fields_json, '$.logo'), ''), COUNT(DISTINCT w.id)
FROM organisation o
JOIN work_organisation wo ON wo.organisation_id = o.id AND wo.role = 'network'
JOIN work w ON w.id = wo.work_id AND w.media_type = 'tv'
 AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id AND d.category_id = ?)
WHERE o.kind = 'network'
GROUP BY o.id
ORDER BY COUNT(DISTINCT w.id) DESC, o.name COLLATE NOCASE, o.id`, categoryID)
	if err != nil {
		return nil, fmt.Errorf("library: tv networks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var networks []TVNetworkRow
	for rows.Next() {
		var row TVNetworkRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Country, &row.LogoURL, &row.SeriesCount); err != nil {
			return nil, fmt.Errorf("library: scan tv network: %w", err)
		}
		networks = append(networks, row)
	}
	return networks, rows.Err()
}

// NetworkSeries answers one network and the ids of the library's series on it; ErrPersonNotFound-style
// ErrNetworkNotFound when the library has none there.
func (r SeriesReader) NetworkSeries(ctx context.Context, categoryID, networkID int64) (TVNetworkRow, []int64, error) {
	networks, err := r.Networks(ctx, categoryID)
	if err != nil {
		return TVNetworkRow{}, nil, err
	}
	index := slices.IndexFunc(networks, func(n TVNetworkRow) bool { return n.ID == networkID })
	if index < 0 {
		return TVNetworkRow{}, nil, ErrNetworkNotFound
	}
	rows, err := r.store.Read().QueryContext(ctx,
		`SELECT w.id FROM work_organisation wo JOIN work w ON w.id = wo.work_id AND w.media_type = 'tv'
 AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id AND d.category_id = ?)
WHERE wo.organisation_id = ? AND wo.role = 'network' ORDER BY w.sort_title, w.id`, categoryID, networkID)
	if err != nil {
		return TVNetworkRow{}, nil, fmt.Errorf("library: network series: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return TVNetworkRow{}, nil, fmt.Errorf("library: scan network series: %w", err)
		}
		ids = append(ids, id)
	}
	return networks[index], ids, rows.Err()
}

// ErrNetworkNotFound reports a network none of the library's series air on.
var ErrNetworkNotFound = errors.New("library: network not found in this library")

// SummariesByIDs lists the given series of a category as a list shows them, in sort-title order.
func (r SeriesReader) SummariesByIDs(ctx context.Context, categoryID int64, ids []int64, now time.Time) ([]SeriesSummary, error) {
	queries := librarydb.New(r.store.Read())
	var summaries []SeriesSummary
	for _, id := range ids {
		row, err := queries.GetSeriesInCategory(ctx, librarydb.GetSeriesInCategoryParams{ID: id, CategoryID: categoryID})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("library: series %d: %w", id, err)
		}
		summaries = append(summaries, SeriesSummary{
			WorkID: row.ID, Title: row.CanonicalTitle, SortTitle: row.SortTitle, Year: row.Year.Int64,
			Description: row.Description.String, AddedAt: row.AddedAt,
		})
	}
	kept := make([]int64, len(summaries))
	for i, summary := range summaries {
		kept[i] = summary.WorkID
	}
	if err := r.fill(ctx, queries, summaries, kept, now); err != nil {
		return nil, err
	}
	return summaries, nil
}

// NetworkOfSeries answers the network a series airs on, with its logo; found is false for a series with none.
func (r SeriesReader) NetworkOfSeries(ctx context.Context, workID int64) (TVNetworkRow, bool, error) {
	var row TVNetworkRow
	err := r.store.Read().QueryRowContext(ctx,
		`SELECT o.id, o.name, COALESCE(o.country, ''), COALESCE(json_extract(o.fields_json, '$.logo'), '')
FROM work_organisation wo JOIN organisation o ON o.id = wo.organisation_id
WHERE wo.work_id = ? AND wo.role = 'network' AND o.kind = 'network'
ORDER BY o.id LIMIT 1`, workID).Scan(&row.ID, &row.Name, &row.Country, &row.LogoURL)
	if errors.Is(err, sql.ErrNoRows) {
		return TVNetworkRow{}, false, nil
	}
	if err != nil {
		return TVNetworkRow{}, false, fmt.Errorf("library: series network: %w", err)
	}
	return row, true, nil
}
