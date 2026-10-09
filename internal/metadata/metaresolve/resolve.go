// Package metaresolve picks the effective value of a work's metadata field from what its providers stored (ADR-8).
// A media type applies its own provider priority when it fetches and merges its sources, so what it stored last is
// its current answer; a person's own value beats every provider (Invariant 13). No I/O.
package metaresolve

// ManualProvider is the provider of a value a person set; a provider refresh never writes it.
const ManualProvider = "__manual__"

// Value is one provider's stored value for one field.
type Value struct {
	Field     string
	Provider  string
	Value     string
	FetchedAt int64
}

// Resolve picks each field's value. An empty value never wins: clearing a person's value returns the field to its
// providers, and a provider without the field does not blank it.
func Resolve(values []Value) map[string]Value {
	resolved := make(map[string]Value)
	for _, value := range values {
		if value.Value == "" {
			continue
		}
		if have, found := resolved[value.Field]; !found || Wins(value, have) {
			resolved[value.Field] = value
		}
	}
	return resolved
}

// Wins says whether a beats b for the same field: a person's own, then the newest, then the provider's name.
func Wins(a, b Value) bool {
	if manualA, manualB := a.Provider == ManualProvider, b.Provider == ManualProvider; manualA != manualB {
		return manualA
	}
	if a.FetchedAt != b.FetchedAt {
		return a.FetchedAt > b.FetchedAt
	}
	return a.Provider < b.Provider
}

// OrderSQL is Wins as an ORDER BY over the work_field rows aliased alias, for a reader that picks in SQL.
func OrderSQL(alias string) string {
	return "(" + alias + ".provider = '" + ManualProvider + "') DESC, " + alias + ".fetched_at DESC, " + alias + ".provider"
}
