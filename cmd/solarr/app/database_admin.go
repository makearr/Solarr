package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// databaseAdmin answers the Database page from the folders on disk.
type databaseAdmin struct {
	FS                                                       fsx.FS
	MediaCoverDir, PreviewsDir, CacheDir, BackupDir, Scratch string
	Recompress                                               *imageRecompress
	// Apply sets the figures new pictures are made with from the settings now stored.
	Apply func()
	// Background ends with the process, so a recompress stops at shutdown.
	Background context.Context
}

func (a databaseAdmin) ImagesChanged() { a.Apply() }

func (a databaseAdmin) StartRecompress(context.Context) bool { return a.Recompress.Start(a.Background) }

func (a databaseAdmin) RecompressStatus() apiserver.RecompressStatus { return a.Recompress.Status() }

func (a databaseAdmin) Usage(ctx context.Context) (apiserver.DatabaseUsage, error) {
	var usage apiserver.DatabaseUsage
	for _, folder := range []struct {
		path string
		into *int64
	}{
		{a.MediaCoverDir, &usage.MediaCoverBytes}, {a.PreviewsDir, &usage.PreviewsBytes},
		{a.CacheDir, &usage.CacheBytes}, {a.BackupDir, &usage.BackupsBytes},
	} {
		bytes, err := folderBytes(ctx, a.FS, folder.path)
		if err != nil {
			return usage, err
		}
		*folder.into = bytes
	}
	bundles, err := store.ListBundles(a.FS, a.BackupDir)
	if err != nil {
		return usage, err
	}
	usage.BackupCount = int64(len(bundles))
	if len(bundles) > 0 {
		at := bundles[0].At
		usage.LatestBackupAt = &at
	}
	return usage, nil
}

func (a databaseAdmin) ClearOldBackups(ctx context.Context) (int64, string, error) {
	bundles, err := store.ListBundles(a.FS, a.BackupDir)
	if err != nil {
		return 0, "", err
	}
	if len(bundles) == 0 {
		return 0, "", fmt.Errorf("%w: there is no backup yet", apiserver.ErrNoValidBackup)
	}
	removed, err := store.KeepOnlyLatestBundle(ctx, a.FS, a.BackupDir, a.Scratch)
	if err != nil {
		if errors.Is(err, store.ErrInvalidBundle) {
			return 0, "", fmt.Errorf("%w: %w", apiserver.ErrNoValidBackup, err)
		}
		return 0, "", err
	}
	return int64(len(removed)), filepath.Base(bundles[0].Path), nil
}

// folderBytes adds up the files under a folder; one that does not exist yet holds nothing.
func folderBytes(ctx context.Context, fsys fsx.FS, folder string) (int64, error) {
	entries, err := fsys.ReadDir(folder)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		child := filepath.Join(folder, entry.Name)
		if entry.IsDir {
			bytes, err := folderBytes(ctx, fsys, child)
			if err != nil {
				return total, err
			}
			total += bytes
			continue
		}
		if info, err := fsys.Stat(child); err == nil {
			total += info.Size
		}
	}
	return total, nil
}
