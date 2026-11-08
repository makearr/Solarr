package spec

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// errFieldMissing marks a required field a row does not have; the row is skipped.
var errFieldMissing = errors.New("spec: field not found")

// node is one result row, or a whole answer, that values are read from.
type node interface {
	// read answers the value at selector (an attribute of it when attribute is set), after removing what remove matches.
	read(selector, attribute, remove string) (string, bool)
	// has reports whether selector matches within the node.
	has(selector string) bool
}

// readField reads one extractor from a node, applying its default, case and filters.
func readField(source node, extractor Extractor, context filterContext) (string, bool, error) {
	variables := context.variables
	value, found := "", false
	switch {
	case extractor.Text != nil:
		rendered, err := render(*extractor.Text, variables)
		if err != nil {
			return "", false, err
		}
		value, found = rendered, true
	case len(extractor.Case) > 0:
		for _, one := range extractor.Case {
			selector, err := render(one.Selector, variables)
			if err != nil {
				return "", false, err
			}
			if selector == "*" || source.has(selector) {
				rendered, err := render(one.Value, variables)
				if err != nil {
					return "", false, err
				}
				value, found = rendered, true
				break
			}
		}
	case extractor.Selector != "":
		selector, err := render(extractor.Selector, variables)
		if err != nil {
			return "", false, err
		}
		attribute, err := render(extractor.Attribute, variables)
		if err != nil {
			return "", false, err
		}
		value, found = source.read(selector, attribute, extractor.Remove)
	}
	if !found {
		switch {
		case extractor.Default != nil:
			rendered, err := render(*extractor.Default, variables)
			if err != nil {
				return "", false, err
			}
			value = rendered
		case extractor.Optional:
			return "", false, nil
		default:
			return "", false, fmt.Errorf("%w: %s", errFieldMissing, extractor.Name)
		}
	}
	value, err := applyFilters(value, extractor.Filters, context)
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// htmlNode reads from a goquery selection.
type htmlNode struct{ selection *goquery.Selection }

func (n htmlNode) find(selector string) *goquery.Selection {
	found := n.selection.Find(selector)
	if found.Length() == 0 && n.selection.Is(selector) {
		return n.selection
	}
	return found
}

func (n htmlNode) read(selector, attribute, remove string) (string, bool) {
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

func (n htmlNode) has(selector string) bool { return n.find(selector).Length() > 0 }

// jsonNode reads from a decoded JSON value; parents lets a selector reach out of the row ("..category").
type jsonNode struct {
	value   any
	parents []any
}

func (n jsonNode) read(selector, attribute, _ string) (string, bool) {
	if attribute != "" {
		selector = selector + "." + attribute
	}
	base, conditions := splitConditions(selector)
	picked, ok := n.path(base)
	if !ok || picked == nil {
		return "", false
	}
	if !holds(jsonNode{value: picked, parents: n.parents}, conditions) {
		return "", false
	}
	return jsonText(picked), true
}

func (n jsonNode) has(selector string) bool {
	base, conditions := splitConditions(selector)
	if base == "" {
		return holds(n, conditions)
	}
	picked, ok := n.path(base)
	return ok && picked != nil && holds(jsonNode{value: picked, parents: n.parents}, conditions)
}

// path follows "a.b[0].c" from the node; ".." names a key of the nearest parent that has it.
func (n jsonNode) path(path string) (any, bool) {
	current := n.value
	if strings.HasPrefix(path, "..") {
		key := strings.TrimPrefix(path, "..")
		for i := len(n.parents) - 1; i >= 0; i-- {
			if object, ok := n.parents[i].(map[string]any); ok {
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

// splitConditions separates "path:not(x:contains(1))" into the path and its pseudo-class conditions.
func splitConditions(selector string) (string, string) {
	if at := strings.IndexByte(selector, ':'); at >= 0 {
		return selector[:at], selector[at:]
	}
	return selector, ""
}

// holds evaluates ":contains(x)", ":not(...)" and ":has(...)" in a row, each possibly after a path inside the parentheses.
func holds(n jsonNode, conditions string) bool {
	for conditions != "" {
		name, inner, rest, ok := nextCondition(conditions)
		if !ok {
			return true
		}
		conditions = rest
		var result bool
		switch name {
		case "contains":
			result = strings.Contains(strings.ToLower(jsonText(n.value)), strings.ToLower(strings.Trim(inner, `"'`)))
		case "not":
			result = !n.has(inner)
		case "has":
			result = n.has(inner)
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
