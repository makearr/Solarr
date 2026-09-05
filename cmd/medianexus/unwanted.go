package main

import (
	"context"
	"time"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/settings"
)

// blockedContentAdapter satisfies apiserver.BlockedContentLister over
// library.BlockedContentStore. Neither package imports the other
// (worksAdapter's own doc comment explains why), so something in between
// has to translate.
type blockedContentAdapter struct{ blocks library.BlockedContentStore }

func (a blockedContentAdapter) ListBlockedContent(
	ctx context.Context, afterID int64, limit int,
) ([]apiserver.BlockedContentRow, error) {
	rows, err := a.blocks.List(ctx, afterID, int64(limit))
	if err != nil {
		return nil, err
	}
	views := make([]apiserver.BlockedContentRow, len(rows))
	for i, row := range rows {
		views[i] = blockedContentRow(row)
	}
	return views, nil
}

func (a blockedContentAdapter) RemoveBlockedContent(ctx context.Context, id int64) error {
	return a.blocks.Remove(ctx, id)
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

// removeAndBlockAdapter satisfies apiserver.RemoveAndBlocker over
// library.RemoveAndBlock, and answers apiserver.ErrNoSuchFile for a
// media_object id nothing in the library recognises -- library.RemoveAndBlock
// itself does not fail on an unknown id (a Reopen-only or Block-only request
// simply has nothing to do), but the HTTP action needs a real 404 rather than
// a silent, empty 200.
type removeAndBlockAdapter struct {
	action   library.RemoveAndBlock
	physical library.Physical
	clock    interface{ Now() time.Time }
}

func (a removeAndBlockAdapter) RemoveAndBlockFile(
	ctx context.Context, mediaObjectID int64, req apiserver.RemoveAndBlockInput,
) (apiserver.RemoveAndBlockOutput, error) {
	exists, err := a.physical.Exists(ctx, mediaObjectID)
	if err != nil {
		return apiserver.RemoveAndBlockOutput{}, err
	}
	if !exists {
		return apiserver.RemoveAndBlockOutput{}, apiserver.ErrNoSuchFile
	}

	at := a.clock.Now().Unix()
	result, err := a.action.Do(ctx, library.RemoveAndBlockRequest{
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

// rootFolderPathFromCache adapts settings.Cache to library.RootFolderPath --
// library.RemoveAndBlock never reaches into settings.db itself (ADR-25's own
// database split).
func rootFolderPathFromCache(cache *settings.Cache) library.RootFolderPath {
	return func(rootFolderID int64) (string, bool) {
		folder, found := cache.Snapshot().RootFolder(rootFolderID)
		if !found {
			return "", false
		}
		return folder.Path, true
	}
}
