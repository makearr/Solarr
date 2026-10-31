package library

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

// workColumns is the sqlc model's field order, which the scan below depends on
// (documentation/sql-and-sqlc.md). Qualified with "work." -- person (joined in
// for workArtistColumn) also has id and added_at columns, which would
// otherwise be ambiguous.
const workColumns = `work.id, work.media_type, work.canonical_title, work.sort_title, work.original_language,
       work.year, work.runtime_ms, work.added_at, work.updated_at, work.added_by`

// workArtistColumn reads work.lead_person_id (migration 0021) via a plain
// LEFT JOIN -- a correlated subquery here used to force SQLite to sort the
// whole result regardless of index (ADR-28; see notes.md for the story).
const workArtistColumn = `lead_person.name AS artist`

// workArtistJoin is workArtistColumn's join, appended to every list query's
// FROM clause. "lead_person" avoids colliding with any other query in this
// file that might alias person differently.
const workArtistJoin = `
LEFT JOIN person lead_person ON lead_person.id = work.lead_person_id`

// narratorsColumn lists a work's narrators in credit order, empty for anything but an audiobook.
const narratorsColumn = `CAST(COALESCE((SELECT GROUP_CONCAT(name, ', ') FROM (SELECT DISTINCT pe.name AS name FROM credit cr
      JOIN person pe ON pe.id = cr.person_id WHERE cr.work_id = work.id AND cr.role = 'narrator' ORDER BY cr.ord, pe.id)), '') AS TEXT) AS narrators`

// workStateColumn is the work's album-level state: its monitored SET item first, a track's own
// SINGLE only when there is no SET -- the oldest item was once taken, so a track wanted before
// its album made a complete album read MISSING. A scalar subquery keeps one row per work.
const workStateColumn = `(SELECT di.state FROM desired_item di
      WHERE di.work_id = work.id AND di.monitored = 1
      ORDER BY di.satisfaction_kind = 'SET' DESC, di.id LIMIT 1) AS wanted_state`

// WorkSort selects which column the list is ordered by. The zero value sorts
// by title, matching every caller that does not set it.
type WorkSort string

// The columns the list can be ordered by.
const (
	WorkSortTitle WorkSort = ""
	WorkSortYear  WorkSort = "year"
	WorkSortAdded WorkSort = "added"
	// WorkSortArtist orders by primary artist, then title, which is what the
	// Albums view groups by (asked for 2026-09-17).
	WorkSortArtist WorkSort = "artist"
	// WorkSortSearched orders by when the album was last searched, never-searched first.
	WorkSortSearched WorkSort = "searched"
	// WorkSortImported orders by when the newest file arrived, and lists only works with files.
	WorkSortImported WorkSort = "imported"
	// The Radarr-style columns (M1g 1.4): size on disk, quality profile, status, genre and tag.
	WorkSortSize    WorkSort = "size"
	WorkSortProfile WorkSort = "profile"
	WorkSortStatus  WorkSort = "status"
	WorkSortGenre   WorkSort = "genre"
	WorkSortTag     WorkSort = "tag"
	// WorkSortFacet orders by WorkFilter.SortFacet, one of the type's own facets (a series' network).
	WorkSortFacet WorkSort = "facet"
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
	// MediaTypes keeps only works of these types; nil any.
	MediaTypes []string
	// PersonID keeps only works the person is credited on; 0 any.
	PersonID int64
	Language string
	YearFrom int64
	YearTo   int64
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
	// FreshRSSSince is ADR-55's other window as a Unix time: a work an RSS feed showed a release for since then is
	// fresh too. Read only when FreshSince is set.
	FreshRSSSince int64
	// Sort and Direction pick the list's order (section 21.2's configurable,
	// sortable columns).
	Sort      WorkSort
	Direction SortDirection
	// WantedOnly restricts to works with a monitored desired_item that is
	// not yet COMPLETE -- todo 4.13's follow-up "Wanted" per-library screen,
	// asked for 2026-09-11. Reuses this list's own pagination/sort/visibility
	// rather than a second query.
	WantedOnly bool
	// CutoffUnmetOnly restricts to works whose monitored album falls short of its cutoff (M1d, Lidarr's
	// Wanted -> Cutoff Unmet).
	CutoffUnmetOnly bool
	// TagIDs are tags a work must carry, each or one below it; ExcludedTagIDs tags it must carry neither of.
	TagIDs, ExcludedTagIDs []int64
	// AboveRating keeps only the works a library's maximum rating does not admit; nil does not filter.
	AboveRating *RatingLimit
	// Where are the conditions of a custom filter, all of which must hold.
	Where []listfilter.Condition
	// Facets are the media type's facets a condition of Where may name.
	Facets []WorkFacet
	// SortFacet is the facet WorkSortFacet orders by.
	SortFacet WorkFacet
}

