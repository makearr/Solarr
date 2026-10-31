package app

import (
	"database/sql"
	"testing"
)

func TestReplacesFiles(t *testing.T) {
	cases := []struct {
		name     string
		required sql.NullInt64
		present  int64
		want     bool
	}{
		{"unknown requirement", sql.NullInt64{}, 3, false},
		{"zero required", sql.NullInt64{Valid: true}, 0, false},
		{"partly held", sql.NullInt64{Int64: 10, Valid: true}, 4, false},
		{"fully held", sql.NullInt64{Int64: 10, Valid: true}, 10, true},
	}
	for _, c := range cases {
		if got := replacesFiles(c.required, c.present); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
