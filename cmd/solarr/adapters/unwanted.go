package adapters

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// BlockedContent satisfies apiserver.BlockedContentLister over library.BlockedContentStore.
type BlockedContent struct{ Blocks library.BlockedContentStore }

func (a BlockedContent) ListBlockedContent(
	ctx context.Context, afterID int64, limit int,
) ([]apiserver.BlockedContentRow, error) {
	rows, err := a.Blocks.List(ctx, afterID, int64(limit))
	if err != nil {
		return nil, err
	}
	views := make([]apiserver.BlockedContentRow, len(rows))
	for i, row := range rows {
		views[i] = blockedContentRow(row)
	}
	return views, nil
}

func (a BlockedContent) RemoveBlockedContent(ctx context.Context, id int64) error {
	return a.Blocks.Remove(ctx, id)
}

func blockedContentRow(row library.BlockedContent) apiserver.BlockedContentRow {
	view := apiserver.BlockedContentRow{
		ID: row.ID, OSHash: row.OSHash, QuickHash: row.QuickHash, Chromaprint: row.Chromaprint,
		Phash: row.Phash, SizeBytes: row.SizeBytes, Reason: string(row.Reason), Note: row.Note,
		MediaType: row.MediaType, Title: row.Title, ReleaseGroup: row.ReleaseGroup,
		Source: string(row.Source), CreatedAt: time.Unix(row.CreatedAt, 0), HitCount: row.HitCount,
	}
	if row.ExpiresAt != nil {
		at := time.Unix(*row.ExpiresAt, 0)
		view.ExpiresAt = &at
	}
	if row.LastHitAt != nil {
		at := time.Unix(*row.LastHitAt, 0)
		view.LastHitAt = &at
	}
	return view
}

// RemoveAndBlock satisfies apiserver.RemoveAndBlocker and answers ErrNoSuchFile for an unknown media_object id, because the library call does not fail but the HTTP action needs a 404.
type RemoveAndBlock struct {
	Action   library.RemoveAndBlock
	Physical library.Physical
	Clock    interface{ Now() time.Time }
}

func (a RemoveAndBlock) RemoveAndBlockFile(
	ctx context.Context, mediaObjectID int64, req apiserver.RemoveAndBlockInput,
) (apiserver.RemoveAndBlockOutput, error) {
	exists, err := a.Physical.Exists(ctx, mediaObjectID)
	if err != nil {
		return apiserver.RemoveAndBlockOutput{}, err
	}
	if !exists {
		return apiserver.RemoveAndBlockOutput{}, apiserver.ErrNoSuchFile
	}

	at := a.Clock.Now().Unix()
	result, err := a.Action.Do(ctx, library.RemoveAndBlockRequest{
		MediaObjectID: mediaObjectID,
		RecycleBin:    req.RecycleBin,
		RecycleReason: library.Reason(req.RecycleReason),
		RecycleRetain: time.Duration(req.RecycleRetainHours) * time.Hour,
		Block:         req.Block,
		OSHash:        req.OSHash, QuickHash: req.QuickHash,
		BlockReason: library.BlockReason(req.BlockReason), BlockNote: req.BlockNote,
		BlockTitle: req.BlockTitle, ReleaseGroup: req.ReleaseGroup,
		Blocklist: req.Blocklist, IndexerID: req.IndexerID, GUID: req.GUID,
		ContentHash: req.ContentHash, BlocklistTitle: req.BlocklistTitle,
		DesiredItemID: req.DesiredItemID, Reopen: req.Reopen, At: at,
	})
	if err != nil {
		return apiserver.RemoveAndBlockOutput{}, err
	}

	output := apiserver.RemoveAndBlockOutput{ReopenedItems: result.ReopenedItems}
	for _, entry := range result.RecycledPaths {
		output.RecycledPaths = append(output.RecycledPaths, entry.RecycledPath)
	}
	if result.Block != nil {
		output.BlockID = &result.Block.ID
	}
	if result.Blocklisted != nil {
		output.BlocklistID = &result.Blocklisted.ID
	}
	return output, nil
}

// RecycleFile is RemoveAndBlockFile's first step alone: the bytes leave the library but stay recoverable, nothing is blocked, and satisfied desired_items are recounted.
func (a RemoveAndBlock) RecycleFile(ctx context.Context, mediaObjectID int64, recycling library.Recycling) error {
	return a.RecycleFor(ctx, mediaObjectID, recycling, library.ReasonUserDelete, 0)
}

// RecycleFor is RecycleFile with the reason recorded in the recycle bin, e.g. an upgrade replacing a file.
func (a RemoveAndBlock) RecycleFor(
	ctx context.Context, mediaObjectID int64, recycling library.Recycling, reason library.Reason, replacedBy int64,
) error {
	_, err := a.Action.Do(ctx, library.RemoveAndBlockRequest{
		MediaObjectID: mediaObjectID, RecycleBin: true,
		RecycleReason: reason, ReplacedBy: replacedBy,
		RecycleRetain: recycling.Retain, RemoveEmptyFolders: recycling.RemoveEmptyFolders,
		At: a.Clock.Now().Unix(),
	})
	return err
}
