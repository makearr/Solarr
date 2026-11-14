package fixture_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/library/fixture"
)

// innerScanLimit is the table size above which a scan on the inner side of a join costs a user noticeably.
const innerScanLimit = 500

var (
	generatedStatement = regexp.MustCompile("(?s)const (\\w+) = `-- name: \\w+ :\\w+\\n(.*?)`")
	scannedTable       = regexp.MustCompile(`^SCAN (\w+)`)
)

// A scan of a big table inside a join runs once per outer row; statistics gone stale made these appear (DB-10).
func TestEveryStatement_NeverScansABigTableInsideAJoin(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 20,000-file library")
	}
	opened := openBuilt(t, build(t, fixture.Spec{Files: 20_000, Seed: 3}))
	if err := opened.Analyze(t.Context()); err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	files, err := filepath.Glob("../../store/gen/librarydb/*.sql.gen.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no generated statements found: %v", err)
	}
	sizes := map[string]int64{}
	size := func(table string) int64 {
		if rows, known := sizes[table]; known {
			return rows
		}
		sizes[table] = count(t, opened, table)
		return sizes[table]
	}

	checked := 0
	for _, file := range files {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range generatedStatement.FindAllStringSubmatch(string(source), -1) {
			name, statement := match[1], match[2]
			if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statement)), "SELECT") &&
				!strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statement)), "WITH") {
				continue
			}
			plan, err := opened.ExplainQueryPlan(t.Context(), statement, nullArguments(statement)...)
			if err != nil {
				continue // a statement SQLite cannot plan without its arguments is not one this guard can judge
			}
			checked++
			for _, table := range innerScans(plan) {
				if rows := size(table); rows > innerScanLimit && !knownInnerScans[name+" "+table] {
					t.Errorf("%s scans %s (%d rows) inside a join:\n%s", name, table, rows, plan)
				}
			}
		}
	}
	if checked < 100 {
		t.Fatalf("checked %d statements, want the generated set (a pattern change would hide them all)", checked)
	}
}

// innerScans are the tables a plan scans after an earlier step at the same level: the inner side of a join.
func innerScans(plan string) []string {
	var tables []string
	seen := map[int]bool{}
	for _, line := range strings.Split(strings.TrimRight(plan, "\n"), "\n") {
		depth := (len(line) - len(strings.TrimLeft(line, " "))) / 2
		for deeper := range seen {
			if deeper > depth {
				delete(seen, deeper)
			}
		}
		detail := strings.TrimSpace(line)
		if match := scannedTable.FindStringSubmatch(detail); match != nil && seen[depth] && !strings.Contains(detail, "VIRTUAL") {
			tables = append(tables, match[1])
		}
		if strings.HasPrefix(detail, "SCAN ") || strings.HasPrefix(detail, "SEARCH ") {
			seen[depth] = true
		}
	}
	return tables
}

func nullArguments(statement string) []any {
	return make([]any, strings.Count(statement, "?"))
}

// knownInnerScans are statements judged by hand to be right: "<statement> <table>".
var knownInnerScans = map[string]bool{}

func TestInnerScans_NamesOnlyAScanAfterAnEarlierStepAtTheSameLevel(t *testing.T) {
	plan := "SCAN mo\nSCAN ma LEFT-JOIN\nSEARCH f USING INTEGER PRIMARY KEY (rowid=?)\nLIST SUBQUERY 1\n  SCAN json_each VIRTUAL TABLE INDEX 1:\n"
	got := innerScans(plan)
	if len(got) != 1 || got[0] != "ma" {
		t.Errorf("innerScans = %v, want only ma: the first scan is the outer side and a subquery has its own level", got)
	}
}
