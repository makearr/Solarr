package settings_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

// The tasks folded into one per metadata pass keep the music task's overrides under the new key; the TV ones go.
func TestMigration_SchedulesFoldIntoOnePerPass(t *testing.T) {
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
	last := slices.IndexFunc(owner.Migrations, func(m store.Migration) bool { return m.Version == 39 })
	if last < 0 {
		t.Fatal("no migration 39")
	}
	before := owner
	before.Migrations = owner.Migrations[:last]
	if err := store.Migrate(t.Context(), opened, backups, before); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"schedule.enrichment.cadence":           `["5 * * * *"]`,
		"schedule.refresh_artists.enabled":      "false",
		"schedule.artwork.run_at_startup":       "false",
		"schedule.refresh_series.enabled":       "false",
		"schedule.tv_artwork.cadence":           `["7 * * * *"]`,
		"schedule.hunt.enabled":                 "false",
		"schedule.refresh_works.run_at_startup": "false",
		"schedule.enrichment.run_at_startup":    "true",
	} {
		if _, err := opened.WriteDB().ExecContext(t.Context(), "INSERT INTO instance_setting (key, value_json, updated_at) VALUES (?, ?, 0)", key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatal(err)
	}

	rows, err := opened.Read().QueryContext(t.Context(), "SELECT key, value_json FROM instance_setting WHERE key LIKE 'schedule.%' ORDER BY key")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatal(err)
		}
		got[key] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"schedule.refresh_works.cadence":        `["5 * * * *"]`,
		"schedule.refresh_works.run_at_startup": "false",
		"schedule.refresh_people.enabled":       "false",
		"schedule.pictures.run_at_startup":      "false",
		"schedule.hunt.enabled":                 "false",
	}
	if len(got) != len(want) {
		t.Fatalf("schedule settings = %v, want %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q (all: %v)", key, got[key], value, got)
		}
	}
	cache, err := settings.NewCache(t.Context(), opened)
	if err != nil {
		t.Fatal(err)
	}
	if cache.Snapshot().ScheduleEnabled("refresh_people") {
		t.Error("refresh_people enabled, want the refresh_artists override carried over")
	}
}
