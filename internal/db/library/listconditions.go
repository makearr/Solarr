package library

import (
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

// WorkFields are the fields a custom filter of the Albums list may name.
var WorkFields = map[string]listfilter.Field{
	"title":     {Kind: listfilter.Text},
	"artist":    {Kind: listfilter.Text},
	"year":      {Kind: listfilter.Number},
	"monitored": {Kind: listfilter.Bool},
	"status":    {Kind: listfilter.Enum, Choices: []string{"MISSING", "PARTIAL", "COMPLETE", "UPGRADABLE"}},
	"genre":     {Kind: listfilter.Text},
	"tag":       {Kind: listfilter.Text},
	"size_mb":   {Kind: listfilter.Number},
	"on_disk":   {Kind: listfilter.Bool},
	"verified":  {Kind: listfilter.Bool},
}

// existsText is a text condition over the rows of a subquery: "has a genre containing", and for the negative
// operators "has no genre containing", which is what a person means by "does not contain".
func existsText(condition listfilter.Condition, from, column string) (string, []any) {
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
	return exists, arguments
}

const (
	workGenreFrom = "FROM tag_assignment sa JOIN tag st ON st.id = sa.tag_id" +
		" WHERE sa.subject_type = 'work' AND sa.subject_id = work.id AND st.taxonomy <> ''"
	workTagFrom = "FROM tag_assignment sa JOIN tag st ON st.id = sa.tag_id" +
		" WHERE sa.subject_type = 'work' AND sa.subject_id = work.id AND st.taxonomy = ''"
	workSizeBytes = "CAST(COALESCE((SELECT SUM(mo.size_bytes) FROM work_file wf JOIN media_object mo ON mo.id = wf.media_object_id" +
		" WHERE wf.work_id = work.id), 0) AS INTEGER)"
)

// addWorkConditions adds every condition of an Albums custom filter as a predicate over work.
func addWorkConditions(predicates *store.Predicates, conditions []listfilter.Condition) {
	for _, condition := range conditions {
		var fragment string
		var arguments []any
		switch condition.Field {
		case "title":
			fragment, arguments = condition.SQL(listfilter.Text, "work.canonical_title")
		case "artist":
			fragment, arguments = condition.SQL(listfilter.Text, "COALESCE(lead_person.name, '')")
		case "year":
			fragment, arguments = condition.SQL(listfilter.Number, "COALESCE(work.year, 0)")
		case "monitored":
			fragment, arguments = condition.SQL(listfilter.Bool,
				"EXISTS (SELECT 1 FROM desired_item mon WHERE mon.work_id = work.id AND mon.monitored = 1 AND mon.satisfaction_kind = 'SET')")
		case "status":
			fragment, arguments = condition.SQL(listfilter.Enum,
				"COALESCE((SELECT di.state FROM desired_item di WHERE di.work_id = work.id AND di.monitored = 1"+
					" ORDER BY di.satisfaction_kind = 'SET' DESC, di.id LIMIT 1), '')")
		case "genre":
			fragment, arguments = existsText(condition, workGenreFrom, "st.name")
		case "tag":
			fragment, arguments = existsText(condition, workTagFrom, "st.name")
		case "size_mb":
			fragment, arguments = condition.SQL(listfilter.Number, workSizeBytes+" / 1048576.0")
		case "on_disk":
			fragment, arguments = condition.SQL(listfilter.Bool, "EXISTS (SELECT 1 FROM work_file wf JOIN library_path lp ON lp.media_object_id = wf.media_object_id WHERE wf.work_id = work.id AND lp.missing_since IS NULL)")
		case "verified":
			fragment, arguments = condition.SQL(listfilter.Bool, "NOT "+unverifiedColumn)
		default:
			continue
		}
		predicates.Add(fragment, arguments...)
	}
}
