package app

import (
	"errors"
	"io"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func TestBackupSourceLatestOpensTheNewestBundleAndSaysWhenThereIsNone(t *testing.T) {
	memory := fsx.NewMem(clock.NewFakeAtEpoch())
	source := backupSource{FS: memory, Directory: "/backups"}
	if _, err := source.Latest(t.Context()); !errors.Is(err, apiserver.ErrNoBackup) {
		t.Fatalf("Latest with no backup = %v, want ErrNoBackup", err)
	}
	if err := memory.MkdirAll("/backups", 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"solarr-backup.2026-11-01T10-00-00Z.tar.zst": "old",
		"solarr-backup.2026-11-02T10-00-00Z.tar.zst": "newest",
	} {
		file, err := memory.Create("/backups/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, content); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	download, err := source.Latest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = download.Content.Close() }()
	body, _ := io.ReadAll(download.Content)
	if download.Name != "solarr-backup.2026-11-02T10-00-00Z.tar.zst" || download.SizeBytes != 6 || string(body) != "newest" {
		t.Errorf("Latest = %q %d bytes %q, want the newest bundle", download.Name, download.SizeBytes, body)
	}
}
