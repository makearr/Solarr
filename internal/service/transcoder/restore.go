package transcoder

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
)

// The refusals of a restore, each a problem the person can read.
var (
	ErrOriginalGone    = errors.New("transcoder: the original is no longer kept")
	ErrNotRestorable   = errors.New("transcoder: only a converted file can be restored")
	ErrOriginalBlocked = errors.New("transcoder: something else sits where the original was")
	ErrRootUnavailable = errors.New("transcoder: the library's folder is not reachable")
)

// Restore puts a converted file's original back: the converted file goes to the bin, the original comes out of it, the library
// takes the original's bytes, and the file rests as not needed until a person retries it.
func (s *Service) Restore(ctx context.Context, mediaObjectID int64) error {
	item, found, err := s.Items.Get(ctx, mediaObjectID)
	if err != nil {
		return err
	}
	if !found {
		return library.ErrNoTranscodeItem
	}
	if item.State != library.TranscodeDone {
		return ErrNotRestorable
	}
	entry, err := s.keptOriginal(ctx, item)
	if err != nil {
		return err
	}
	root, resultRel, err := s.locate(ctx, mediaObjectID)
	if err != nil {
		return err
	}
	resultAbs := path.Join(root.Path, resultRel)
	if entry.OriginalPath != resultAbs {
		if _, taken := s.FS.Lstat(entry.OriginalPath); taken == nil {
			return ErrOriginalBlocked
		}
	}
	if err := s.Items.SetJournal(ctx, mediaObjectID, stageRestoring, "", entry.ID); err != nil {
		return err
	}
	return s.undoConversion(ctx, item, root, resultRel, entry)
}

// keptOriginal is the bin entry that still holds the item's original with its bytes.
func (s *Service) keptOriginal(ctx context.Context, item library.TranscodeItem) (library.Entry, error) {
	entry, err := s.Bin.Get(ctx, item.OriginalRecycleID)
	if err != nil || entry.RestoredAt != nil {
		return library.Entry{}, ErrOriginalGone
	}
	if _, err := s.FS.Lstat(entry.RecycledPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return library.Entry{}, ErrOriginalGone
		}
		return library.Entry{}, err
	}
	return entry, nil
}

// locate is the root and relative path the library holds the file at now.
func (s *Service) locate(ctx context.Context, mediaObjectID int64) (settings.RootFolder, string, error) {
	paths, err := s.Items.Paths(ctx, mediaObjectID)
	if err != nil {
		return settings.RootFolder{}, "", err
	}
	if len(paths) != 1 || paths[0].Missing {
		return settings.RootFolder{}, "", ErrRootUnavailable
	}
	root, known := s.Settings.RootFolder(paths[0].RootFolderID)
	if !known || !root.Online || !root.Writable || root.ReadOnly {
		return settings.RootFolder{}, "", ErrRootUnavailable
	}
	return root, paths[0].RelPath, nil
}

// undoConversion carries a restore from wherever it stopped to its end: it is safe to run again after any step.
func (s *Service) undoConversion(ctx context.Context, item library.TranscodeItem, root settings.RootFolder, resultRel string, entry library.Entry) error {
	ctx = context.WithoutCancel(ctx)
	id := item.MediaObjectID
	resultAbs := path.Join(root.Path, resultRel)
	if entry.RestoredAt == nil {
		if _, err := s.FS.Lstat(resultAbs); err == nil {
			rootID := root.ID
			if _, err := s.Bin.Put(ctx, library.PutRequest{
				RootPath: root.Path, OriginalPath: resultAbs, RootFolderID: &rootID, MediaObjectID: &id, Reason: library.ReasonTranscodeReverted,
				Retain: s.Settings.Retention(item.CategoryID),
			}); err != nil {
				_ = s.Items.SetJournal(ctx, id, "", "", item.OriginalRecycleID)
				return describe("recycle the converted file", err)
			}
		}
		if _, err := s.Bin.Restore(ctx, entry.ID); err != nil {
			return describe("bring the original back", err)
		}
	}
	info, err := s.FS.Stat(entry.OriginalPath)
	if err != nil {
		return describe("read the restored original", err)
	}
	quick, err := library.QuickHash(s.FS, entry.OriginalPath, info.Size)
	if err != nil {
		return describe("hash the restored original", err)
	}
	reasons, _ := json.Marshal([]Reason{{Code: ReasonRestoredByUser, Stream: -1}})
	err = s.Items.RestoreBytes(ctx, library.RestoredFile{
		MediaObjectID: id, RootFolderID: root.ID, ResultRelPath: resultRel, OriginalRel: strings.TrimPrefix(entry.OriginalPath, root.Path+"/"),
		SizeBytes: info.Size, QuickHash: quick, ReasonsJSON: string(reasons),
	})
	if err != nil {
		return err
	}
	if s.Reporter != nil {
		s.Reporter.Reverted(ctx, Converted{
			MediaObjectID: id, CategoryID: item.CategoryID, Kind: item.Kind, Engine: item.Engine, Path: resultRel, OriginalBytes: item.OriginalSizeBytes,
			ResultBytes: item.ResultSizeBytes, EncodeSeconds: item.EncodeSeconds, At: s.Clock.Now(),
		})
	}
	return nil
}
