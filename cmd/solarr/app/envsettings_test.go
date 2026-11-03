package app

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/secrets"
	"github.com/makearr/Solarr/migrations"
)

func envProcess(t *testing.T, pairs ...string) *process {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "settings", Path: filepath.Join(directory, "settings.db"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Settings()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	cache, err := settings.NewCache(t.Context(), opened)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)

	values := map[string]string{}
	for index := 0; index+1 < len(pairs); index += 2 {
		values[pairs[index]] = pairs[index+1]
	}
	return &process{
		cache: cache, logger: slog.New(slog.DiscardHandler), config: config.Default(),
		env: func(name string) (string, bool) { value, found := values[name]; return value, found },
	}
}

func TestApplyEnvSettings_AnEnvironmentKeyReplacesTheStoredOne(t *testing.T) {
	p := envProcess(t, "SOLARR_TMDB_API_KEY", "from-env")
	if err := p.cache.SetProviderKey(t.Context(), clock.NewFakeAtEpoch(), settings.TVSourceTMDB, "from-the-screen"); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}

	if err := p.applyEnvSettings(t.Context()); err != nil {
		t.Fatalf("applyEnvSettings: %v", err)
	}

	got, err := p.cache.ProviderKey(settings.TVSourceTMDB)
	if err != nil || string(got) != "from-env" {
		t.Errorf("stored TMDB key = %q, %v, want the environment's", string(got), err)
	}
}

func TestApplyEnvSettings_AnUnsetKeyLeavesTheStoredOneAlone(t *testing.T) {
	p := envProcess(t)
	if err := p.cache.SetProviderKey(t.Context(), clock.NewFakeAtEpoch(), settings.TVSourceTMDB, "from-the-screen"); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}

	if err := p.applyEnvSettings(t.Context()); err != nil {
		t.Fatalf("applyEnvSettings: %v", err)
	}

	got, _ := p.cache.ProviderKey(settings.TVSourceTMDB)
	if string(got) != "from-the-screen" {
		t.Errorf("stored TMDB key = %q, want it untouched", string(got))
	}
}

func TestApplyEnvSettings_RootFoldersAreAddedOnceAndNeverRemoved(t *testing.T) {
	p := envProcess(t, "SOLARR_ROOT_FOLDERS", "Music=/media/music, /media/tv")
	if _, err := p.cache.CreateRootFolder(t.Context(), "/media/old", ""); err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}

	for range 2 {
		if err := p.applyEnvSettings(t.Context()); err != nil {
			t.Fatalf("applyEnvSettings: %v", err)
		}
	}

	var paths []string
	for _, root := range p.cache.Snapshot().RootFolders() {
		paths = append(paths, root.Path)
	}
	slices.Sort(paths)
	if want := []string{"/media/music", "/media/old", "/media/tv"}; !slices.Equal(paths, want) {
		t.Errorf("root folders = %v, want %v", paths, want)
	}
}

func TestApplyEnvSettings_APortFromTheEnvironmentIsWrittenToTheNetworkPolicy(t *testing.T) {
	p := envProcess(t, "SOLARR_PORT", "6000", "SOLARR_PROXY_URL", "socks5://10.0.0.2:1080")
	p.config.Server.Port = 6000

	if err := p.applyEnvSettings(t.Context()); err != nil {
		t.Fatalf("applyEnvSettings: %v", err)
	}

	network := p.cache.Snapshot().NetworkSettings()
	if network.Port != 6000 || !network.ProxyEnabled || network.ProxyURL != "socks5://10.0.0.2:1080" {
		t.Errorf("network = %+v, want port 6000 and the proxy enabled", network)
	}
}

func TestApplyEnvSettings_ARelativeRootFolderRefusesStartup(t *testing.T) {
	p := envProcess(t, "SOLARR_ROOT_FOLDERS", "media/music")

	if err := p.applyEnvSettings(t.Context()); err == nil {
		t.Error("applyEnvSettings accepted a relative root folder")
	}
}
