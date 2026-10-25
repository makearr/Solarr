package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"time"
)

// RootFolderPath answers a root folder's own absolute filesystem path, for
// resolving library_path.rel_path into a real path to the recycle bin. This
// package never reaches into settings.db itself (ADR-25's own database
// split), so a caller supplies the lookup -- the same shape Recycle bin's own
// Sweep already asks a caller for root capacity.
type RootFolderPath func(rootFolderID int64) (path string, ok bool)

// RemoveAndBlockRequest is ADR-53 section 3's "remove and block" action --
// the user's actual workflow ("this is fake, get rid of it and make sure it
// never comes back"), not "add a hash to a list". Each step is individually
// overridable, matching architecture.md's own text.
type RemoveAndBlockRequest struct {
	MediaObjectID int64

	// Recycle bin is step 1. Every copy of these bytes (every library_path
	// row for MediaObjectID, across every root -- ADR-11 cross-entry
	// hardlinking means there can be more than one) is recycled together,
	// then the media_object itself is removed: "remove and block" removes
	// the fake everywhere it exists, not just the one path a caller happened
	// to be looking at.
	RecycleBin    bool
	RecycleReason Reason
	RecycleRetain time.Duration
	// RemoveEmptyFolders is the library's "Delete empty folders" for what recycle bin leaves behind.
	RemoveEmptyFolders bool

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

	// Reopen is step 4: every desired_item this file satisfied is detached
	// from it and recomputed, so it returns to wanting the real thing rather
	// than silently under-reporting what it already has.
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

// RemoveAndBlock composes recycle bin, blocked_content, blocklisted_release
// and desired_item into the one action architecture.md section 10.8/ADR-53
// describes -- each of its own pieces already exists and is independently
// tested; this is the orchestration, not new storage.
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
// satisfies) is read before anything is deleted, since Recycle bin's own
// media_object cleanup would otherwise cascade it away first -- Reopen then
// explicitly detaches each one regardless, which is a harmless no-op for
// whatever the cascade already removed and the only path for whatever it
// did not (Recycle bin not requested).
func (r RemoveAndBlock) Do(ctx context.Context, req RemoveAndBlockRequest) (RemoveAndBlockResult, error) {
	var result RemoveAndBlockResult

	var membership []FileMembership
	if req.Reopen {
		var err error
		membership, err = r.desired.ItemsForFile(ctx, req.MediaObjectID)
		if err != nil {
			return result, fmt.Errorf("library: remove and block: %w", err)
		}
	}

	if req.RecycleBin {
		if err := r.doRecycle(ctx, req, &result); err != nil {
			return result, err
		}
	}

	if req.Block {
		block, err := r.blocks.Create(ctx, NewBlock{
			OSHash: req.OSHash, QuickHash: req.QuickHash, SizeBytes: totalBytes(result.RecycledPaths),
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

	if req.Reopen {
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

func totalBytes(entries []Entry) int64 {
	var total int64
	for _, e := range entries {
		total += e.SizeBytes
	}
	return total
}
