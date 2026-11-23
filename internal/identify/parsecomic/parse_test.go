package parsecomic

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// render lists every non-zero field, so a golden line states the whole value.
func render(c Claim) string {
	var parts []string
	add := func(k string, v any) {
		switch x := v.(type) {
		case string:
			if x != "" {
				parts = append(parts, k+"="+x)
			}
		case bool:
			if x {
				parts = append(parts, k)
			}
		case int:
			if x != 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", k, x))
			}
		}
	}
	add("series", c.Series)
	add("year", c.Year)
	add("issue", c.IssueNumber)
	add("issueEnd", c.IssueEnd)
	add("vol", c.Volume)
	add("volEnd", c.VolumeEnd)
	add("ch", c.Chapter)
	add("chEnd", c.ChapterEnd)
	add("format", c.Format)
	add("publisher", c.Publisher)
	add("source", c.Source)
	add("group", c.Group)
	add("edition", c.Edition)
	add("lang", c.Language)
	add("px", c.PageSizeHint)
	add("ext", c.Extension)
	add("of", c.OfTotal)
	add("complete", c.Complete)
	return strings.Join(parts, "; ")
}

func TestGoldenCorpus(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.txt")
	lines := map[string]int{}
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(fh)
		for n := 1; sc.Scan(); n++ {
			line := sc.Text()
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			name, want, ok := strings.Cut(line, " =>")
			if !ok {
				t.Fatalf("%s:%d: no ' =>'", f, n)
			}
			want = strings.TrimPrefix(want, " ")
			lines[filepath.Base(f)]++
			if got := render(Parse(name)); got != want {
				t.Errorf("%s:%d %q\n got  %s\n want %s", filepath.Base(f), n, name, got, want)
			}
		}
		_ = fh.Close()
	}
	if lines["comics.txt"] < 100 || lines["manga.txt"] < 40 {
		t.Fatalf("corpus too small: %v", lines)
	}
}
