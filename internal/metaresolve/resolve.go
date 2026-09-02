// Package metaresolve picks the effective value of a metadata field from several
// providers' stored values (ADR-8). No I/O: the caller fetches the rows and the
// priority order; this package only decides who wins.
package metaresolve

// ManualProvider is the provider name a work_field row carries when a person, not a
// metadata provider, set it. It always wins, never mind priority (Invariant 13), and
// a routine provider refresh never writes this provider -- SetWorkField's upsert key
// is (work_id, field, provider), so a refresh for "tmdb" cannot touch it.
const ManualProvider = "__manual__"

// Value is one provider's stored value for one field -- ListWorkFields's row shape,
// decoupled from the generated type so this package needs no store import.
type Value struct {
	Field     string
	Provider  string
	Value     string
	FetchedAt int64
}

// Resolved is which value won for one field, and which provider it came from.
type Resolved struct {
	Value     string
	Provider  string
	FetchedAt int64
}

// Resolve picks the effective value for every field present in values, keyed by
// field name. A field resolves to ManualProvider's row whenever one exists, however
// it reads -- a person choosing to clear a field is still an override. Otherwise the
// winner is the first provider in priority order that has a *non-empty* value for
// that field: "a provider ranked first but missing a synopsis does not blank the
// synopsis" (ADR-8). A provider absent from priority never wins, even with a value.
// A field with nothing eligible is left out of the result rather than reported as an
// empty win.
func Resolve(values []Value, priority []string) map[string]Resolved {
	byField := make(map[string][]Value, len(values))
	for _, v := range values {
		byField[v.Field] = append(byField[v.Field], v)
	}

	resolved := make(map[string]Resolved, len(byField))
	for field, candidates := range byField {
		if winner, ok := resolveOne(candidates, priority); ok {
			resolved[field] = winner
		}
	}
	return resolved
}

func resolveOne(candidates []Value, priority []string) (Resolved, bool) {
	for _, v := range candidates {
		if v.Provider == ManualProvider {
			return Resolved{Value: v.Value, Provider: v.Provider, FetchedAt: v.FetchedAt}, true
		}
	}

	for _, provider := range priority {
		for _, v := range candidates {
			if v.Provider == provider && v.Value != "" {
				return Resolved{Value: v.Value, Provider: v.Provider, FetchedAt: v.FetchedAt}, true
			}
		}
	}
	return Resolved{}, false
}
