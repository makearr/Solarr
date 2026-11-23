package providerdef

import (
	"fmt"
	"slices"

	"github.com/goccy/go-yaml"
)

// Marshal writes a definition file's envelope and descriptor fields.
func Marshal(file File) ([]byte, error) {
	if file.Schema == 0 {
		file.Schema = SchemaVersion
	}
	data, err := yaml.Marshal(file)
	if err != nil {
		return nil, fmt.Errorf("providerdef: write %s/%s: %w", file.Kind, file.Key, err)
	}
	return data, nil
}

// Tuning is what a custom descriptor changes of a compiled provider: nothing is set unless the file said so.
type Tuning struct {
	Enabled      *bool
	PerMinute    int64
	MediaTypes   []string
	BaseURLs     []string
	RequestDelay string
}

// Tuning answers what the custom descriptor of a compiled provider changes; the built-in descriptor's own values are no change.
func (c *Catalog) Tuning(kind Kind, key string) Tuning {
	def, found := c.Get(kind, key)
	if !found || def.Source != SourceCustom || def.Engine != EngineBuiltin {
		return Tuning{}
	}
	tuning := Tuning{Enabled: def.Enabled, MediaTypes: def.MediaTypes, BaseURLs: def.BaseURLs, RequestDelay: def.RequestDelay}
	if def.Pace != nil {
		tuning.PerMinute = def.Pace.PerMinute
	}
	return tuning
}

// Hidden says a custom descriptor switches the compiled provider off for new use.
func (c *Catalog) Hidden(kind Kind, key string) bool {
	enabled := c.Tuning(kind, key).Enabled
	return enabled != nil && !*enabled
}

// CheckOverride compares a custom descriptor with the built-in one it replaces: a changed field a person may not tune is
// a warning (it is ignored), a pace above the compiled ceiling is an error, and media types may only narrow.
func CheckOverride(builtin, custom Definition) []Problem {
	var problems []Problem
	ignored := func(field string, differs bool) {
		if differs {
			problems = append(problems, Problem{Severity: SeverityWarning, Code: CodeIgnoredField, Field: field})
		}
	}
	ignored("needs_key", custom.NeedsKey && !builtin.NeedsKey)
	ignored("carries", len(custom.Carries) > 0 && !slices.Equal(custom.Carries, builtin.Carries))
	ignored("also_for", len(custom.AlsoFor) > 0 && !slices.Equal(custom.AlsoFor, builtin.AlsoFor))
	ignored("published", custom.Published != "" && custom.Published != builtin.Published)
	ignored("hosts", len(custom.Hosts) > 0 && !slices.Equal(custom.NormalHosts(), builtin.NormalHosts()))
	ignored("role", custom.Role != "" && custom.Role != builtin.Role)
	ignored("impl", custom.Impl != "" && custom.Impl != builtin.Impl)
	ignored("privacy", custom.Privacy != "" && custom.Privacy != builtin.Privacy)
	ignored("protocol", custom.Protocol != "" && custom.Protocol != builtin.Protocol)
	ignored("credentials", custom.Credentials != nil && (builtin.Credentials == nil || *custom.Credentials != *builtin.Credentials))
	if custom.Pace != nil && builtin.Pace != nil && builtin.Pace.MaxPerMinute > 0 && custom.Pace.PerMinute > builtin.Pace.MaxPerMinute {
		problems = append(problems, Problem{Severity: SeverityError, Code: CodePaceAboveMax, Field: "pace.per_minute"})
	}
	if len(custom.MediaTypes) > 0 && !subset(custom.MediaTypes, builtin.MediaTypes) {
		problems = append(problems, Problem{Severity: SeverityError, Code: CodeWrongType, Field: "media_types"})
	}
	return problems
}

func subset(narrow, wide []string) bool {
	for _, one := range narrow {
		if !slices.Contains(wide, one) {
			return false
		}
	}
	return true
}
