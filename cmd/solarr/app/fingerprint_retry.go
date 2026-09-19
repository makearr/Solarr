package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// retryPageSize bounds one sweep of the queue. Generous, because the rows are
// small and this runs once, when a key is first entered.
const retryPageSize = 500

// retryMaxPages stops a pathological library taking the request with it: what is
// not covered here is still recovered by a full rescan.
const retryMaxPages = 40

// fingerprintRetrier is apiserver.FingerprintRetrier: once an AcoustID key is
// entered, the files the fingerprint pass skipped for the want of one are
// rescanned (D12). It re-runs them through the ordinary scan rather than a
// pass of its own -- scan.Payload.Directories already exists for exactly this,
// and a rung that re-ran outside the chain could disagree with it.
type fingerprintRetrier struct{ p *process }

func (f fingerprintRetrier) RetryFingerprintSkipped(ctx context.Context) (int, error) {
	attempts := library.NewAttempts(f.p.library)

	directoriesByRoot := make(map[int64]map[string]bool)
	var afterID int64
	for range retryMaxPages {
		files, lastID, err := attempts.ListSkipped(ctx,
			music.PassFingerprint, music.PassOutcomeDisabled, afterID, retryPageSize)
		if err != nil {
			return 0, err
		}
		for _, file := range files {
			if directoriesByRoot[file.RootFolderID] == nil {
				directoriesByRoot[file.RootFolderID] = make(map[string]bool)
			}
			directoriesByRoot[file.RootFolderID][file.Directory] = true
		}
		if lastID == afterID {
			break // a page that advanced nothing is the end of the queue
		}
		afterID = lastID
	}

	var errs []error
	queued := 0
	for rootFolderID, set := range directoriesByRoot {
		categoryID, found := f.p.musicCategoryFor(rootFolderID)
		if !found {
			continue
		}
		directories := make([]string, 0, len(set))
		for directory := range set {
			directories = append(directories, directory)
		}
		if _, err := scan.EnqueueDirectories(ctx, f.p.queue, rootFolderID, categoryID, directories); err != nil {
			errs = append(errs, err)
			continue
		}
		queued += len(directories)
		f.p.logger.Info("rescan queued for files a missing AcoustID key skipped",
			slog.Int64("root_folder_id", rootFolderID), slog.Int("directories", len(directories)))
	}
	return queued, errors.Join(errs...)
}