// WorkCursor is a position in the list: the id always breaks ties, since without it a
// page boundary inside a group sharing a sort value drops rows. Only the field matching
// the active WorkSort is read; the others are ignored.
type WorkCursor struct {
	SortTitle string
	SortYear  int64
	SortAdded int64
	// SortArtist is read, with SortTitle, only by WorkSortArtist.
	SortArtist   string
	SortSearched int64
	SortImported int64
	// SortNumber is read by the size and profile sorts, SortText by status, genre and tag.
	SortNumber int64
	SortText   string
	ID         int64
}

// WorkListRow is one row of the library list plus its primary artist credit
// and, when it has a monitored desired_item, that item's own state.
type WorkListRow struct {
	librarydb.Work
	Artist      sql.NullString
	WantedState sql.NullString
	// ArtistSortName is the primary artist's sort name, empty with none: what
	// WorkSortArtist orders on, and so what its next-page cursor carries.
	ArtistSortName string
	// PresentCount, RequiredCount, AvailableFrom and Downloading are what the
	// availability bar under a cover is drawn from (2026-09-19).
	PresentCount  sql.NullInt64
	RequiredCount sql.NullInt64
	AvailableFrom sql.NullInt64
	Downloading   bool
	// OnDisk reports a root folder link, so an album nothing wants but whose
	// files are present is not coloured as missing.
	OnDisk bool
	// LastSearchedAt is when any of the work's desired items was last searched; 0 is never.
	LastSearchedAt int64
	// Unverified is an album known only by its own tags or folder: every id it has is local (D6).
	Unverified bool
	// ArrivedAt is when the work's newest file arrived; 0 with none.
	ArrivedAt int64
	// SortNumber and SortText are the value the size/profile and status/genre/tag sorts ordered this row by.
	SortNumber int64
	SortText   string
	// ArtistCover is an album with no cover of its own, shown by its primary artist's image instead.
	ArtistCover bool
	// NoArtwork says neither the work nor its lead has a cached picture, so a grid need not ask for one.
	NoArtwork bool
	// Monitored, Description and SizeBytes are what Radarr's poster, overview and table show (2026-09-30).
	Monitored   bool
	Description string
	SizeBytes   int64
	// PresentTotal, RequiredTotal and Upgradable sum the work's monitored SET items: one album, or every season.
	PresentTotal  int64
	RequiredTotal int64
	Upgradable    int64
	// Narrators are the audiobook readers, comma-joined in credit order; empty for any other work.
	Narrators string
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
	if filter.MediaTypes != nil {
		fragment, arguments, _ := membershipStrings("media_type IN (%s)", filter.MediaTypes)
		predicates.Add(fragment, arguments...)
	}
	predicates.AddIf(filter.PersonID != 0, "EXISTS (SELECT 1 FROM credit pc WHERE pc.work_id = work.id AND pc.person_id = ?)", filter.PersonID)
	predicates.AddIf(filter.Language != "", "original_language = ?", filter.Language)
	predicates.AddIf(filter.YearFrom != 0, "year >= ?", filter.YearFrom)
	predicates.AddIf(filter.YearTo != 0, "year <= ?", filter.YearTo)
	addPrefix(&predicates, filter.TitlePrefix)
	addVisibility(&predicates, filter.RootFolderIDs, filter.CategoryIDs)
	addWorkConditions(&predicates, addFacetConditions(&predicates, filter.Where, filter.Facets))
	for _, id := range filter.TagIDs {
		predicates.Add("EXISTS "+workTagTree, id)
	}
	for _, id := range filter.ExcludedTagIDs {
		predicates.Add("NOT EXISTS "+workTagTree, id)
	}
	if limit := filter.AboveRating; limit != nil {
		predicates.Add(aboveRatingSQL, boolToInt(limit.AllowUnrated), limit.MaxAge)
	}
	predicates.AddIf(filter.FreshSince != "",
		"(EXISTS (SELECT 1 FROM work_date wd WHERE wd.work_id = work.id AND wd.kind = ? AND wd.date >= ?)"+
			" OR EXISTS (SELECT 1 FROM candidate_decision fcd JOIN candidate fc ON fc.id = fcd.candidate_id"+
			" JOIN desired_item fdi ON fdi.id = fcd.desired_item_id"+
			" WHERE fdi.work_id = work.id AND fc.source = 'rss' AND fc.first_seen_at >= ?))",
		WorkDateStreet, filter.FreshSince, filter.FreshRSSSince)
	// A work with no tracks yet is waiting for metadata, not missing: Lidarr never calls an album
	// missing before it knows its tracks (answered 2026-09-28).
	predicates.AddIf(filter.WantedOnly,
		"EXISTS (SELECT 1 FROM desired_item di WHERE di.work_id = work.id AND di.monitored = 1 AND di.state IN ('MISSING', 'PARTIAL'))"+
			" AND EXISTS (SELECT 1 FROM work_part tracklist WHERE tracklist.work_id = work.id)")
	predicates.AddIf(filter.CutoffUnmetOnly,
		"EXISTS (SELECT 1 FROM desired_item cu WHERE cu.work_id = work.id AND cu.monitored = 1 AND cu.state = 'UPGRADABLE')")

	operator := ">"
	orderDirection := ""
	if filter.Direction == SortDescending {
		operator = "<"
		orderDirection = " DESC"
	}

	// The id tie-break follows the sort's own direction (changed 2026-09-18).
	// It used to advance ascending whatever the sort did, which was consistent
	// with the OR-form cursor it was paired with. Once the cursor became a row
	// value it stopped being optional: a row value compares the whole tuple in
	// one direction, so an ascending tie-break under a descending sort makes
	// the WHERE and the ORDER BY disagree and pagination silently drops rows
	// inside a tie group -- 230 of 250 in the test that pins it. The speed is
	// the row value's doing (notes.md 8.17); this is the correctness half.
	// Every cursor compares as a row value, which SQLite answers with an index
	// seek. The OR form this replaced could not be seeked at all.
	var orderBy string
	join := workArtistJoin
	numberColumn, textColumn := "0", "''"
	switch filter.Sort {
	case WorkSortImported:
		// Joined, not a subquery, so the order walks work_arrival_at instead of sorting every work.
		join += "\nJOIN work_arrival arrival ON arrival.work_id = work.id"
		predicates.AddIf(after.ID != 0,
			"(arrival.arrived_at, arrival.work_id) "+operator+" (?, ?)", after.SortImported, after.ID)
		orderBy = "arrival.arrived_at" + orderDirection + ", arrival.work_id" + orderDirection
	case WorkSortArtist:
		predicates.AddIf(after.ID != 0,
			"("+leadSortExpression+", work.sort_title, work.id) "+operator+" ("+leadSortCursor+", ?, ?)",
			after.SortArtist, after.SortTitle, after.ID)
		orderBy = leadSortExpression + orderDirection + ", work.sort_title" + orderDirection +
			", work.id" + orderDirection
	case WorkSortSize, WorkSortProfile:
		numberColumn = sortNumberColumn(filter.Sort)
		predicates.AddIf(after.ID != 0, "("+numberColumn+", work.id) "+operator+" (?, ?)", after.SortNumber, after.ID)
		orderBy = numberColumn + orderDirection + ", work.id" + orderDirection
	case WorkSortStatus, WorkSortGenre, WorkSortTag, WorkSortFacet:
		textColumn = sortTextColumn(filter.Sort, filter.SortFacet)
		predicates.AddIf(after.ID != 0, "("+textColumn+", work.id) "+operator+" (?, ?)", after.SortText, after.ID)
		orderBy = textColumn + orderDirection + ", work.id" + orderDirection
	default:
		column, cursorValue := sortColumn(filter.Sort, after)
		predicates.AddIf(after.ID != 0,
			"("+column+", work.id) "+operator+" (?, ?)", cursorValue, after.ID)
		orderBy = column + orderDirection + ", work.id" + orderDirection
	}

	query := "SELECT " + workColumns + ",\n       " + workArtistColumn + ",\n       " + workStateColumn +
		",\n       " + discographyStatusColumns +
		",\n       EXISTS (SELECT 1 FROM work_file wf JOIN library_path lp ON lp.media_object_id = wf.media_object_id WHERE wf.work_id = work.id AND lp.missing_since IS NULL) AS on_disk" +
		",\n       COALESCE(work.lead_person_sort_name, '') AS artist_sort" +
		",\n       " + lastSearchedColumn + " AS last_searched" +
		",\n       " + unverifiedColumn + " AS unverified" +
		",\n       COALESCE((SELECT a.arrived_at FROM work_arrival a WHERE a.work_id = work.id), 0) AS arrived_at" +
		",\n       (work.lead_person_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM artwork aw" +
		" WHERE aw.subject_type = 'work' AND aw.subject_id = work.id AND aw.cache_key <> '')) AS artist_cover" +
		",\n       (NOT EXISTS (SELECT 1 FROM artwork own WHERE own.subject_type = 'work' AND own.subject_id = work.id AND own.cache_key <> '')" +
		" AND NOT EXISTS (SELECT 1 FROM artwork lead WHERE lead.subject_type = 'person' AND lead.subject_id = work.lead_person_id" +
		" AND lead.cache_key <> ''))" +
		" AS no_artwork" +
		",\n       EXISTS (SELECT 1 FROM desired_item mon WHERE mon.work_id = work.id AND mon.monitored = 1" +
		" AND mon.satisfaction_kind = 'SET') AS monitored" +
		",\n       COALESCE(work.description, '') AS description" +
		",\n       CAST(" + numberColumn + " AS INTEGER) AS sort_number, CAST(" + textColumn + " AS TEXT) AS sort_text" +
		",\n       CAST(COALESCE((SELECT SUM(mo.size_bytes) FROM work_file wf JOIN media_object mo ON mo.id = wf.media_object_id" +
		" WHERE wf.work_id = work.id), 0) AS INTEGER) AS size_bytes" +
		",\n       " + setTotalsColumns + ", work.lead_person_id" +
		",\n       " + narratorsColumn +
		"\nFROM work" + join +
		predicates.Where() + "\nORDER BY " + orderBy + "\nLIMIT ?"
	return query, predicates.Args(pageSize)
}

