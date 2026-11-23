package spec

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
)

// Patterns as the bundled specs write them for .NET: lookaround, back-references and Unicode block names.

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
