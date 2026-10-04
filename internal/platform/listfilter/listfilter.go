// Package listfilter is the custom filter of a list: conditions of field, operator and value that a person builds and
// saves, parsed once from the request and turned into SQL by the reader that owns the field.
package listfilter

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Kind is what a field holds, which decides the operators it takes.
type Kind string

// The field kinds.
const (
	Text   Kind = "text"
	Number Kind = "number"
	Bool   Kind = "bool"
	Enum   Kind = "enum"
)

// Field declares one filterable field of a list; Choices are the values of an Enum.
type Field struct {
	Kind    Kind
	Choices []string
}

// Condition is one rule of a filter; all rules of a filter must hold.
type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

var operators = map[Kind][]string{
	Text:   {"contains", "not_contains", "is", "is_not", "starts_with"},
	Number: {"eq", "ne", "gt", "ge", "lt", "le"},
	Bool:   {"is"},
	Enum:   {"is", "is_not"},
}

// maxConditions bounds one filter: a list is not a query language.
const maxConditions = 12

// Parse reads the conditions a request carries as a JSON array; empty is no filter. Every problem is reported at once,
// naming the condition, so a person can fix a saved filter that no longer fits.
func Parse(raw string, fields map[string]Field) ([]Condition, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var conditions []Condition
	if err := json.Unmarshal([]byte(raw), &conditions); err != nil {
		return nil, fmt.Errorf("the filter is not a list of conditions: %w", err)
	}
	if len(conditions) > maxConditions {
		return nil, fmt.Errorf("a filter has at most %d conditions", maxConditions)
	}
	var problems []string
	for index, condition := range conditions {
		field, known := fields[condition.Field]
		switch {
		case !known:
			problems = append(problems, fmt.Sprintf("condition %d: %q is not a field of this list", index+1, condition.Field))
			continue
		case !slices.Contains(operators[field.Kind], condition.Op):
			problems = append(problems, fmt.Sprintf("condition %d: %q does not take %q", index+1, condition.Field, condition.Op))
			continue
		}
		switch field.Kind {
		case Number:
			if _, err := strconv.ParseFloat(condition.Value, 64); err != nil {
				problems = append(problems, fmt.Sprintf("condition %d: %q is not a number", index+1, condition.Value))
			}
		case Bool:
			if condition.Value != "true" && condition.Value != "false" {
				problems = append(problems, fmt.Sprintf("condition %d: %q is not true or false", index+1, condition.Value))
			}
		case Enum:
			if !slices.Contains(field.Choices, condition.Value) {
				problems = append(problems, fmt.Sprintf("condition %d: %q is not one of %s", index+1, condition.Value, strings.Join(field.Choices, ", ")))
			}
		case Text:
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return conditions, nil
}

// SQL is the condition as a predicate over a column or expression, with its arguments; a Bool field is read as a
// truth value of the expression, so "photo is false" is NOT (expression).
func (c Condition) SQL(kind Kind, expression string) (string, []any) {
	switch kind {
	case Text:
		lower := "LOWER(" + expression + ")"
		value := strings.ToLower(c.Value)
		switch c.Op {
		case "contains":
			return lower + " LIKE ? ESCAPE '\\'", []any{"%" + escapeLike(value) + "%"}
		case "not_contains":
			return lower + " NOT LIKE ? ESCAPE '\\'", []any{"%" + escapeLike(value) + "%"}
		case "starts_with":
			return lower + " LIKE ? ESCAPE '\\'", []any{escapeLike(value) + "%"}
		case "is":
			return lower + " = ?", []any{value}
		default:
			return lower + " <> ?", []any{value}
		}
	case Number:
		number, _ := strconv.ParseFloat(c.Value, 64)
		symbol := map[string]string{"eq": "=", "ne": "<>", "gt": ">", "ge": ">=", "lt": "<", "le": "<="}[c.Op]
		return "(" + expression + ") " + symbol + " ?", []any{number}
	case Bool:
		if c.Value == "true" {
			return "(" + expression + ")", nil
		}
		return "NOT (" + expression + ")", nil
	default:
		if c.Op == "is_not" {
			return "(" + expression + ") <> ?", []any{c.Value}
		}
		return "(" + expression + ") = ?", []any{c.Value}
	}
}

func escapeLike(text string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text)
}
