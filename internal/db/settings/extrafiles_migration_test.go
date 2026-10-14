package settings_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

// The instance-wide switch becomes each stored library's own, except a TV library's, which takes its type's default.
func TestMigration_ExtraFilesMoveOntoEachLibrary(t *testing.T) {
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{Name: "settings", Path: filepath.Join(directory, "settings.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Settings()
	if err != nil {
		t.Fatal(err)
	}
	last := slices.IndexFunc(owner.Migrations, func(m store.Migration) bool { return m.Version == 40 })
	if last < 0 {
		t.Fatal("no migration 40")
	}
	before := owner
	before.Migrations = owner.Migrations[:last]
	if err := store.Migrate(t.Context(), opened, backups, before); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO category (id, media_type, name) VALUES (1, 'music', 'Music'), (2, 'tv', 'Series'), (3, 'music', 'Untouched')`,
		`INSERT INTO category_file_management (category_id) VALUES (1), (2)`,
		`INSERT INTO instance_setting (key, value_json, updated_at) VALUES ('import.extra_files', 'true', 0),
		   ('import.extra_file_extensions', '["lrc","srt"]', 0)`,
	} {
		if _, err := opened.WriteDB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatal(err)
	}

	cache, err := settings.NewCache(t.Context(), opened)
	if err != nil {
		t.Fatal(err)
	}
	on := true
	want := map[string]settings.ExtraFiles{
		"Music":     {Import: &on, Extensions: []string{"lrc", "srt"}},
		"Series":    {},
		"Untouched": {},
	}
	for _, category := range cache.Snapshot().Categories() {
		if got := category.FileManagement.ExtraFiles; !reflect.DeepEqual(got, want[category.Name]) {
			t.Errorf("%s: %+v, want %+v", category.Name, got, want[category.Name])
		}
	}
	var left int
	if err := opened.Read().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM instance_setting WHERE key LIKE 'import.extra%'`).Scan(&left); err != nil || left != 0 {
		t.Errorf("instance-wide keys left: %d, %v", left, err)
	}
}
