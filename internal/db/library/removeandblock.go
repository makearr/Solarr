package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"time"
)

// RootFolderPath answers a root's absolute path for resolving rel_path to the recycle bin; the caller supplies it since this package never reads settings.db (ADR-25).
type RootFolderPath func(rootFolderID int64) (path string, ok bool)

// RemoveAndBlockRequest is ADR-53 section 3's action ("fake: remove it and never let it back"), each step individually overridable.
type RemoveAndBlockRequest struct {
	MediaObjectID int64

	// Recycle bin is step 1: every library_path copy of MediaObjectID across all roots (ADR-11 hardlinks) is recycled together, then the media_object is removed.
	RecycleBin    bool
	RecycleReason Reason
	RecycleRetain time.Duration
	// RemoveEmptyFolders is the library's "Delete empty folders" for what recycle bin leaves behind.
	RemoveEmptyFolders bool
	// ReplacedBy is the media_object that took this file's place (an upgrade, a duplicate resolved); zero when none did.
	ReplacedBy int64

	// Block is step 2: block the file's own hashes so the bytes never enter
	// the library again (blocked_content).
	Block        bool
	OSHash       string
	QuickHash    string
	BlockReason  BlockReason
	BlockNote    string
	BlockTitle   string
	ReleaseGroup string
	BlockSource  BlockSource
	BlockUserID  *int64

	// Blocklist is step 3: the source release must not be re-grabbed
	// (blocklisted_release).
	Blocklist      bool
	IndexerID      *int64
	GUID           string
	ContentHash    string
	BlocklistTitle string
	DesiredItemID  *int64

	// Reopen detaches the file from every desired_item it satisfied and recomputes them even when the file stays;
	// a recycled file always does, so no item keeps counting a file that is gone.
	Reopen bool

	At int64
}

// RemoveAndBlockResult is what actually happened -- each field only set when
// its own step ran, so a caller can report exactly what was done rather than
// assuming every requested step succeeded identically.
type RemoveAndBlockResult struct {
	RecycledPaths []Entry
	Block         *BlockedContent
	Blocklisted   *BlocklistEntry
	ReopenedItems []int64
}

// RemoveAndBlock composes recycle bin, blocked_content, blocklisted_release and desired_item into the ADR-53 action (section 10.8); orchestration only, no new storage.
type RemoveAndBlock struct {
	recycleBin RecycleBin
	blocks     BlockedContentStore
	blocklist  BlocklistStore
	desired    Desired
	physical   Physical
	rootPath   RootFolderPath
}

// NewRemoveAndBlock builds it over the four stores it composes.
func NewRemoveAndBlock(
	recycleBin RecycleBin, blocks BlockedContentStore, blocklist BlocklistStore,
	desired Desired, physical Physical, rootPath RootFolderPath,
) RemoveAndBlock {
	return RemoveAndBlock{
		recycleBin: recycleBin, blocks: blocks, blocklist: blocklist,
		desired: desired, physical: physical, rootPath: rootPath,
	}
}

