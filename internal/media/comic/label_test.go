package comic

import "testing"

func TestReleaseLabelStatesWhatTheNameHolds(t *testing.T) {
	cases := map[string]string{
		"Night Harbor 003 (2019) (Digital).cbz":  "#3",
		"Night Harbor 001-006 (2019) (Digital)":  "#1-#6",
		"Night Harbor v02 (Digital)":             "v02",
		"Night Harbor v02-v05 (Digital)":         "v02-v05",
		"Night Harbor c120":                      "c120",
		"Night Harbor c120-c125":                 "c120-c125",
		"Night Harbor Complete (2019) (Digital)": "Complete",
		"Night Harbor":                           "",
	}
	for title, want := range cases {
		if got := ReleaseLabel(title); got != want {
			t.Errorf("ReleaseLabel(%q) = %q, want %q", title, got, want)
		}
	}
}
