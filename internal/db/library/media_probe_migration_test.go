package library_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

// The probes media_object held move to media_probe with the version their content shows, so each is read again
// exactly when the old LIKE tests read it again (revision 2, D1).
func TestMigration_ProbesMoveWithTheirVersion(t *testing.T) {
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{Name: "library", Path: filepath.Join(directory, "library.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatal(err)
	}
	cut := slices.IndexFunc(owner.Migrations, func(m store.Migration) bool { return m.Version == 70 })
	before := owner
	before.Migrations = owner.Migrations[:cut]
	if err := store.Migrate(t.Context(), opened, backups, before); err != nil {
		t.Fatal(err)
	}
	for id, probe := range map[int64]string{
		1: `{"AudioCodec":"mp3"}`,
		2: `{"AudioCodec":"flac","LyricsLanguages":["de"]}`,
		3: `{"VideoCodec":"hevc","LyricsLanguages":null,"SubtitleLanguages":["en"],"CRF":22}`,
	} {
		if _, err := opened.WriteDB().ExecContext(t.Context(),
			`INSERT INTO media_object (id, size_bytes, quick_hash, probe_json, measured_attributes_id, first_seen_at, verified_at) VALUES (?, 1, ?, ?, NULL, 5, 9)`,
			id, probe, probe); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatal(err)
	}
	rows, err := opened.Read().QueryContext(t.Context(), "SELECT media_object_id, probe_version, probed_at FROM media_probe ORDER BY media_object_id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got [][3]int64
	for rows.Next() {
		var row [3]int64
		if err := rows.Scan(&row[0], &row[1], &row[2]); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := [][3]int64{{1, 1, 9}, {2, library.ProbeVersionLyrics, 9}, {3, library.ProbeVersionVideo, 9}}
	if !slices.Equal(got, want) {
		t.Errorf("media_probe = %v, want %v", got, want)
	}
}
