package spec

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
)

// Patterns as the bundled specs write them for .NET: lookaround, back-references and Unicode block names.
func TestFiltersRunDotNetPatterns(t *testing.T) {
	context := filterContext{variables: &Variables{}, now: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
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
		got, err := applyFilter(test.in, test.name, test.args, context)
		if err != nil || got != test.want {
			t.Errorf("%s(%q, %v) = %q, %v; want %q", test.name, test.in, test.args, got, err, test.want)
		}
	}
}

func TestACatastrophicPatternCostsTheFieldNotTheSearch(t *testing.T) {
	context := filterContext{variables: &Variables{}, now: time.Now()}
	input := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!"
	started := time.Now()
	for _, name := range []string{"regexp", "re_replace"} {
		got, err := applyFilter(input, name, []string{`(a+)+$`, "x"}, context)
		if err != nil || got != "" {
			t.Errorf("%s = %q, %v; want an empty field and no error", name, got, err)
		}
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("two runaway patterns took %s, want about the match timeout each", elapsed)
	}
}

// Every pattern the bundled specs state outright must compile (IDX-4: 63 specs used features Go's engine rejects).
func TestBundledSpecsCompile(t *testing.T) {
	specs, err := All()
	if err != nil {
		t.Fatal(err)
	}
	broken := map[string]int{}
	for _, one := range specs {
		for _, reason := range one.Unsupported() {
			if reason == "a regular expression Solarr cannot read" {
				broken[one.ID]++
			}
		}
	}
	if len(broken) != 0 {
		t.Errorf("%d specs state a pattern the engine cannot compile: %v", len(broken), broken)
	}
}

func TestSearchSkipsARowAFilterCannotReadAndFailsWhenNoRowCan(t *testing.T) {
	const page = `<table><tr class="r"><td class="n"><a href="/t/1">fine</a></td><td class="p">(unclosed</td></tr>
<tr class="r"><td class="n"><a href="/t/2">other</a></td><td class="p">o</td></tr></table>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(page)) }))
	defer server.Close()
	spec := htmlSpec()
	// The first row's title field is built from a pattern the page supplies.
	spec.Search.Fields = []Extractor{
		{Name: "pattern", Selector: "td.p"},
		{Name: "title", Selector: "td.n a", Filters: []Filter{{Name: "regexp", Args: []string{"{{ .Result.pattern }}"}}}},
		{Name: "download", Selector: "td.n a", Attribute: "href"},
	}
	driver := testDriver(t, spec, server, nil)
	items, err := driver.Search(t.Context(), indexer.SearchRequest{Query: "x", Categories: []int{3000}})
	if err != nil || len(items) != 1 || items[0].Title != "o" {
		t.Fatalf("items = %+v, %v; want the readable row only", items, err)
	}

	broken := `<table><tr class="r"><td class="n"><a href="/t/1">fine</a></td><td class="p">(unclosed</td></tr></table>`
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(broken)) }))
	defer server2.Close()
	if items, err := testDriver(t, spec, server2, nil).Search(t.Context(), indexer.SearchRequest{Query: "x", Categories: []int{3000}}); err == nil {
		t.Errorf("items = %+v, nil; want the filter's error when no row could be read", items)
	}
}

// A typical row runs several pattern filters; the pattern is compiled once, not for every field of every row (IDX-5).
func BenchmarkRegexpFilter(b *testing.B) {
	context := filterContext{variables: &Variables{}, now: time.Now()}
	for b.Loop() {
		if _, err := applyFilter("Artist - Album (2020) [FLAC] 1,234 seeders", "regexp", []string{`\((\d{4})\)`}, context); err != nil {
			b.Fatal(err)
		}
	}
}

func TestAPatternIsCompiledOnce(t *testing.T) {
	first, err := compiled(`(?i)once-only-\d+`)
	if err != nil {
		t.Fatal(err)
	}
	second, err := compiled(`(?i)once-only-\d+`)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Error("the same pattern compiled twice")
	}
}
