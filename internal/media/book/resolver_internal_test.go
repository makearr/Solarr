package book

import "testing"

func TestYearsAgree(t *testing.T) {
	cases := []struct {
		name            string
		wanted, held    int
		audiobook, want bool
	}{
		{"ebook reprint decades later", 2019, 1962, false, false},
		{"ebook next year", 2020, 2019, false, true},
		{"ebook two years apart", 2021, 2019, false, false},
		{"ebook unknown file year", 0, 1962, false, true},
		{"ebook unknown held year", 2019, 0, false, true},
		{"audiobook recording year", 2019, 1962, true, true},
	}
	for _, c := range cases {
		if got := yearsAgree(c.wanted, c.held, c.audiobook); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
