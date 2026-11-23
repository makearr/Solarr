package comic

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
)

func wantedIssues(numbers ...int) wanted {
	w := wanted{kind: PartIssue, title: "Night Harbor", year: 2019, missing: map[int]string{}, names: map[string]bool{"nightharbor": true, "harborlights": true}}
	for _, number := range numbers {
		w.missing[number] = ""
	}
	return w
}

func TestAReleaseIsWantedWhenItNamesTheSeriesAndAWantedPart(t *testing.T) {
	const mib = 1 << 20
	cases := []struct {
		name  string
		w     wanted
		title string
		size  int64
		parts []int
		held  int
		ok    bool
	}{
		{"the wanted issue", wantedIssues(3), "Night Harbor 003 (2019) (Digital) (Group).cbz", 30 * mib, []int{3}, 1, true},
		{"another issue", wantedIssues(3), "Night Harbor 004 (2019) (Digital).cbz", 30 * mib, nil, 0, false},
		{"a range holding the wanted one", wantedIssues(3), "Night Harbor 001-006 (2019) (Digital)", 180 * mib, []int{3}, 6, true},
		{"a series that starts the same", wantedIssues(3), "Night Harbor Rising 003 (2019)", 30 * mib, nil, 0, false},
		{"an alias", wantedIssues(3), "Harbor Lights 003 (2019)", 30 * mib, []int{3}, 1, true},
		{"the wrong decade", wantedIssues(3), "Night Harbor 003 (1999)", 30 * mib, nil, 0, false},
		{"a volume", wanted{kind: PartVolume, manga: true, title: "Night Harbor", missing: map[int]string{2: ""}, names: map[string]bool{"nightharbor": true}}, "Night Harbor v02 (Digital)", 80 * mib, []int{2}, 1, true},
		{"a chapter is not a volume", wanted{kind: PartVolume, manga: true, title: "Night Harbor", missing: map[int]string{2: ""}, names: map[string]bool{"nightharbor": true}}, "Night Harbor c002", 10 * mib, nil, 0, false},
		{"a complete pack holds every wanted part", wantedIssues(3, 5), "Night Harbor Complete (2019) (Digital)", 300 * mib, []int{3, 5}, 2, true},
		{"a complete pack of another series", wantedIssues(3, 5), "Dusk Harbor Complete (2019)", 300 * mib, nil, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := comicSpec{}.Read(c.w, indexer.RawItem{Title: c.title, SizeBytes: c.size}, judge.Conn{}, time.Time{})
			if ok != c.ok || !reflect.DeepEqual(got.Parts, c.parts) || got.Held != c.held {
				t.Fatalf("Read = parts %v held %d ok %v, want %v %d %v", got.Parts, got.Held, ok, c.parts, c.held, c.ok)
			}
		})
	}
}

func TestSearchTermsNameTheSeriesAndTheNumber(t *testing.T) {
	var got [][]string
	for _, tier := range (comicSpec{}).Queries(wantedIssues(1, 2)) {
		var terms []string
		for _, query := range tier {
			terms = append(terms, query.Text)
		}
		got = append(got, terms)
	}
	want := [][]string{
		{"Night Harbor 001", "Night Harbor 002"},
		{"Night Harbor #1", "Night Harbor #2"},
		{"Night Harbor 001 2019", "Night Harbor 002 2019"},
		{"Night Harbor 2019"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("queries = %v, want %v", got, want)
	}
}