// yearSortExpression is the year sort's column: a NULL year compares as nothing in a row-value cursor (index 0095).
const yearSortExpression = "COALESCE(work.year, 0)"

// leadSortExpression is the lead-person order, exactly as migration 0072 indexes it: a work without a lead sorts last.
const leadSortExpression = "COALESCE(NULLIF(lead_person_sort_name, ''), char(1114111)) COLLATE NOCASE"

// leadSortCursor compares a cursor's sort name the same way; the cursor carries "" for a work without a lead.
const leadSortCursor = "COALESCE(NULLIF(?, ''), char(1114111))"

// sortColumn maps a WorkSort to its SQL column and the cursor value to compare
// it against. Qualified with "work." -- workArtistColumn's join makes "added_at"
// otherwise ambiguous against person's own column of the same name.
func sortColumn(sort WorkSort, after WorkCursor) (column string, cursorValue any) {
	switch sort {
	case WorkSortYear:
		return yearSortExpression, after.SortYear
	case WorkSortAdded:
		return "work.added_at", after.SortAdded
	case WorkSortSearched:
		return lastSearchedColumn, after.SortSearched
	default:
		return "work.sort_title", after.SortTitle
	}
}

// sortNumberColumn is the integer a size or profile sort orders by. Neither is indexed: they are
// views the user asks for, and the list stays paged.
func sortNumberColumn(sort WorkSort) string {
	if sort == WorkSortProfile {
		return "COALESCE((SELECT MIN(pd.profile_id) FROM desired_item pd WHERE pd.work_id = work.id), 0)"
	}
	return "CAST(COALESCE((SELECT SUM(mo.size_bytes) FROM work_file wf JOIN media_object mo ON mo.id = wf.media_object_id" +
		" WHERE wf.work_id = work.id), 0) AS INTEGER)"
}

