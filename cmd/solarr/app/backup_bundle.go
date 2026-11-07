package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// stagingDirName holds the database copies while they are checked, before they are packed.
const stagingDirName = ".staging"

// bundleBackup takes one rotation backup: a copy of every database, each checked, packed with config.yml.
type bundleBackup struct {
	FS         fsx.FS
	Directory  string
	ConfigPath string
	Stores     []*store.Store
	Clock      clock.Clock
}

// Take answers the bundle's path; any copy that fails its check fails the whole backup and leaves no bundle.
func (b bundleBackup) Take(ctx context.Context) (string, error) {
	staging := filepath.Join(b.Directory, stagingDirName)
	if err := b.FS.MkdirAll(staging, 0o700); err != nil {
		return "", fmt.Errorf("backup: create %s: %w", staging, err)
	}
	defer b.clearStaging(staging)

	members := make([]store.BundleMember, 0, len(b.Stores)+1)
	for _, target := range b.Stores {
		copied, err := target.Backup(ctx, staging, store.BackupRotation)
		if err != nil {
			return "", fmt.Errorf("back up %s: %w", target.Name(), err)
		}
		if err := store.VerifyFile(ctx, b.FS, target.Name(), copied); err != nil {
			return "", fmt.Errorf("the backup of %s is not valid: %w", target.Name(), err)
		}
		members = append(members, store.BundleMember{Name: target.Name() + ".db", Path: copied})
	}
	members = append(members, store.BundleMember{Name: store.ConfigMember, Path: b.ConfigPath})
	return store.WriteBundle(b.FS, b.Directory, b.Clock.Now(), members)
}

func (b bundleBackup) clearStaging(staging string) {
	entries, err := b.FS.ReadDir(staging)
	if err != nil {
		return
	}
	for _, entry := range entries {
		_ = b.FS.Remove(filepath.Join(staging, entry.Name))
	}
}
