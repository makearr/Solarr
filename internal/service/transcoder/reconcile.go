package transcoder

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
)

// Reconcile settles every conversion a restart left half done: a replace is finished or undone so the library holds
// exactly one of the original and the result, a running item goes back to waiting, and work files nobody names are removed.
func (s *Service) Reconcile(ctx context.Context) error {
	journalled, err := s.Items.Journalled(ctx)
	if err != nil {
		return err
	}
	kept := map[string]bool{}
	for _, item := range journalled {
		settled, err := s.settleJournal(ctx, item)
		if err != nil {
			return err
		}
		if !settled {
			kept[item.WorkPath] = true
		}
	}
	running, err := s.Items.Running(ctx)
	if err != nil {
		return err
	}
	for _, item := range running {
		full, found, err := s.Items.Get(ctx, item.MediaObjectID)
		if err != nil || !found || full.State != library.TranscodeRunning {
			continue
		}
		if err := s.interrupted(ctx, full); err != nil {
			return err
		}
	}
	s.cleanWorkFolders(kept)
	return nil
}

// settleJournal finishes or undoes the replace one item was in the middle of; false when its root is not reachable, so the work file must stay.
func (s *Service) settleJournal(ctx context.Context, item library.TranscodeItem) (bool, error) {
	id := item.MediaObjectID
	if item.ReplaceStage == stageRestoring {
		return s.settleRestore(ctx, item)
	}
	if item.State != library.TranscodeRunning {
		return true, s.Items.SetJournal(ctx, id, "", "", item.OriginalRecycleID)
	}
	paths, err := s.Items.Paths(ctx, id)
	if err != nil || len(paths) == 0 {
		return false, err
	}
	root, known := s.Settings.RootFolder(paths[0].RootFolderID)
	if !known || !root.Online {
		return false, nil
	}
	oldAbs := path.Join(root.Path, paths[0].RelPath)
	stage := item.ReplaceStage
	if stage == stageRecycling {
		if _, err := s.FS.Lstat(oldAbs); err == nil {
			if err := s.Items.SetJournal(ctx, id, "", "", 0); err != nil {
				return false, err
			}
			return true, s.release(ctx, id)
		}
		found, taken, err := s.Bin.Find(ctx, oldAbs)
		if err != nil {
			return false, err
		}
		if !taken {
			if err := s.Items.SetJournal(ctx, id, "", "", 0); err != nil {
				return false, err
			}
			return true, s.blocked(ctx, id, FailureSourceMissing)
		}
		item.OriginalRecycleID, stage = found.ID, stagePlacing
	}
	finalRel := withExtension(paths[0].RelPath, strings.TrimPrefix(path.Ext(item.WorkPath), "."))
	_, outputThere := s.FS.Lstat(item.WorkPath)
	_, finalThere := s.FS.Lstat(path.Join(root.Path, finalRel))
	if stage == stagePlacing && (outputThere == nil || finalThere == nil) {
		return true, s.finishPlacing(ctx, item, root.Path, root.ID, paths[0].RelPath, finalRel)
	}
	return true, s.undoReplace(ctx, item)
}

// finishPlacing continues a replace at step 4 or 5 from what the journal and the bin remember.
func (s *Service) finishPlacing(ctx context.Context, item library.TranscodeItem, rootPath string, rootID int64, oldRel, finalRel string) error {
	entry, err := s.Bin.Get(ctx, item.OriginalRecycleID)
	if err != nil {
		return err
	}
	original, _ := s.FS.Stat(entry.RecycledPath)
	return s.place(ctx, placement{
		itemID: item.MediaObjectID, rootFolderID: rootID, rootPath: rootPath, oldRel: oldRel, newRel: finalRel, output: item.WorkPath, entryID: entry.ID,
		original: original, result: library.TranscodeResult{
			OriginalRecycleID: entry.ID, OriginalSizeBytes: entry.SizeBytes, FinishedAt: s.Clock.Now().Unix(),
		},
	})
}

// undoReplace brings the original back from the bin when the result is gone, and sends the item back to waiting.
func (s *Service) undoReplace(ctx context.Context, item library.TranscodeItem) error {
	if item.OriginalRecycleID != 0 {
		if _, err := s.Bin.Restore(ctx, item.OriginalRecycleID); err != nil {
			return describe("restore the original", err)
		}
	}
	if err := s.Items.SetJournal(ctx, item.MediaObjectID, "", "", 0); err != nil {
		return err
	}
	return s.interrupted(ctx, item)
}

// cleanWorkFolders removes the files in every root's work folder that no journal names.
func (s *Service) cleanWorkFolders(kept map[string]bool) {
	seen := map[string]bool{}
	for _, one := range s.Settings.Roots() {
		if seen[one.Path] {
			continue
		}
		seen[one.Path] = true
		folder := path.Join(one.Path, library.RecycleBinDirectory, WorkDirectory)
		entries, err := s.FS.ReadDir(folder)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if full := path.Join(folder, entry.Name); !entry.IsDir && !kept[full] {
				_ = s.FS.Remove(full)
			}
		}
	}
}

// settleRestore finishes a restore a restart interrupted, or clears its journal when nothing had happened yet: the converted file
// still at its path and the original still in the bin.
func (s *Service) settleRestore(ctx context.Context, journalled library.TranscodeItem) (bool, error) {
	id := journalled.MediaObjectID
	item, found, err := s.Items.Get(ctx, id)
	if err != nil {
		return false, err
	}
	if !found || item.State != library.TranscodeDone || item.OriginalRecycleID == 0 {
		return true, s.Items.SetJournal(ctx, id, "", "", item.OriginalRecycleID)
	}
	root, resultRel, err := s.locate(ctx, id)
	if errors.Is(err, ErrRootUnavailable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	entry, err := s.Bin.Get(ctx, item.OriginalRecycleID)
	if err != nil {
		return true, s.Items.SetJournal(ctx, id, "", "", 0)
	}
	if _, there := s.FS.Lstat(path.Join(root.Path, resultRel)); there == nil && entry.RestoredAt == nil {
		return true, s.Items.SetJournal(ctx, id, "", "", item.OriginalRecycleID)
	}
	return true, s.undoConversion(ctx, item, root, resultRel, entry)
}
