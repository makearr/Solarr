package library

import (
	"database/sql"
	"testing"
)

func TestRecountedState(t *testing.T) {
	one := sql.NullInt64{Int64: 1, Valid: true}
	for _, test := range []struct {
		name     string
		previous string
		present  int64
		want     string
	}{
		{"a rescan keeps an upgrade wanted", "UPGRADABLE", 1, "UPGRADABLE"},
		{"complete stays complete", "COMPLETE", 1, "COMPLETE"},
		{"a lost file is missing even when upgradable", "UPGRADABLE", 0, "MISSING"},
		{"a new file completes", "MISSING", 1, "COMPLETE"},
	} {
		if got := recountedState(test.previous, one, test.present); got != test.want {
			t.Errorf("%s: state = %q, want %q", test.name, got, test.want)
		}
	}
}
