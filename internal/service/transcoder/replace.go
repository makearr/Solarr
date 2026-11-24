package transcoder

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// The stages a replace journals; a restart reads them.
const (
	stageRecycling = "recycling"
	stagePlacing   = "placing"
)

// finish replaces the original with an accepted result: the original goes to the recycle bin, the result takes its
// place, and the library learns the new bytes, each step journalled so a restart can finish or undo it.
func (s *Service) finish(ctx context.Context, run *fileRun, result transcode.File, outcome Outcome, engine transcode.Accel, pass int, encodeSeconds int64, output string) error {
	id := run.item.MediaObjectID
	current, err := s.FS.Stat(run.abs)
	if err != nil || current.Size != run.info.Size || !current.ModTime.Equal(run.info.ModTime) {
		s.discard(output)
		return s.release(ctx, id)
	}
	finalRel := withExtension(run.relPath, extensionOf(run.plan.Container))
	finalAbs := path.Join(run.root.Path, finalRel)
	if finalRel != run.relPath {
		if _, taken := s.FS.Lstat(finalAbs); taken == nil {
			s.discard(output)
			return s.failedWith(ctx, id, FailurePathTaken, finalRel)
		}
	}
	if err := s.Items.SetJournal(ctx, id, stageRecycling, output, 0); err != nil {
		return err
	}
	rootID := run.root.ID
	entry, err := s.Bin.Put(ctx, library.PutRequest{
		RootPath: run.root.Path, OriginalPath: run.abs, RootFolderID: &rootID, Reason: library.ReasonTranscoded,
		Retain: s.originalsRetention(run.item.CategoryID),
	})
	if err != nil {
		_ = s.Items.SetJournal(ctx, id, "", "", 0)
		return describe("recycle the original", err)
	}
	if err := s.Items.SetJournal(ctx, id, stagePlacing, output, entry.ID); err != nil {
		return err
	}
	placed := placement{
		itemID: id, rootFolderID: run.root.ID, rootPath: run.root.Path, oldRel: run.relPath, newRel: finalRel, output: output, entryID: entry.ID,
		original: run.info, result: library.TranscodeResult{
			OriginalRecycleID: entry.ID, OriginalSizeBytes: run.info.Size, ResultSizeBytes: result.SizeBytes, ResultRatioPct: int64(outcome.RatioPct),
			Engine: string(engine), EncodeSeconds: encodeSeconds, FinishedAt: s.Clock.Now().Unix(),
		},
	}
	if err := s.place(ctx, placed); err != nil {
		return err
	}
	if err := s.succeeded(ctx, run.item.Kind); err != nil {
		return err
	}
	if s.Reporter != nil {
		s.Reporter.Converted(ctx, Converted{
			MediaObjectID: id, CategoryID: run.item.CategoryID, Kind: run.item.Kind, Engine: string(engine), Path: finalRel,
			OriginalBytes: run.info.Size, ResultBytes: placed.result.ResultSizeBytes, EncodeSeconds: encodeSeconds, Passes: int64(pass) + 1, At: s.Clock.Now(),
		})
	}
	return nil
}

func (s *Service) originalsRetention(categoryID int64) time.Duration {
	if days := s.Settings.Global().Originals.RetainDays; days > 0 {
		return time.Duration(days) * 24 * time.Hour
	}
	return s.Settings.Retention(categoryID)
}

// placement is what steps 4 to 6 of a replace need; a restart rebuilds it from the journal.
type placement struct {
	itemID, rootFolderID, entryID int64
	rootPath, oldRel, newRel      string
	output                        string
	original                      fsx.FileInfo
	result                        library.TranscodeResult
}

// place moves the result to the original's path, records the new bytes and clears the journal; it is safe to run again from any point.
func (s *Service) place(ctx context.Context, placed placement) error {
	ctx = context.WithoutCancel(ctx)
	finalAbs := path.Join(placed.rootPath, placed.newRel)
	if _, err := s.FS.Lstat(placed.output); err == nil {
		if err := s.FS.Rename(placed.output, finalAbs); err != nil {
			return describe("place the result", err)
		}
		s.carryOwnership(finalAbs, placed.original)
	}
	info, err := s.FS.Stat(finalAbs)
	if err != nil {
		return describe("read the placed result", err)
	}
	quick, err := library.QuickHash(s.FS, finalAbs, info.Size)
	if err != nil {
		return describe("hash the placed result", err)
	}
	placed.result.ResultSizeBytes = info.Size
	if err := s.Items.ReplaceBytes(ctx, library.ReplacedFile{
		MediaObjectID: placed.itemID, RootFolderID: placed.rootFolderID, OldRelPath: placed.oldRel, NewRelPath: placed.newRel,
		SizeBytes: info.Size, QuickHash: quick, Result: placed.result,
	}); err != nil {
		return err
	}
	return s.Items.SetJournal(ctx, placed.itemID, "", "", placed.entryID)
}

// carryOwnership gives the result the original's mode, owner and date where the filesystem allows; none of it can fail a replace.
func (s *Service) carryOwnership(target string, original fsx.FileInfo) {
	_ = s.FS.Chmod(target, original.Mode.Perm())
	if original.UID >= 0 && original.GID >= 0 {
		_ = s.FS.Chown(target, original.UID, original.GID)
	}
	if !original.ModTime.IsZero() {
		_ = s.FS.Chtimes(target, original.ModTime)
	}
}

// withExtension is the path with its extension changed.
func withExtension(relPath, extension string) string {
	current := path.Ext(relPath)
	if strings.EqualFold(strings.TrimPrefix(current, "."), extension) {
		return relPath
	}
	return strings.TrimSuffix(relPath, current) + "." + extension
}
