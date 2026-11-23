package extract

import (
	"testing"
	"time"
)

func TestFiltersRunDotNetPatterns(t *testing.T) {
	context := FilterContext{Variables: &Variables{}, Now: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	for _, test := range []struct {
		name, in string
		args     []string
		want     string
	}{
		{"regexp", "Show.S1E2", []string{`(?i)\bS(\d)(?=E)`}, "1"},
		{"re_replace", "1 2", []string{`(?<=\d)\s+(?=\d)`, ""}, "12"},
		{"re_replace", "a1b22", []string{`(\d+)`, "<$1>"}, "a<1>b<22>"},
		{"re_replace", "one two", []string{`(?<first>\w+) (?<second>\w+)`, "${second} ${first}"}, "two one"},
		{"regexp", "abab", []string{`(ab)\1`}, "ab"},
		{"regexp", "жук", []string{`\p{IsCyrillic}+`}, "жук"},
		{"regexp", "no digits", []string{`(\d+)`}, ""},
		{"regexp", "xyz", []string{`y`}, "y"},
	} {
		got, err := ApplyFilter(test.in, test.name, test.args, context)
		if err != nil || got != test.want {
			t.Errorf("%s(%q, %v) = %q, %v; want %q", test.name, test.in, test.args, got, err, test.want)
		}
	}
}

func TestACatastrophicPatternCostsTheFieldNotTheSearch(t *testing.T) {
	context := FilterContext{Variables: &Variables{}, Now: time.Now()}
	input := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!"
	started := time.Now()
	for _, name := range []string{"regexp", "re_replace"} {
		got, err := ApplyFilter(input, name, []string{`(a+)+$`, "x"}, context)
		if err != nil || got != "" {
			t.Errorf("%s = %q, %v; want an empty field and no error", name, got, err)
		}
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("two runaway patterns took %s, want about the match timeout each", elapsed)
	}
}

// A typical row runs several pattern filters; the pattern is compiled once, not for every field of every row (IDX-5).
func BenchmarkRegexpFilter(b *testing.B) {
	context := FilterContext{Variables: &Variables{}, Now: time.Now()}
	for b.Loop() {
		if _, err := ApplyFilter("Artist - Album (2020) [FLAC] 1,234 seeders", "regexp", []string{`\((\d{4})\)`}, context); err != nil {
			b.Fatal(err)
		}
	}
}

func TestAPatternIsCompiledOnce(t *testing.T) {
	first, err := Compiled(`(?i)once-only-\d+`)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compiled(`(?i)once-only-\d+`)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Error("the same pattern compiled twice")
	}
}
