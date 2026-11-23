package providerdef

import (
	"slices"
	"sync/atomic"
)

// Catalog is every definition of the running build: built-in and custom, custom winning by key. It never changes after it is built.
type Catalog struct {
	byKind map[Kind]map[string]Definition
}

// NewCatalog merges built-in and custom definitions; a custom one replaces the built-in one of its key and is marked Overrides.
func NewCatalog(builtin, custom []Definition) *Catalog {
	catalog := &Catalog{byKind: map[Kind]map[string]Definition{}}
	for _, def := range builtin {
		catalog.add(def)
	}
	for _, def := range custom {
		if _, taken := catalog.byKind[def.Kind][def.Key]; taken && catalog.byKind[def.Kind][def.Key].Source == SourceBuiltin {
			def.Overrides = true
		}
		catalog.add(def)
	}
	return catalog
}

func (c *Catalog) add(def Definition) {
	if c.byKind[def.Kind] == nil {
		c.byKind[def.Kind] = map[string]Definition{}
	}
	c.byKind[def.Kind][def.Key] = def
}

// Get answers the definition of a key.
func (c *Catalog) Get(kind Kind, key string) (Definition, bool) {
	def, found := c.byKind[kind][key]
	return def, found
}

// List answers a kind's definitions in key order.
func (c *Catalog) List(kind Kind) []Definition {
	defs := make([]Definition, 0, len(c.byKind[kind]))
	for _, def := range c.byKind[kind] {
		defs = append(defs, def)
	}
	slices.SortFunc(defs, func(a, b Definition) int {
		switch {
		case a.Key < b.Key:
			return -1
		case a.Key > b.Key:
			return 1
		}
		return 0
	})
	return defs
}

// Holder keeps the current catalog and swaps it whole on a reload, so a reader never sees half of one.
type Holder struct{ current atomic.Pointer[Catalog] }

// NewHolder starts with an empty catalog.
func NewHolder() *Holder {
	holder := &Holder{}
	holder.current.Store(NewCatalog(nil, nil))
	return holder
}

// Catalog is the catalog now in force.
func (h *Holder) Catalog() *Catalog { return h.current.Load() }

// Swap puts a new catalog in force.
func (h *Holder) Swap(next *Catalog) { h.current.Store(next) }

// State is what a person set about a definition in the interface; it wins over the file.
type State struct {
	// Enabled is nil while the person has not chosen, so the file's own default holds.
	Enabled       *bool
	ApprovedHash  string
	ApprovedHosts []string
}

// Status is where a definition stands.
type Status string

// A definition is active, switched off, or a custom one that still needs an administrator's approval.
const (
	StatusActive        Status = "active"
	StatusOff           Status = "off"
	StatusNeedsApproval Status = "needs_approval"
)

// StatusOf is a definition's status under the state stored for it. A custom file that is the provider (http, spec) needs an
// approval of its host list; a changed body with the same hosts stays approved, a changed host list does not.
func StatusOf(def Definition, state State) Status {
	if def.Source == SourceCustom && def.Engine != EngineBuiltin {
		if state.ApprovedHash == "" || !slices.Equal(state.ApprovedHosts, def.NormalHosts()) {
			return StatusNeedsApproval
		}
	}
	enabled := def.EnabledByDefault()
	if state.Enabled != nil {
		enabled = *state.Enabled
	}
	if !enabled {
		return StatusOff
	}
	return StatusActive
}

// BuildCatalog is NewCatalog with each custom descriptor of a compiled provider checked against the built-in one: a warning
// is reported and the file kept, an error is reported and the file dropped so the built-in descriptor stays.
func BuildCatalog(builtin, custom []Definition) (*Catalog, []FileProblems) {
	compiled := map[Kind]map[string]Definition{}
	for _, def := range builtin {
		if compiled[def.Kind] == nil {
			compiled[def.Kind] = map[string]Definition{}
		}
		compiled[def.Kind][def.Key] = def
	}
	var kept []Definition
	var reports []FileProblems
	for _, def := range custom {
		base, replaces := compiled[def.Kind][def.Key]
		if !replaces || def.Engine != EngineBuiltin || base.Engine != EngineBuiltin {
			kept = append(kept, def)
			continue
		}
		problems := CheckOverride(base, def)
		if len(problems) > 0 {
			reports = append(reports, FileProblems{File: def.Key + ".yaml", Problems: problems})
		}
		if !hasError(problems) {
			kept = append(kept, def)
		}
	}
	return NewCatalog(builtin, kept), reports
}
