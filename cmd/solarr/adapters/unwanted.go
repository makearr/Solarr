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

// RemoveAndBlock satisfies apiserver.RemoveAndBlocker over
// library.RemoveAndBlock, and answers apiserver.ErrNoSuchFile for a
// media_object id nothing in the library recognises -- library.RemoveAndBlock
// itself does not fail on an unknown id (a Reopen-only or Block-only request
// simply has nothing to do), but the HTTP action needs a real 404 rather than
// a silent, empty 200.
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
		MediaObjectID:    mediaObjectID,
		Quarantine:       req.Quarantine,
		QuarantineReason: library.Reason(req.QuarantineReason),
		QuarantineRetain: time.Duration(req.QuarantineRetainHours) * time.Hour,
		Block:            req.Block,
		OSHash:           req.OSHash, QuickHash: req.QuickHash,
		BlockReason: library.BlockReason(req.BlockReason), BlockNote: req.BlockNote,
		BlockTitle: req.BlockTitle, ReleaseGroup: req.ReleaseGroup,
		Blocklist: req.Blocklist, IndexerID: req.IndexerID, GUID: req.GUID,
		ContentHash: req.ContentHash, BlocklistTitle: req.BlocklistTitle,
		DesiredItemID: req.DesiredItemID, Reopen: req.Reopen, At: at,
	})
	if err != nil {
		return apiserver.RemoveAndBlockOutput{}, err
	}

	output := apiserver.RemoveAndBlockOutput{
		CrossDeviceDeletes: result.CrossDeviceDeletes, ReopenedItems: result.ReopenedItems,
	}
	for _, entry := range result.QuarantinedPaths {
		output.QuarantinedPaths = append(output.QuarantinedPaths, entry.QuarantinePath)
	}
	if result.Block != nil {
		output.BlockID = &result.Block.ID
	}
	if result.Blocklisted != nil {
		output.BlocklistID = &result.Blocklisted.ID
	}
	return output, nil
}

// QuarantineFile is RemoveAndBlockFile's own first step alone: the bytes
// leave the library and stay recoverable, nothing is blocked, and every
// desired_item that file satisfied is reopened so the album reports what it
// really has.
func (a RemoveAndBlock) QuarantineFile(ctx context.Context, mediaObjectID int64, recycling library.Recycling) error {
	_, err := a.Action.Do(ctx, library.RemoveAndBlockRequest{
		MediaObjectID: mediaObjectID, Quarantine: true,
		QuarantineReason: library.ReasonUserDelete, Reopen: true,
		QuarantineRetain: recycling.Retain, RemoveEmptyFolders: recycling.RemoveEmptyFolders,
		At: a.Clock.Now().Unix(),
	})
	return err
}
