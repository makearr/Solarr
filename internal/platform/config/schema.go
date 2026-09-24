package config

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// The key set is derived from the struct tags rather than written out separately, so a new
// setting cannot be added and then rejected as unknown.
func schemaKeys() map[string][]string {
	keys := make(map[string][]string)
	collectKeys("", reflect.TypeOf(Config{}), keys)
	return keys
}

func collectKeys(prefix string, structType reflect.Type, keys map[string][]string) {
	for index := range structType.NumField() {
		field := structType.Field(index)
		name := yamlName(field)
		if name == "" {
			continue
		}
		keys[prefix] = append(keys[prefix], name)

		if field.Type.Kind() != reflect.Struct {
			continue
		}
		child := name
		if prefix != "" {
			child = prefix + "." + name
		}
		collectKeys(child, field.Type, keys)
	}
}

func yamlName(field reflect.StructField) string {
	tag := field.Tag.Get("yaml")
	if tag == "" || tag == "-" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	return name
}

// unknownKeys reports every key the schema does not define, each with the nearest valid
// alternative (Invariant 59).
func unknownKeys(document map[string]any) Problems {
	keys := schemaKeys()
	var problems Problems

	var walk func(prefix string, node map[string]any)
	walk = func(prefix string, node map[string]any) {
		valid := keys[prefix]
		for key, value := range node {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}

			if !slices.Contains(valid, key) {
				problem := newProblem(path, summarise(value), "unknown key")
				problem.Suggestion = nearestKey(key, valid)
				problems = append(problems, problem)
				continue
			}
			if child, isSection := value.(map[string]any); isSection {
				walk(path, child)
			}
		}
	}
	walk("", document)
	return problems
}

// summarise renders a value for the error report without printing a whole
// section back at the user.
func summarise(value any) string {
	switch typed := value.(type) {
	case nil:
		return "(empty)"
	case map[string]any:
		return fmt.Sprintf("(section, %d keys)", len(typed))
	case []any:
		return fmt.Sprintf("(list, %d items)", len(typed))
	case string:
		if len(typed) > 32 {
			return typed[:29] + "..."
		}
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

// UnknownKeysIn reports every key a decoded YAML document has that shape does not define, with
// the nearest valid one: nested structs, lists of structs and pointers are followed, and a map's
// keys are free. Used for documents other than config.yml, such as a settings restore.
func UnknownKeysIn(document any, shape reflect.Type) Problems {
	var problems Problems
	var walk func(path string, node any, shape reflect.Type)
	walk = func(path string, node any, shape reflect.Type) {
		for shape.Kind() == reflect.Pointer {
			shape = shape.Elem()
		}
		switch shape.Kind() {
		case reflect.Struct:
			fields, ok := node.(map[string]any)
			if !ok {
				return
			}
			byName := map[string]reflect.Type{}
			var valid []string
			for index := range shape.NumField() {
				if name := yamlName(shape.Field(index)); name != "" {
					byName[name] = shape.Field(index).Type
					valid = append(valid, name)
				}
			}
			for _, key := range slices.Sorted(maps.Keys(fields)) {
				child := key
				if path != "" {
					child = path + "." + key
				}
				fieldType, known := byName[key]
				if !known {
					problem := newProblem(child, summarise(fields[key]), "unknown key")
					problem.Suggestion = nearestKey(key, valid)
					problems = append(problems, problem)
					continue
				}
				walk(child, fields[key], fieldType)
			}
		case reflect.Slice:
			items, ok := node.([]any)
			if !ok {
				return
			}
			for index, item := range items {
				walk(fmt.Sprintf("%s[%d]", path, index), item, shape.Elem())
			}
		}
	}
	walk("", document, shape)
	return problems
}
