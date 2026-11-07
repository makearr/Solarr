package store_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func takeBundle(t *testing.T, fixture migrationFixture, at time.Time) string {
	t.Helper()
	config := filepath.Join(filepath.Dir(fixture.path), "config.yml")
	if err := os.WriteFile(config, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	copied, err := fixture.store.Backup(t.Context(), fixture.backupDir, store.BackupRotation)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	bundle, err := store.WriteBundle(fsx.NewOS(), fixture.backupDir, at, []store.BundleMember{
		{Name: "library.db", Path: copied}, {Name: store.ConfigMember, Path: config},
	})
	if err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	return bundle
}

func TestBundle_HoldsTheDatabaseAndConfigAndVerifies(t *testing.T) {
	fixture := newMigrationFixture(t)
	bundle := takeBundle(t, fixture, time.Unix(1_000_000, 0))

	members, err := store.BundleMembers(fsx.NewOS(), bundle)
	if err != nil {
		t.Fatalf("BundleMembers: %v", err)
	}
	if want := []string{"library.db", "config.yml"}; !slices.Equal(members, want) {
		t.Errorf("members = %v, want %v", members, want)
	}
	if err := store.VerifyBundle(t.Context(), fsx.NewOS(), bundle, t.TempDir()); err != nil {
		t.Errorf("VerifyBundle: %v", err)
	}
}

func TestVerifyBundle_RefusesADatabaseThatIsNotOne(t *testing.T) {
	fixture := newMigrationFixture(t)
	junk := filepath.Join(t.TempDir(), "library.db")
	if err := os.WriteFile(junk, []byte("this is not a sqlite database, only text padding it out to be long enough"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(config, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := store.WriteBundle(fsx.NewOS(), fixture.backupDir, time.Unix(2_000_000, 0), []store.BundleMember{
		{Name: "library.db", Path: junk}, {Name: store.ConfigMember, Path: config},
	})
	if err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	if err := store.VerifyBundle(t.Context(), fsx.NewOS(), bundle, t.TempDir()); err == nil {
		t.Fatal("a bundle holding a text file as its database verified")
	}
}

func TestKeepOnlyLatestBundle_RemovesOlderOnesOnlyWhenTheNewestIsValid(t *testing.T) {
	fixture := newMigrationFixture(t)
	oldest := takeBundle(t, fixture, time.Unix(1_000_000, 0))
	newest := takeBundle(t, fixture, time.Unix(2_000_000, 0))

	removed, err := store.KeepOnlyLatestBundle(t.Context(), fsx.NewOS(), fixture.backupDir, t.TempDir())
	if err != nil {
		t.Fatalf("KeepOnlyLatestBundle: %v", err)
	}
	if len(removed) != 1 || removed[0] != oldest {
		t.Errorf("removed = %v, want [%s]", removed, oldest)
	}
	if _, err := os.Stat(newest); err != nil {
		t.Errorf("the newest bundle is gone: %v", err)
	}
}

func TestPruneBundles_KeepsTheNewestMinimum(t *testing.T) {
	fixture := newMigrationFixture(t)
	for index := range 5 {
		takeBundle(t, fixture, time.Unix(int64(1_000_000*(index+1)), 0))
	}
	removed, err := store.PruneBundles(fsx.NewOS(), fixture.backupDir, 1, time.Time{})
	if err != nil {
		t.Fatalf("PruneBundles: %v", err)
	}
	if len(removed) != 2 {
		t.Errorf("removed %d bundles, want 2 (5 minus the floor of 3)", len(removed))
	}
}
