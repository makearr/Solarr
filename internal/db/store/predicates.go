package store

import "strings"

// Predicates collects the filters a request actually carries, so a query says only what
// it means. A filter written into static SQL as an is-it-empty test is not sargable, so
// SQLite can use no index for that column and sorts the whole table instead: measured at
// 31 times slower (documentation/sql-and-sqlc.md).
type Predicates struct {
	fragments []string
	arguments []any
}

// Add appends a fragment and its arguments. A filter that is not set is simply not
// added, which is the whole point.
func (p *Predicates) Add(fragment string, arguments ...any) {
	p.fragments = append(p.fragments, fragment)
	p.arguments = append(p.arguments, arguments...)
}

// AddIf is Add when keep is true, so a caller reads as a list of filters rather than a
// list of ifs.
func (p *Predicates) AddIf(keep bool, fragment string, arguments ...any) {
	if keep {
		p.Add(fragment, arguments...)
	}
}

// Where renders the clause, including the keyword, or nothing at all when no filter was
// added. Fragments are joined with AND; a fragment needing OR brackets itself.
func (p Predicates) Where() string {
	if len(p.fragments) == 0 {
		return ""
	}
	return "\nWHERE " + strings.Join(p.fragments, "\n  AND ")
}

// Args returns the fragment arguments in order, followed by trailing ones. Placeholders
// are positional, so anything after the clause -- a page size -- is passed here rather
// than appended by the caller.
func (p Predicates) Args(trailing ...any) []any {
	arguments := make([]any, 0, len(p.arguments)+len(trailing))
	arguments = append(arguments, p.arguments...)
	return append(arguments, trailing...)
}

// Len reports how many filters were added.
func (p Predicates) Len() int { return len(p.fragments) }
