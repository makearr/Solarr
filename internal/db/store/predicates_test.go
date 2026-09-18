package store_test

import (
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/store"
)

func TestPredicates_NoFilterMeansNoClause(t *testing.T) {
	var predicates store.Predicates

	if got := predicates.Where(); got != "" {
		t.Errorf("Where() = %q, want empty: a WHERE with nothing in it is a syntax error", got)
	}
	if got := predicates.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0", got)
	}
}

func TestPredicates_JoinsWithAnd(t *testing.T) {
	var predicates store.Predicates
	predicates.Add("media_type = ?", "music")
	predicates.Add("year >= ?", 1990)

	clause := predicates.Where()
	if !strings.Contains(clause, "WHERE") {
		t.Errorf("Where() = %q, want a WHERE keyword", clause)
	}
	if strings.Count(clause, "AND") != 1 {
		t.Errorf("Where() = %q, want exactly one AND between two fragments", clause)
	}
}

// Placeholders are positional, so an argument out of order binds to the wrong column
// and the query silently answers the wrong question.
func TestPredicates_ArgumentsKeepFragmentOrderAndTrailAfter(t *testing.T) {
	var predicates store.Predicates
	predicates.Add("media_type = ?", "music")
	predicates.Add("sort_title > ? OR (sort_title = ? AND id > ?)", "silver", "silver", int64(42))

	got := predicates.Args(100)
	want := []any{"music", "silver", "silver", int64(42), 100}

	if len(got) != len(want) {
		t.Fatalf("Args() returned %d arguments, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("argument %d = %v, want %v", index, got[index], want[index])
		}
	}
}

func TestPredicates_AddIfSkipsWhatIsNotSet(t *testing.T) {
	var predicates store.Predicates
	predicates.AddIf(true, "media_type = ?", "music")
	predicates.AddIf(false, "original_language = ?", "")

	if predicates.Len() != 1 {
		t.Errorf("Len() = %d, want 1: an unset filter must not reach the SQL", predicates.Len())
	}
	if got := predicates.Args(); len(got) != 1 {
		t.Errorf("Args() returned %d arguments, want 1: a skipped fragment must skip its argument too", len(got))
	}
}

// Args must not hand back its own slice, or a second call sees the first call's
// trailing arguments.
func TestPredicates_ArgsDoesNotAliasBetweenCalls(t *testing.T) {
	var predicates store.Predicates
	predicates.Add("media_type = ?", "music")

	first := predicates.Args(10)
	second := predicates.Args(20)

	if first[1] != 10 {
		t.Errorf("the first call's trailing argument became %v, want 10", first[1])
	}
	if second[1] != 20 {
		t.Errorf("the second call's trailing argument is %v, want 20", second[1])
	}
}
