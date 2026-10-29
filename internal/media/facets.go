package media

import (
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/identify/model"
)

// FacetKind is what values a facet holds, which decides how it is shown, sorted and filtered.
type FacetKind string

// The facet kinds.
const (
	FacetText     FacetKind = "text"
	FacetNumber   FacetKind = "number"
	FacetEnum     FacetKind = "enum"
	FacetList     FacetKind = "list"
	FacetDuration FacetKind = "duration"
)

// SourceKind is where core reads a facet from; every source is an existing generic table (plan 09 section 3).
type SourceKind string

// The facet sources.
const (
	// SourceColumn is a base column of the entity's table.
	SourceColumn SourceKind = "column"
	// SourceField is the winning work_field value: the user's own first, then the providers'.
	SourceField SourceKind = "field"
	// SourceOrganisation is the work's organisation in one role.
	SourceOrganisation SourceKind = "organisation"
	// SourceCredit is the work's people credited in one role.
	SourceCredit SourceKind = "credit"
	// SourceClassification is the work's work_classification values of one kind.
	SourceClassification SourceKind = "classification"
	// SourceGenreTree is the work's tags of one genre tree, whoever assigned them.
	SourceGenreTree SourceKind = "genre_tree"
)

// FacetSource names where a facet is read: a column, a field, a role, a classification kind or a tag source.
type FacetSource struct {
	Kind SourceKind
	Name string
}

// Facet is one extra field of a base entity a media type declares; core lists, shows, sorts and filters it.
type Facet struct {
	Key    string
	Label  string
	Entity model.LevelEntity
	Kind   FacetKind
	Source FacetSource
	// Choices are an enum's values.
	Choices []Choice
	// Column offers it as a table column; Sortable and Filterable say what a list may do with it.
	Column     bool
	Sortable   bool
	Filterable bool
	// Editable lets a person set it on the work's Edit; their value wins over every source.
	Editable bool
	// RefreshesWork reads the work again when a person changes it: the type derives other data from it.
	RefreshesWork bool
	// Badge marks the work's tile with the logo of its organisation in this role: a series' network.
	Badge bool
}

// workColumns are the base columns of a work a facet may read; a facet never adds one.
var workColumns = []string{"year", "subtype", "runtime_ms", "original_language"}

// checkFacet refuses a facet core could not read: an unknown source, a column the work does not have, an enum
// without choices.
func checkFacet(facet Facet) error {
	switch facet.Source.Kind {
	case SourceColumn:
		if facet.Entity != model.LevelEntityWork || !slices.Contains(workColumns, facet.Source.Name) {
			return fmt.Errorf("facet %s reads column %s, which a %s does not have", facet.Key, facet.Source.Name, facet.Entity)
		}
	case SourceField, SourceOrganisation, SourceCredit, SourceClassification, SourceGenreTree:
		if facet.Source.Name == "" {
			return fmt.Errorf("facet %s names no %s", facet.Key, facet.Source.Kind)
		}
	default:
		return fmt.Errorf("facet %s has an unknown source %q", facet.Key, facet.Source.Kind)
	}
	if facet.Editable && facet.Source.Kind != SourceField {
		return fmt.Errorf("facet %s is editable but not a field", facet.Key)
	}
	if facet.Kind == FacetEnum && len(facet.Choices) == 0 {
		return fmt.Errorf("facet %s is an enum without choices", facet.Key)
	}
	return nil
}
