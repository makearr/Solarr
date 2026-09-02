package fixture_test

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/library/fixture"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
	"github.com/makearr/MediaNexus/migrations"
)

// small keeps these tests to a second while still crossing several batches.
var small = fixture.Spec{Files: 400, Seed: 7}

func build(t *testing.T, spec fixture.Spec) string {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "library.db")
	if err := fixture.Build(t.Context(), fsx.NewOS(), path, spec); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return path
}

func openBuilt(t *testing.T, path string) *store.Store {
	t.Helper()
	opened, err := store.Open(t.Context(), clock.NewSystem(), store.Config{
		Name: "library", Path: path, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return opened
}

func count(t *testing.T, opened *store.Store, table string) int64 {
	t.Helper()
	var total int64
	// The table name is a constant at every call site, never input.
	if err := opened.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&total); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return total
}

func TestBuild_ProducesExactlyTheRequestedFileCount(t *testing.T) {
	opened := openBuilt(t, build(t, small))

	if got := count(t, opened, "media_object"); got != int64(small.Files) {
		t.Errorf("media_object rows = %d, want %d", got, small.Files)
	}
	// One path per file, and one link per file, or the corpus does not answer the
	// list queries the benchmarks measure.
	if got := count(t, opened, "library_path"); got != int64(small.Files) {
		t.Errorf("library_path rows = %d, want %d", got, small.Files)
	}
	if got := count(t, opened, "desired_item_file"); got != int64(small.Files) {
		t.Errorf("desired_item_file rows = %d, want %d", got, small.Files)
	}
}

func TestBuild_CoversEveryMediaTypeInTheMix(t *testing.T) {
	opened := openBuilt(t, build(t, small))

	rows, err := opened.Read().QueryContext(t.Context(),
		"SELECT media_type, count(*) FROM work GROUP BY media_type")
	if err != nil {
		t.Fatalf("group by media_type: %v", err)
	}
	defer func() { _ = rows.Close() }()

	found := map[string]int64{}
	for rows.Next() {
		var mediaType string
		var total int64
		if err := rows.Scan(&mediaType, &total); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found[mediaType] = total
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for _, mediaType := range []string{"music", "tv", "movie"} {
		if found[mediaType] == 0 {
			t.Errorf("no %s works: the mix produced %v", mediaType, found)
		}
	}
	if found["music"] <= found["movie"] {
		t.Errorf("music %d should outnumber film %d, as the library does",
			found["music"], found["movie"])
	}
}

// A benchmark that compares two drivers is only a comparison if both read the same
// corpus, so generation must not vary between runs.
func TestBuild_IsDeterministic(t *testing.T) {
	first := titles(t, openBuilt(t, build(t, small)))
	second := titles(t, openBuilt(t, build(t, small)))

	if len(first) != len(second) {
		t.Fatalf("work counts differ: %d and %d", len(first), len(second))
	}
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("work %d differs: %q and %q", index, first[index], second[index])
		}
	}
}

func TestBuild_ADifferentSeedProducesADifferentCorpus(t *testing.T) {
	first := titles(t, openBuilt(t, build(t, small)))
	other := small
	other.Seed = 99
	second := titles(t, openBuilt(t, build(t, other)))

	if len(first) == len(second) {
		same := true
		for index := range first {
			if first[index] != second[index] {
				same = false
				break
			}
		}
		if same {
			t.Fatal("seed 7 and seed 99 produced identical titles")
		}
	}
}

// Repeated sort titles are deliberate: they are what a cursor without an id
// tie-break silently drops (notes section 11).
func TestBuild_RepeatsSomeSortTitles(t *testing.T) {
	opened := openBuilt(t, build(t, small))

	var repeated int64
	err := opened.Read().QueryRowContext(t.Context(),
		"SELECT count(*) FROM (SELECT sort_title FROM work GROUP BY sort_title HAVING count(*) > 1)").
		Scan(&repeated)
	if err != nil {
		t.Fatalf("count repeated titles: %v", err)
	}
	if repeated == 0 {
		t.Error("no sort title repeats, so no page boundary can land inside a group")
	}
}

