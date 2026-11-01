package model

import "testing"

func TestAcceptsFolderMatch(t *testing.T) {
	at := func(floor float64) func() float64 { return func() float64 { return floor } }
	cases := []struct {
		name       string
		confidence float64
		threshold  func() float64
		want       bool
	}{
		{"loose clears the default", FolderLooseMatch, nil, true},
		{"loose clears 0.90", FolderLooseMatch, at(0.9), true},
		{"loose refused by 0.95", FolderLooseMatch, at(0.95), false},
		{"settled clears 0.95", FolderSettledMatch, at(0.95), true},
		{"settled refused by 1.0", FolderSettledMatch, at(1), false},
		{"exact clears 1.0", FolderExactMatch, at(1), true},
	}
	for _, c := range cases {
		if got := AcceptsFolderMatch(c.confidence, c.threshold); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
