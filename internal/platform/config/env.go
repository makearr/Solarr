package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// EnvPrefix namespaces every override.
const EnvPrefix = "SOLARR_"

// envAliases are the short environment variable names a Docker deployment actually sets, each
// accepted beside the generated SOLARR_<SECTION>_<KEY> name (documentation/environment-variables.md).
var envAliases = map[string]string{
	"SOLARR_CONFIG_DIR":     "paths.config_dir",
	"SOLARR_DATA_DIR":       "paths.data_dir",
	"SOLARR_DB_DIR":         "paths.db_dir",
	"SOLARR_CACHE_DIR":      "paths.cache_dir",
	"SOLARR_BACKUP_DIR":     "paths.backup_dir",
	"SOLARR_PORT":           "server.port",
	"SOLARR_BIND":           "server.bind",
	"SOLARR_BASE_URL":       "server.base_url",
	"SOLARR_LOG_LEVEL":      "logging.level",
	"SOLARR_LOG_FORMAT":     "logging.format",
	"SOLARR_AUTH_MODE":      "security.auth_mode",
	"SOLARR_ENCRYPTION_KEY": "security.encryption_key",
}

// EnvName returns the generated variable name for a dotted setting path.
func EnvName(path string) string {
	return EnvPrefix + strings.ToUpper(strings.ReplaceAll(path, ".", "_"))
}

// Lookup reads an environment variable. Injected so overrides are testable
// without touching the process environment.
type Lookup func(name string) (string, bool)

// applyEnv overlays environment variables onto an already-decoded config (ADR-44).
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
		if err := setField(field, canonicalValue); err != nil {
			detail := fmt.Sprintf("set by %s: %v", canonical, err)
			if isSecretPath(leaf.path) {
				detail = fmt.Sprintf("set by %s: %s", canonical, redactedParseFailure)
			}
			problems = append(problems, newProblem(leaf.path, canonicalValue, detail))
		}
	}
	return problems
}

// setField assigns an environment variable's raw text to field. A string-kind field
// (including named types like LogLevel) takes the text directly: an env var is
// already a literal value, not YAML source, and routing it through yaml.Unmarshal
// misreads any value starting with a YAML-special character (*, &, !, [, { and more)
// as document syntax -- "* * * * *" for a cron schedule is exactly such a value,
// since a leading * is YAML's alias-reference syntax. Every other kind (Duration,
// Bytes, bool, int, ...) keeps the YAML path, which is where their type coercion and
// custom UnmarshalYAML methods live.
func setField(field reflect.Value, value string) error {
	if field.Kind() == reflect.String {
		field.SetString(value)
		return nil
	}
	return yaml.Unmarshal([]byte(value), field.Addr().Interface())
}

func aliasFor(path string) string {
	for name, aliased := range envAliases {
		if aliased == path {
			return name
		}
	}
	return ""
}

// EnvSet reports whether the environment overrides a setting, under either of its names.
func EnvSet(path string, lookup Lookup) bool {
	if _, found := lookup(EnvName(path)); found {
		return true
	}
	alias := aliasFor(path)
	if alias == "" {
		return false
	}
	_, found := lookup(alias)
	return found
}

// schemaLeaf is one settable field: its dotted path and how to reach it.
type schemaLeaf struct {
	path  string
	index []int
}

// schemaLeaves enumerates every scalar setting, so the environment mapping is generated from
// the same struct tags as the key set.
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