// ADR-35's fingerprint cursor has to have something to resume from at any scale.
func TestBuild_LeavesSomeFilesUnfingerprinted(t *testing.T) {
	opened := openBuilt(t, build(t, small))

	var pending int64
	err := opened.Read().QueryRowContext(t.Context(),
		"SELECT count(*) FROM media_object WHERE fingerprinted_at IS NULL").Scan(&pending)
	if err != nil {
		t.Fatalf("count unfingerprinted: %v", err)
	}
	if pending == 0 || pending == int64(small.Files) {
		t.Errorf("unfingerprinted files = %d of %d, want some but not all", pending, small.Files)
	}
}

// Invariant 2: generation is bulk work, and bulk work is what breaches a budget
// first. Wiring the report to the test is only possible from the caller's config.
func TestPopulate_KeepsEveryTransactionInsideTheHardBudget(t *testing.T) {
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	opened, err := store.Open(t.Context(), clock.NewSystem(), store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"),
		Logger: slog.New(slog.DiscardHandler),
		OnBudgetExceeded: func(report store.TransactionReport) {
			t.Errorf("transaction over the hard budget: %s", report)
		},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = opened.Close() }()

	owner, err := migrations.Library()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := fixture.Populate(t.Context(), opened, small); err != nil {
		t.Fatalf("Populate: %v", err)
	}
}

func TestEnsure_BuildsOnceAndReusesTheSameFile(t *testing.T) {
	directory := t.TempDir()

	path, err := fixture.Ensure(t.Context(), fsx.NewOS(), directory, small)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	marker(t, path)

	again, err := fixture.Ensure(t.Context(), fsx.NewOS(), directory, small)
	if err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	if again != path {
		t.Fatalf("second Ensure returned %s, want %s", again, path)
	}
	// A rebuild would drop the marker: a cached corpus must be reused whole, or a
	// benchmark pays the generation cost on every run.
	opened := openBuilt(t, again)
	if got := count(t, opened, "work"); got == 0 {
		t.Fatal("no works in the reused fixture")
	}
	var found int64
	if err := opened.Read().QueryRowContext(t.Context(),
		"SELECT count(*) FROM work WHERE canonical_title = 'marker'").Scan(&found); err != nil {
		t.Fatalf("look for the marker: %v", err)
	}
	if found != 1 {
		t.Errorf("marker rows = %d, want 1: the fixture was rebuilt rather than reused", found)
	}
}

func TestEnsure_NamesDifferentSpecsDifferently(t *testing.T) {
	other := small
	other.Files = small.Files * 2
	if small.FileName() == other.FileName() {
		t.Errorf("both specs name %s, so one corpus would answer for the other", small.FileName())
	}
}

func TestEnsure_RefusesARelativeDirectory(t *testing.T) {
	if _, err := fixture.Ensure(t.Context(), fsx.NewOS(), "fixtures", small); err == nil {
		t.Fatal("a relative directory was accepted")
	}
}

// marker closes the store before returning. Leaving it open would leave the marker in
// a WAL beside the file, which a rebuild would then replay into its replacement -- and
// the test could no longer tell a reuse from a rebuild.
func marker(t *testing.T, path string) {
	t.Helper()
	opened, err := store.Open(t.Context(), clock.NewSystem(), store.Config{
		Name: "library", Path: path, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	err = opened.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		_, err := librarydb.New(opened.Read()).WithTx(tx).CreateWork(ctx, librarydb.CreateWorkParams{
			MediaType: "music", CanonicalTitle: "marker", SortTitle: "marker",
			AddedAt: 1, UpdatedAt: 1,
		})
		return err
	})
	if err != nil {
		t.Fatalf("write the marker: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("close after the marker: %v", err)
	}
}

func titles(t *testing.T, opened *store.Store) []string {
	t.Helper()
	rows, err := opened.Read().QueryContext(t.Context(),
		"SELECT canonical_title FROM work ORDER BY id")
	if err != nil {
		t.Fatalf("read titles: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var found []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found = append(found, title)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return found
}
