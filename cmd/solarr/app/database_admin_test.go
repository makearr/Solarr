package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func TestDatabaseAdmin_UsageAddsUpEachFolderAndMissingOnesAreEmpty(t *testing.T) {
	root := t.TempDir()
	covers := filepath.Join(root, "mediacover")
	if err := os.MkdirAll(filepath.Join(covers, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, size := range map[string]int{filepath.Join(covers, "a"): 10, filepath.Join(covers, "nested", "b"): 5} {
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	admin := databaseAdmin{FS: fsx.NewOS(), MediaCoverDir: covers, PreviewsDir: filepath.Join(root, "none"), CacheDir: filepath.Join(root, "none"), BackupDir: filepath.Join(root, "none")}

	usage, err := admin.Usage(t.Context())
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if usage.MediaCoverBytes != 15 || usage.PreviewsBytes != 0 || usage.BackupCount != 0 || usage.LatestBackupAt != nil {
		t.Errorf("usage = %+v", usage)
	}
}

func TestDatabaseAdmin_ClearOldBackupsRefusesWithoutAValidNewest(t *testing.T) {
	root := t.TempDir()
	admin := databaseAdmin{FS: fsx.NewOS(), BackupDir: root, Scratch: filepath.Join(root, ".staging")}
	if _, _, err := admin.ClearOldBackups(t.Context()); !errors.Is(err, apiserver.ErrNoValidBackup) {
		t.Errorf("err = %v, want ErrNoValidBackup for an empty folder", err)
	}
}