// sortTextColumn is the text a status, genre or tag sort orders by: the album's first genre or tag by name.
func sortTextColumn(sort WorkSort, facet WorkFacet) string {
	switch sort {
	case WorkSortFacet:
		return facetSortColumn(facet)
	case WorkSortGenre:
		return tagNameColumn("<>")
	case WorkSortTag:
		return tagNameColumn("=")
	default:
		return "COALESCE((SELECT di.state FROM desired_item di WHERE di.work_id = work.id AND di.monitored = 1" +
			" ORDER BY di.satisfaction_kind = 'SET' DESC, di.id LIMIT 1), '') COLLATE NOCASE"
	}
}

// workTagTree is "the work carries this tag or one below it", one join over the materialised closure (ADR-33).
const workTagTree = "(SELECT 1 FROM tag_assignment ta JOIN tag_closure tc ON tc.descendant_id = ta.tag_id" +
	" WHERE ta.subject_type = 'work' AND ta.subject_id = work.id AND tc.ancestor_id = ?)"

// tagNameColumn reads the genres (a genre tree's tags, operator <>) or the free labels (=).
func tagNameColumn(operator string) string {
	return "COALESCE((SELECT MIN(st.name) FROM tag_assignment sa JOIN tag st ON st.id = sa.tag_id" +
		" WHERE sa.subject_type = 'work' AND sa.subject_id = work.id AND st.genre_tree " + operator + " ''), '') COLLATE NOCASE"
}

