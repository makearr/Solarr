package migrations_test

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Every database starts from one squashed baseline; a mangled marker would make an existing database
// refuse to start (ErrMigrationChanged), so the checksum it carries is checked for shape here.
func TestEveryDatabase_StartsFromABaseline(t *testing.T) {
	for name, load := range map[string]func() (store.Owner, error){
		"settings": migrations.Settings, "jobs": migrations.Jobs, "library": migrations.Library,
		"metadata": migrations.Metadata, "statistics": migrations.Statistics,
	} {
		owner, err := load()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		first := owner.Migrations[0]
		if !sha256Hex.MatchString(first.Replaces) {
			t.Errorf("%s: first migration %d replaces %q, want a sha256 checksum", name, first.Version, first.Replaces)
		}
		for _, later := range owner.Migrations[1:] {
			if later.Replaces != "" {
				t.Errorf("%s: migration %d is a second baseline", name, later.Version)
			}
		}
	}
}

// A foreign key column without an index makes every delete of the parent row scan the whole child table.
func TestEveryForeignKeyColumn_LeadsAnIndex(t *testing.T) {
	// Configuration tables hold a few dozen rows, so the scan costs nothing.
	const configuration = "settings.db holds a few dozen rows per table"
	allowed := map[string]string{}
	for _, column := range []string{
		"category.library_id", "category.quality_cutoff_profile_id", "category.naming_profile_id",
		"category.language_profile_id", "category.quality_profile_id",
		"import_list.quality_profile_id", "download_folder.category_id", "category_quality_profile.quality_profile_id",
		"arr_instance.category_id", "quality_target.profile_id", "category_target.target_id",
	} {
		allowed["settings."+column] = configuration
	}

	for name, load := range map[string]func() (store.Owner, error){
		"settings": migrations.Settings, "jobs": migrations.Jobs, "library": migrations.Library,
		"metadata": migrations.Metadata, "statistics": migrations.Statistics,
	} {
		owner, err := load()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
			Name: name, Path: filepath.Join(t.TempDir(), name+".db"), ReadPoolSize: 2,
			Logger: slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatalf("%s: open: %v", name, err)
		}
		t.Cleanup(func() { _ = opened.Close() })
		if err := store.Migrate(t.Context(), opened, t.TempDir(), owner); err != nil {
			t.Fatalf("%s: migrate: %v", name, err)
		}
		for _, missing := range unindexedForeignKeys(t.Context(), t, opened.Read()) {
			if _, ok := allowed[name+"."+missing]; !ok {
				t.Errorf("%s: foreign key column %s leads no index", name, missing)
			}
		}
	}
}

func unindexedForeignKeys(ctx context.Context, t *testing.T, db *sql.DB) []string {
	t.Helper()
	tables := queryStrings(ctx, t, db, "SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")
	var missing []string
	for _, table := range tables {
		columns := queryStrings(ctx, t, db, "SELECT \"from\" FROM pragma_foreign_key_list(?)", table)
		for _, column := range columns {
			var leading int
			err := db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_index_list(?1) AS list
				JOIN pragma_index_info(list.name) AS info ON info.seqno = 0 WHERE info.name = ?2`, table, column).Scan(&leading)
			if err != nil {
				t.Fatal(err)
			}
			var primary int
			err = db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?1)
				WHERE name = ?2 AND pk = 1 AND (SELECT count(*) FROM pragma_table_info(?1) WHERE pk > 0) = 1`, table, column).Scan(&primary)
			if err != nil {
				t.Fatal(err)
			}
			if leading+primary == 0 {
				missing = append(missing, table+"."+column)
			}
		}
	}
	return missing
}

func queryStrings(ctx context.Context, t *testing.T, db *sql.DB, query string, arguments ...any) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx, query, arguments...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

// The TMDB chart import lists become Discover settings, popular as trending; other lists and libraries are left alone.
func TestSettingsMigration_FoldsChartListsIntoDiscover(t *testing.T) {
	ctx := t.Context()
	owner, err := migrations.Settings()
	if err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(ctx, clock.NewFakeAtEpoch(), store.Config{
		Name: "settings", Path: filepath.Join(t.TempDir(), "settings.db"), ReadPoolSize: 2, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	before := owner
	before.Migrations = owner.Migrations[:1]
	if err := store.Migrate(ctx, opened, t.TempDir(), before); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO category (id, media_type, name, monitor_release_types) VALUES (1, 'tv', 'Shows', ''), (2, 'tv', 'Anime', ''), (3, 'tv', 'Other', '')`,
		`INSERT INTO import_list (category_id, name, type, enabled, search_on_add) VALUES
		 (1, 'Trending', 'tmdb_trending_tv', 1, 1), (1, 'Popular', 'tmdb_popular_tv', 1, 0),
		 (2, 'Popular', 'tmdb_popular_tv', 0, 0), (3, 'Mine', 'tmdb_list', 1, 0)`,
	} {
		if _, err := opened.Read().ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Migrate(ctx, opened, t.TempDir(), owner); err != nil {
		t.Fatal(err)
	}
	read := func(query string) []string { return queryStrings(ctx, t, opened.Read(), query) }
	if got, want := read(`SELECT category_id || ':' || enabled || ':' || auto_add_search FROM category_discover ORDER BY category_id`), []string{"1:1:1", "2:1:0"}; !slices.Equal(got, want) {
		t.Errorf("category_discover = %v, want %v", got, want)
	}
	if got, want := read(`SELECT category_id || ':' || type || ':' || auto_add FROM category_discover_type ORDER BY category_id`), []string{"1:trending:1", "2:trending:0"}; !slices.Equal(got, want) {
		t.Errorf("category_discover_type = %v, want %v", got, want)
	}
	if got := read(`SELECT value_json FROM instance_setting WHERE key = 'discover.enabled'`); !slices.Equal(got, []string{"true"}) {
		t.Errorf("discover.enabled = %v", got)
	}
	if got := read(`SELECT name FROM import_list ORDER BY name`); !slices.Equal(got, []string{"Mine"}) {
		t.Errorf("import lists left = %v", got)
	}
	if got := read(`SELECT name FROM sqlite_master WHERE name = 'discovery_job'`); len(got) != 0 {
		t.Errorf("discovery_job still exists: %v", got)
	}
}
