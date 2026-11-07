package config_test

import (
	"io"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

const legacyDocument = `version: 1
paths:
  config_dir: /config
  data_dir: /config/data
  db_dir: /config/data/db
  cache_dir: /custom/cache
  backup_dir: /config/data/backups
`

func TestMigrateLegacyLayout_MovesTheOldFoldersAndRewritesConfig(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	for _, path := range []string{"/config/data/db", "/config/data/backups"} {
		if err := fsys.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	if err := fsx.WriteAtomic(fsys, "/config/config.yml", func(w io.Writer) error {
		_, err := w.Write([]byte(legacyDocument))
		return err
	}); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := config.Load(fsys, "/config/config.yml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	migrated, moves, err := config.MigrateLegacyLayout(fsys, "/config/config.yml", loaded)
	if err != nil {
		t.Fatalf("MigrateLegacyLayout: %v", err)
	}
	want := []config.Move{{From: "/config/data/db", To: "/config/db"}, {From: "/config/data/backups", To: "/config/backups"}}
	if len(moves) != len(want) || moves[0] != want[0] || moves[1] != want[1] {
		t.Errorf("moves = %v, want %v", moves, want)
	}
	if migrated.Paths.DBDir != "/config/db" || migrated.Paths.CacheDir != "/custom/cache" || migrated.Paths.BackupDir != "/config/backups" {
		t.Errorf("paths = %+v", migrated.Paths)
	}
	if _, err := fsys.Stat("/config/db"); err != nil {
		t.Errorf("/config/db was not created: %v", err)
	}
	file, err := fsys.Open("/config/config.yml")
	if err != nil {
		t.Fatalf("open config: %v", err)
	}
	defer func() { _ = file.Close() }()
	data, _ := io.ReadAll(file)
	text := string(data)
	if !strings.Contains(text, "db_dir: /config/db") || !strings.Contains(text, "cache_dir: /custom/cache") {
		t.Errorf("config.yml = %q", text)
	}
}

func TestMigrateLegacyLayout_RefusesWhenBothFoldersExist(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	for _, path := range []string{"/config/data/db", "/config/db"} {
		if err := fsys.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	loaded := config.Default()
	if _, _, err := config.MigrateLegacyLayout(fsys, "/config/config.yml", func() config.Config {
		loaded.Paths.DBDir = "/config/data/db"
		return loaded
	}()); err == nil {
		t.Fatal("two db folders were merged silently")
	}
}

func TestParse_MediaCoverDirFollowsConfigDirWhenTheFileDoesNotNameIt(t *testing.T) {
	parsed, err := config.Parse([]byte("version: 1\npaths:\n  config_dir: /srv/solarr\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Paths.MediaCoverDir != "/srv/solarr/mediacover" {
		t.Errorf("MediaCoverDir = %q, want /srv/solarr/mediacover", parsed.Paths.MediaCoverDir)
	}
}