// setTotalsColumns sum a work's monitored SET items, what a list shows for a work of many groups.
const setTotalsColumns = `CAST(COALESCE((SELECT SUM(st.present_count) FROM desired_item st
      WHERE st.work_id = work.id AND st.monitored = 1 AND st.satisfaction_kind = 'SET'), 0) AS INTEGER) AS present_total,
   CAST(COALESCE((SELECT SUM(st.required_count) FROM desired_item st
      WHERE st.work_id = work.id AND st.monitored = 1 AND st.satisfaction_kind = 'SET'), 0) AS INTEGER) AS required_total,
   CAST((SELECT COUNT(*) FROM desired_item st
      WHERE st.work_id = work.id AND st.monitored = 1 AND st.satisfaction_kind = 'SET' AND st.state = 'UPGRADABLE') AS INTEGER) AS upgradable`

// unverifiedColumn is D6's "only local ids", over the per-work external-id index.
const unverifiedColumn = "(EXISTS (SELECT 1 FROM work_external_id ux WHERE ux.work_id = work.id) AND " +
	"NOT EXISTS (SELECT 1 FROM work_external_id vx WHERE vx.work_id = work.id AND vx.provider NOT LIKE 'local:%'))"

// lastSearchedColumn is when the work was last searched, 0 for never. Not indexed: the only list
// sorting on it is Wanted, which holds the incomplete albums alone.
const lastSearchedColumn = "COALESCE((SELECT MAX(h.last_searched_at) FROM hunt_queue h " +
	"JOIN desired_item hd ON hd.id = h.desired_item_id WHERE hd.work_id = work.id), 0)"

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

// containsPattern is a LIKE pattern, escaped with '\\', matching any lower-cased text containing search.
func containsPattern(search string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(search)) + "%"
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
			&work.UpdatedAt, &work.AddedBy, &work.Artist, &work.WantedState,
			&work.PresentCount, &work.RequiredCount, &work.AvailableFrom, &work.Downloading,
			&work.OnDisk, &work.ArtistSortName, &work.LastSearchedAt, &work.Unverified, &work.ArrivedAt, &work.ArtistCover, &work.NoArtwork,
			&work.Monitored, &work.Description, &work.SortNumber, &work.SortText, &work.SizeBytes,
			&work.PresentTotal, &work.RequiredTotal, &work.Upgradable, &work.LeadPersonID, &work.Narrators); err != nil {
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
	return w.store.ExplainQueryPlan(ctx, query, arguments...)
}

// AlternateTitles answers a work's own alias/original/translit titles
// (ADR-41, work_title) -- every kind except "primary", which is already
// Work.CanonicalTitle. Empty until something populates work_title, which
// nothing does yet (enrichment writes editions and credits but no alternate
// titles) -- a search tier built against this simply finds nothing extra
// until that lands, the same "ready ahead of its data" shape placement.Resolve
// and priority.Compute already had ahead of their own first caller.
func (w Works) AlternateTitles(ctx context.Context, workID int64) ([]string, error) {
	rows, err := librarydb.New(w.store.Read()).ListWorkTitles(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: alternate titles for work %d: %w", workID, err)
	}
	titles := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Kind == "primary" {
			continue
		}
		titles = append(titles, row.Title)
	}
	return titles, nil
}

// membershipStrings is membershipFragment for names: an empty list matches nothing.
func membershipStrings(template string, names []string) (string, []any, bool) {
	if len(names) == 0 {
		return "0 = 1", nil, true
	}
	arguments := make([]any, len(names))
	placeholders := make([]string, len(names))
	for index, name := range names {
		placeholders[index], arguments[index] = "?", name
	}
	return fmt.Sprintf(template, strings.Join(placeholders, ",")), arguments, true
}
