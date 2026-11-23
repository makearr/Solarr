package extract

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// ErrFieldMissing marks a required field a row does not have; the row is skipped.
var ErrFieldMissing = errors.New("spec: field not found")

// Node is one result row, or a whole answer, that values are read from.
type Node interface {
	// read answers the value at selector (an attribute of it when attribute is set), after removing what remove matches.
	Read(selector, attribute, remove string) (string, bool)
	// has reports whether selector matches within the node.
	Has(selector string) bool
}

// ReadField reads one extractor from a node, applying its default, case and filters.
func ReadField(source Node, extractor Extractor, context FilterContext) (string, bool, error) {
	variables := context.Variables
	value, found := "", false
	switch {
	case extractor.Text != nil:
		rendered, err := Render(*extractor.Text, variables)
		if err != nil {
			return "", false, err
		}
		value, found = rendered, true
	case len(extractor.Case) > 0:
		for _, one := range extractor.Case {
			selector, err := Render(one.Selector, variables)
			if err != nil {
				return "", false, err
			}
			if selector == "*" || source.Has(selector) {
				rendered, err := Render(one.Value, variables)
				if err != nil {
					return "", false, err
				}
				value, found = rendered, true
				break
			}
		}
	case extractor.Selector != "":
		selector, err := Render(extractor.Selector, variables)
		if err != nil {
			return "", false, err
		}
		attribute, err := Render(extractor.Attribute, variables)
		if err != nil {
			return "", false, err
		}
		value, found = source.Read(selector, attribute, extractor.Remove)
	}
	if !found {
		switch {
		case extractor.Default != nil:
			rendered, err := Render(*extractor.Default, variables)
			if err != nil {
				return "", false, err
			}
			value = rendered
		case extractor.Optional:
			return "", false, nil
		default:
			return "", false, fmt.Errorf("%w: %s", ErrFieldMissing, extractor.Name)
		}
	}
	value, err := ApplyFilters(value, extractor.Filters, context)
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// HTMLNode reads from a goquery selection.
type HTMLNode struct{ Selection *goquery.Selection }

func (n HTMLNode) find(selector string) *goquery.Selection {
	found := n.Selection.Find(selector)
	if found.Length() == 0 && n.Selection.Is(selector) {
		return n.Selection
	}
	return found
}

func (n HTMLNode) Read(selector, attribute, remove string) (string, bool) {
	found := n.find(selector)
	if found.Length() == 0 {
		return "", false
	}
	first := found.First()
	if remove != "" {
		first = first.Clone()
		first.Find(remove).Remove()
	}
	if attribute != "" {
		value, exists := first.Attr(attribute)
		return strings.TrimSpace(value), exists
	}
	return strings.TrimSpace(first.Text()), true
}

func (n HTMLNode) Has(selector string) bool { return n.find(selector).Length() > 0 }

// JSONNode reads from a decoded JSON value; parents lets a selector reach out of the row ("..category").
type JSONNode struct {
	Value   any
	Parents []any
}

func (n JSONNode) Read(selector, attribute, _ string) (string, bool) {
	if attribute != "" {
		selector = selector + "." + attribute
	}
	base, conditions := SplitConditions(selector)
	picked, ok := n.Path(base)
	if !ok || picked == nil {
		return "", false
	}
	if !Holds(JSONNode{Value: picked, Parents: n.Parents}, conditions) {
		return "", false
	}
	return jsonText(picked), true
}

func (n JSONNode) Has(selector string) bool {
	base, conditions := SplitConditions(selector)
	if base == "" {
		return Holds(n, conditions)
	}
	picked, ok := n.Path(base)
	return ok && picked != nil && Holds(JSONNode{Value: picked, Parents: n.Parents}, conditions)
}

// path follows "a.b[0].c" from the node; ".." names a key of the nearest parent that has it.
func (n JSONNode) Path(path string) (any, bool) {
	current := n.Value
	if strings.HasPrefix(path, "..") {
		key := strings.TrimPrefix(path, "..")
		for i := len(n.Parents) - 1; i >= 0; i-- {
			if object, ok := n.Parents[i].(map[string]any); ok {
				if value, found := object[key]; found {
					return value, true
				}
			}
		}
		return nil, false
	}
	if path == "" || path == "$" {
		return current, true
	}
	for _, step := range strings.Split(strings.TrimPrefix(path, "$."), ".") {
		name, index, indexed := step, 0, false
		if open := strings.IndexByte(step, '['); open >= 0 && strings.HasSuffix(step, "]") {
			parsed, err := strconv.Atoi(step[open+1 : len(step)-1])
			if err != nil {
				return nil, false
			}
			name, index, indexed = step[:open], parsed, true
		}
		if name != "" {
			object, ok := current.(map[string]any)
			if !ok {
				return nil, false
			}
			if current, ok = object[name]; !ok {
				return nil, false
			}
		}
		if indexed {
			list, ok := current.([]any)
			if !ok || index < 0 || index >= len(list) {
				return nil, false
			}
			current = list[index]
		}
	}
	return current, true
}

// SplitConditions separates "path:not(x:contains(1))" into the path and its pseudo-class conditions.
func SplitConditions(selector string) (string, string) {
	if at := strings.IndexByte(selector, ':'); at >= 0 {
		return selector[:at], selector[at:]
	}
	return selector, ""
}

// Holds evaluates ":contains(x)", ":not(...)" and ":has(...)" in a row, each possibly after a path inside the parentheses.
func Holds(n JSONNode, conditions string) bool {
	for conditions != "" {
		name, inner, rest, ok := nextCondition(conditions)
		if !ok {
			return true
		}
		conditions = rest
		var result bool
		switch name {
		case "contains":
			result = strings.Contains(strings.ToLower(jsonText(n.Value)), strings.ToLower(strings.Trim(inner, `"'`)))
		case "not":
			result = !n.Has(inner)
		case "has":
			result = n.Has(inner)
		default:
			result = true
		}
		if !result {
			return false
		}
	}
	return true
}

// nextCondition cuts the first ":name(inner)" off conditions.
func nextCondition(conditions string) (name, inner, rest string, ok bool) {
	open := strings.IndexByte(conditions, '(')
	if !strings.HasPrefix(conditions, ":") || open < 0 {
		return "", "", "", false
	}
	depth := 0
	for i := open; i < len(conditions); i++ {
		switch conditions[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return conditions[1:open], conditions[open+1 : i], conditions[i+1:], true
			}
		}
	}
	return "", "", "", false
}

// jsonText is a JSON value as the text a field holds: whole numbers without a decimal point, lists and objects as JSON.
func jsonText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	case nil:
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}
