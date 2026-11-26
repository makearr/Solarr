package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Why a file is called defective.
const (
	DefectUnreadable  = "unreadable"
	DefectEmpty       = "empty"
	DefectDecodeError = "decode_error"
)

// defectAfterAttempts is how many failed probes of one size make an unreadable file defective: one failure can be the share hiccuping.
const defectAfterAttempts = 2

// FileDefect is one broken or incomplete file of a library.
type FileDefect struct {
	MediaObjectID, RootFolderID, WorkID, SizeBytes, DetectedAt int64
	RelPath, WorkTitle, Reason, Detail                         string
}

// RecordDefect marks a file defective, or updates why.
func (p Physical) RecordDefect(ctx context.Context, mediaObjectID, sizeBytes int64, reason, detail string, at time.Time) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).UpsertFileDefect(ctx, librarydb.UpsertFileDefectParams{
			MediaObjectID: mediaObjectID, Reason: reason, Detail: detail, SizeBytes: sizeBytes, DetectedAt: at.Unix(),
		})
	})
	if err != nil {
		return fmt.Errorf("library: record defect %d: %w", mediaObjectID, err)
	}
	return nil
}

// ClearDefect forgets a file's defect, as after a good probe.
func (p Physical) ClearDefect(ctx context.Context, mediaObjectID int64) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteFileDefect(ctx, mediaObjectID)
	})
	if err != nil {
		return fmt.Errorf("library: clear defect %d: %w", mediaObjectID, err)
	}
	return nil
}

// FileDefects reads the defective files of libraries.
type FileDefects struct{ store *store.Store }

// NewFileDefects builds it over an open library store.
func NewFileDefects(opened *store.Store) FileDefects { return FileDefects{store: opened} }

// List pages the defective files on the roots, after the media object id given.
func (d FileDefects) List(ctx context.Context, rootFolderIDs []int64, afterID int64, limit int) ([]FileDefect, error) {
	roots, err := json.Marshal(rootFolderIDs)
	if err != nil {
		return nil, fmt.Errorf("library: defect roots: %w", err)
	}
	rows, err := librarydb.New(d.store.Read()).ListFileDefects(ctx, librarydb.ListFileDefectsParams{RootFolderIds: string(roots), AfterID: afterID, PageSize: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("library: list defects: %w", err)
	}
	out := make([]FileDefect, len(rows))
	for i, row := range rows {
		out[i] = FileDefect{
			MediaObjectID: row.MediaObjectID, RootFolderID: row.RootFolderID, WorkID: row.WorkID, SizeBytes: row.SizeBytes, DetectedAt: row.DetectedAt,
			RelPath: row.RelPath, WorkTitle: row.WorkTitle, Reason: row.Reason, Detail: row.Detail,
		}
	}
	return out, nil
}

// Count is how many files on the roots are defective.
func (d FileDefects) Count(ctx context.Context, rootFolderIDs []int64) (int64, error) {
	roots, err := json.Marshal(rootFolderIDs)
	if err != nil {
		return 0, fmt.Errorf("library: defect roots: %w", err)
	}
	total, err := librarydb.New(d.store.Read()).CountFileDefects(ctx, string(roots))
	if err != nil {
		return 0, fmt.Errorf("library: count defects: %w", err)
	}
	return total, nil
}

// InRoots keeps the ids that are defective files on the roots, so a bulk action never reaches into another library.
func (d FileDefects) InRoots(ctx context.Context, ids, rootFolderIDs []int64) ([]int64, error) {
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("library: defect ids: %w", err)
	}
	rows, err := librarydb.New(d.store.Read()).ListFileDefectRoots(ctx, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("library: defect roots: %w", err)
	}
	var kept []int64
	for _, row := range rows {
		for _, root := range rootFolderIDs {
			if root == row.RootFolderID {
				kept = append(kept, row.MediaObjectID)
				break
			}
		}
	}
	return kept, nil
}
