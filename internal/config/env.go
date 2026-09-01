package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// EnvPrefix namespaces every override.
const EnvPrefix = "MEDIANEXUS_"

// pathAliases are the short environment variable names §14 documents for the
// directory settings, because those are the ones a Docker deployment actually
// sets. Everything else uses the generated MEDIANEXUS_<SECTION>_<KEY> form,
// which cannot collide as the schema grows.
//
// Setting both forms for one setting is an error rather than a precedence rule:
// a container with two variables disagreeing about where the database lives
// should say so, not pick one.
var pathAliases = map[string]string{
	"MEDIANEXUS_CONFIG_DIR": "paths.config_dir",
	"MEDIANEXUS_DATA_DIR":   "paths.data_dir",
	"MEDIANEXUS_DB_DIR":     "paths.db_dir",
	"MEDIANEXUS_CACHE_DIR":  "paths.cache_dir",
	"MEDIANEXUS_BACKUP_DIR": "paths.backup_dir",
}

// EnvName returns the generated variable name for a dotted setting path.
func EnvName(path string) string {
	return EnvPrefix + strings.ToUpper(strings.ReplaceAll(path, ".", "_"))
}

// Lookup reads an environment variable. Injected so overrides are testable
// without touching the process environment.
type Lookup func(name string) (string, bool)

// applyEnv overlays environment variables onto an already-decoded config.
//
// Values are parsed as YAML scalars, the same way the file's were, so an
// override is validated identically to a file value and MEDIANEXUS_SERVER_PORT=abc
// fails exactly as `port: abc` would (ADR-44).
func applyEnv(config *Config, lookup Lookup) Problems {
	var problems Problems

	settings := reflect.ValueOf(config).Elem()
	for _, leaf := range schemaLeaves() {
		canonical := EnvName(leaf.path)
		alias := aliasFor(leaf.path)

		canonicalValue, hasCanonical := lookup(canonical)
		aliasValue, hasAlias := "", false
		if alias != "" {
			aliasValue, hasAlias = lookup(alias)
		}

		switch {
		case hasCanonical && hasAlias && canonicalValue != aliasValue:
			problems = append(problems, newProblem(leaf.path, canonicalValue,
				fmt.Sprintf("set by both %s and %s, which disagree", canonical, alias)))
			continue
		case !hasCanonical && !hasAlias:
			continue
		case !hasCanonical:
			canonicalValue, canonical = aliasValue, alias
		}

		field := fieldByPath(settings, leaf.index)
		if err := yaml.Unmarshal([]byte(canonicalValue), field.Addr().Interface()); err != nil {
			detail := fmt.Sprintf("set by %s: %v", canonical, err)
			if isSecretPath(leaf.path) {
				detail = fmt.Sprintf("set by %s: %s", canonical, redactedParseFailure)
			}
			problems = append(problems, newProblem(leaf.path, canonicalValue, detail))
		}
	}
	return problems
}

func aliasFor(path string) string {
	for name, aliased := range pathAliases {
		if aliased == path {
			return name
		}
	}
	return ""
}

// schemaLeaf is one settable field: its dotted path and how to reach it.
type schemaLeaf struct {
	path  string
	index []int
}

// schemaLeaves enumerates every scalar setting, so the environment mapping is
// generated from the same struct tags as the key set. A setting cannot be added
// and then be unsettable from the environment.
func schemaLeaves() []schemaLeaf {
	var leaves []schemaLeaf
	collectLeaves("", nil, reflect.TypeOf(Config{}), &leaves)
	sort.Slice(leaves, func(i, j int) bool { return leaves[i].path < leaves[j].path })
	return leaves
}

func collectLeaves(prefix string, index []int, structType reflect.Type, leaves *[]schemaLeaf) {
	for fieldIndex := range structType.NumField() {
		field := structType.Field(fieldIndex)
		name := yamlName(field)
		if name == "" {
			continue
		}

		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		position := append(append([]int(nil), index...), fieldIndex)

		if field.Type.Kind() == reflect.Struct {
			collectLeaves(path, position, field.Type, leaves)
			continue
		}
		*leaves = append(*leaves, schemaLeaf{path: path, index: position})
	}
}

func fieldByPath(root reflect.Value, index []int) reflect.Value {
	value := root
	for _, position := range index {
		value = value.Field(position)
	}
	return value
}