// Do runs every requested step. Membership (which desired_item this file
// satisfies) is read before anything is deleted, since the media_object's removal
// cascades it away; each item is then detached and recounted.
func (r RemoveAndBlock) Do(ctx context.Context, req RemoveAndBlockRequest) (RemoveAndBlockResult, error) {
	var result RemoveAndBlockResult

	membership, err := r.desired.ItemsForFile(ctx, req.MediaObjectID)
	if err != nil {
		return result, fmt.Errorf("library: remove and block: %w", err)
	}

	// What the person asked to be blocked is written before anything moves: a later failure then still leaves the
	// block in place, and a retry that finds the file gone has nothing left to repeat.
	if req.Block {
		size, err := r.physical.ObjectSize(ctx, req.MediaObjectID)
		if err != nil {
			return result, fmt.Errorf("library: remove and block: %w", err)
		}
		block, err := r.blocks.Create(ctx, NewBlock{
			OSHash: req.OSHash, QuickHash: req.QuickHash, SizeBytes: size,
			Reason: req.BlockReason, Note: req.BlockNote, Title: req.BlockTitle,
			ReleaseGroup: req.ReleaseGroup, Source: req.BlockSource, UserID: req.BlockUserID,
			CreatedAt: req.At,
		})
		if err != nil {
			return result, fmt.Errorf("library: remove and block: block content: %w", err)
		}
		result.Block = &block
	}

	if req.Blocklist {
		entry, err := r.blocklist.Create(ctx, NewBlocklistEntry{
			IndexerID: req.IndexerID, GUID: req.GUID, ContentHash: req.ContentHash,
			Title: req.BlocklistTitle, DesiredItemID: req.DesiredItemID, Reason: BlocklistManual,
			Source: BlocklistSourceUser, CreatedAt: req.At,
		})
		if err != nil {
			return result, fmt.Errorf("library: remove and block: blocklist release: %w", err)
		}
		result.Blocklisted = &entry
	}

	if req.RecycleBin {
		if err := r.doRecycle(ctx, req, &result); err != nil {
			return result, err
		}
	}

	// A file that left the library always leaves the counts true; Reopen asks for the same of a file that stays.
	if req.RecycleBin || req.Reopen {
		if err := r.doReopen(ctx, req.MediaObjectID, membership, &result); err != nil {
			return result, err
		}
	}

	return result, nil
}

func (r RemoveAndBlock) doRecycle(ctx context.Context, req RemoveAndBlockRequest, result *RemoveAndBlockResult) error {
	paths, err := r.physical.PathsForObject(ctx, req.MediaObjectID)
	if err != nil {
		return fmt.Errorf("library: remove and block: %w", err)
	}

	objectID := req.MediaObjectID
	var replacedBy *int64
	if req.ReplacedBy != 0 {
		replacedBy = &req.ReplacedBy
	}
	for _, p := range paths {
		rootPath, ok := r.rootPath(p.RootFolderID)
		if !ok {
			return fmt.Errorf("library: remove and block: no known path for root folder %d", p.RootFolderID)
		}
		rootFolderID := p.RootFolderID
		entry, err := r.recycleBin.Put(ctx, PutRequest{
			RootPath: rootPath, OriginalPath: path.Join(rootPath, p.RelPath),
			RootFolderID: &rootFolderID, MediaObjectID: &objectID,
			Reason: req.RecycleReason, Retain: req.RecycleRetain, RemoveEmptyFolders: req.RemoveEmptyFolders,
			ReplacedByObjectID: replacedBy,
		})
		if err != nil {
			// A copy a scan already found gone has nothing to recycle; stopping here would leave the block half done.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("library: remove and block: recycle bin %q: %w", p.RelPath, err)
		}
		result.RecycledPaths = append(result.RecycledPaths, entry)
	}

	if err := r.physical.DeleteObject(ctx, req.MediaObjectID); err != nil {
		return fmt.Errorf("library: remove and block: %w", err)
	}
	return nil
}

func (r RemoveAndBlock) doReopen(ctx context.Context, mediaObjectID int64, membership []FileMembership, result *RemoveAndBlockResult) error {
	rolesByItem := make(map[int64][]string)
	var order []int64
	for _, m := range membership {
		if rolesByItem[m.DesiredItemID] == nil {
			order = append(order, m.DesiredItemID)
		}
		rolesByItem[m.DesiredItemID] = append(rolesByItem[m.DesiredItemID], m.Role)
	}

	for _, itemID := range order {
		item, err := r.desired.Get(ctx, itemID)
		if err != nil {
			return fmt.Errorf("library: remove and block: reopen item %d: %w", itemID, err)
		}
		if err := r.desired.DetachAndRecompute(ctx, itemID, item.WorkID, mediaObjectID, rolesByItem[itemID]); err != nil {
			return fmt.Errorf("library: remove and block: reopen item %d: %w", itemID, err)
		}
		result.ReopenedItems = append(result.ReopenedItems, itemID)
	}
	return nil
}
