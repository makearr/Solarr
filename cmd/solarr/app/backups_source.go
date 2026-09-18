package app

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// backupSource satisfies apiserver.Backups over the backup directory and the
// open stores. It lives here rather than in adapters because it needs the
// *store.Store values themselves, which the adapters layer deliberately
// cannot reach -- every other adapter goes through db/library or db/settings.
// It reads the directory rather than a record of what was taken: a backup
// that is not on disk is not a backup.
type backupSource struct {
	FS        fsx.FS
	Directory string
	// Stores is what RunNow backs up, in the order it backs them up.
	Stores []*store.Store
}

// List answers every backup file present, newest first.
func (a backupSource) List(context.Context) ([]apiserver.BackupFileRow, error) {
	entries, err := a.FS.ReadDir(a.Directory)
	if err != nil {
		return nil, fmt.Errorf("adapters: read backup directory %q: %w", a.Directory, err)
	}
	rows := make([]apiserver.BackupFileRow, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		info, err := a.FS.Stat(path.Join(a.Directory, entry.Name))
		if err != nil {
			// A file that vanished between the listing and the stat is not an
			// error worth failing the whole screen for.
			continue
		}
		rows = append(rows, apiserver.BackupFileRow{
			Name: entry.Name, SizeBytes: info.Size, TakenAt: info.ModTime,
			PreMigration: strings.Contains(entry.Name, string(store.BackupPreMigration)),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TakenAt.After(rows[j].TakenAt) })
	return rows, nil
}

// RunNow backs up every store and answers what it wrote. A failure on any
// store fails the whole request: reporting a partial backup as a backup is
// how someone discovers at restore time that half of it is missing.
func (a backupSource) RunNow(ctx context.Context) ([]apiserver.BackupFileRow, error) {
	written := make([]apiserver.BackupFileRow, 0, len(a.Stores))
	for _, target := range a.Stores {
		backupPath, err := target.Backup(ctx, a.Directory, store.BackupRotation)
		if err != nil {
			return nil, fmt.Errorf("adapters: back up: %w", err)
		}
		info, err := a.FS.Stat(backupPath)
		if err != nil {
			return nil, fmt.Errorf("adapters: stat backup %q: %w", backupPath, err)
		}
		written = append(written, apiserver.BackupFileRow{
			Name: path.Base(backupPath), SizeBytes: info.Size, TakenAt: info.ModTime,
		})
	}
	return written, nil
}
